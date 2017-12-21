// Package postgreskeystore provides an implementation of bakery.RootKeyStore
// that uses Postgres as a persistent store.
package postgresrootkeystore

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/juju/loggo"
	"golang.org/x/net/context"
	"gopkg.in/errgo.v1"
	"gopkg.in/mgo.v2"

	"gopkg.in/macaroon-bakery.v2/bakery"
)

// Functions defined as variables so they can be overidden
// for testing.
var (
	timeNow        = time.Now
	rootKeysFindId = (*RootKeys).findId
)

var logger = loggo.GetLogger("bakery.postgresrootkeystore")

// maxPolicyCache holds the maximum number of store policies that can
// hold cached keys in a given RootKeys instance.
//
// 100 is probably overkill, given that practical systems will
// likely only have a small number of active policies on any given
// macaroon collection.
const maxPolicyCache = 100

// RootKeys represents a cache of macaroon root keys.
type RootKeys struct {
	db    *sql.DB
	table string
	stmts [numStmts]*sql.Stmt

	// initDBOnce guards initDBErr.
	initDBOnce sync.Once
	initDBErr  error

	maxCacheSize int

	// TODO (rogpeppe) use RWMutex instead of Mutex here so that
	// it's faster in the probably-common case that we
	// have many contended readers.
	mu       sync.Mutex
	oldCache map[string]rootKey
	cache    map[string]rootKey

	// current holds the current root key for each store policy.
	current map[Policy]rootKey
}

type rootKey struct {
	id      []byte
	created time.Time
	expires time.Time
	rootKey []byte
}

// isValid reports whether the root key contains a key. Note that we
// always generate non-empty root keys, so we use this to find
// whether the root key is empty or not.
func (rk rootKey) isValid() bool {
	return rk.rootKey != nil
}

// isValidWithPolicy reports whether the given root key
// is currently valid to use with the given store policy.
func (rk rootKey) isValidWithPolicy(p Policy) bool {
	if !rk.isValid() {
		return false
	}
	now := timeNow()
	return afterEq(rk.created, now.Add(-p.GenerateInterval)) &&
		afterEq(rk.expires, now.Add(p.ExpiryDuration)) &&
		beforeEq(rk.expires, now.Add(p.ExpiryDuration+p.GenerateInterval))
}

// NewRootKeys returns a root-keys cache that
// uses the given table in the given Postgres database for storage
// and is limited in size to approximately the given size.
// The table will be created lazily when the root key store
// is first used.
//
// The returned RootKeys instance must be closed after use.
//
// It also creates other SQL resources using the table name
// as a prefix.
//
// Use the NewStore method to obtain a RootKeyStore
// implementation suitable for particular root key
// lifetimes.
func NewRootKeys(db *sql.DB, table string, maxCacheSize int) *RootKeys {
	return &RootKeys{
		maxCacheSize: maxCacheSize,
		cache:        make(map[string]rootKey),
		current:      make(map[Policy]rootKey),
		db:           db,
		table:        table,
	}
}

// Close closes the RootKeys instance. This must be called after using the instance.
func (s *RootKeys) Close() error {
	var retErr error
	for _, stmt := range s.stmts {
		if stmt == nil {
			continue
		}
		if err := stmt.Close(); err != nil && retErr == nil {
			retErr = err
		}
	}
	return errgo.Mask(retErr)
}

// Policy holds a store policy for root keys.
type Policy struct {
	// GenerateInterval holds the maximum length of time
	// for which a root key will be returned from RootKey.
	// If this is zero, it defaults to ExpiryDuration.
	GenerateInterval time.Duration

	// ExpiryDuration holds the minimum length of time that
	// root keys will be valid for after they are returned from
	// RootKey. The maximum length of time that they
	// will be valid for is ExpiryDuration + GenerateInterval.
	ExpiryDuration time.Duration
}

// NewStore returns a new RootKeyStore implementation that
// stores and obtains root keys from the given collection.
//
// Root keys will be generated and stored following the
// given store policy.
//
// It is expected that all collections passed to a given Store's
// NewStore method should refer to the same underlying collection.
func (s *RootKeys) NewStore(policy Policy) bakery.RootKeyStore {
	if policy.GenerateInterval == 0 {
		policy.GenerateInterval = policy.ExpiryDuration
	}
	return &store{
		keys:   s,
		policy: policy,
	}
}

// get gets the root key for the given id, trying the cache first and
// falling back to calling fallback if it's not found there.
//
// If the key does not exist or has expired, it returns
// bakery.ErrNotFound.
//
// Called with s.mu locked.
func (s *RootKeys) get(id []byte) (rootKey, error) {
	key, cached, err := s.get0(id)
	if err != nil && err != bakery.ErrNotFound {
		return rootKey{}, errgo.Mask(err)
	}
	if err == nil && timeNow().After(key.expires) {
		key = rootKey{}
		err = bakery.ErrNotFound
	}
	if !cached {
		s.addCache(id, key)
	}
	return key, err
}

// get0 is the inner version of RootKeys.get. It returns an item and reports
// whether it was found in the cache, but doesn't check whether the
// item has expired or move the returned item to s.cache.
func (s *RootKeys) get0(id []byte) (key rootKey, inCache bool, err error) {
	if k, ok := s.cache[string(id)]; ok {
		if !k.isValid() {
			return rootKey{}, true, bakery.ErrNotFound
		}
		return k, true, nil
	}
	if k, ok := s.oldCache[string(id)]; ok {
		if !k.isValid() {
			return rootKey{}, false, bakery.ErrNotFound
		}
		return k, false, nil
	}
	logger.Infof("cache miss for %q", id)
	// Try to find the root key in the database. Note that we
	// indirect through a variable rather than calling
	// RootKeys.findId directly so that tests can check whether
	// we're using it rather than using the cache.
	k, err := rootKeysFindId(s, id)
	return k, false, err
}

// addCache adds the given key to the cache.
// Called with s.mu locked.
func (s *RootKeys) addCache(id []byte, k rootKey) {
	if len(s.cache) >= s.maxCacheSize {
		s.oldCache = s.cache
		s.cache = make(map[string]rootKey)
	}
	s.cache[string(id)] = k
}

// setCurrent sets the current key for the given store policy.
// Called with s.mu locked.
func (s *RootKeys) setCurrent(policy Policy, key rootKey) {
	if len(s.current) > maxPolicyCache {
		// Sanity check to avoid possibly memory leak:
		// if some client is using arbitrarily many store
		// policies, we don't want s.keys.current to endlessly
		// expand, so just kill the cache if it grows too big.
		// This will result in worse performance but it shouldn't
		// happen in practice and it's better than using endless
		// space.
		s.current = make(map[Policy]rootKey)
	}
	s.current[policy] = key
}

type store struct {
	keys   *RootKeys
	policy Policy
	coll   *mgo.Collection
}

// Get implements bakery.RootKeyStore.Get.
func (s *store) Get(ctx context.Context, id []byte) ([]byte, error) {
	s.keys.mu.Lock()
	defer s.keys.mu.Unlock()

	key, err := s.keys.get(id)
	if err != nil {
		return nil, err
	}
	return key.rootKey, nil
}

// RootKey implements bakery.RootKeyStore.RootKey by
// returning an existing key from the cache when compatible
// with the current policy.
func (s *store) RootKey(context.Context) ([]byte, []byte, error) {
	if key := s.rootKeyFromCache(); key.isValid() {
		return key.rootKey, key.id, nil
	}
	logger.Debugf("root key cache miss")
	// Try to find a root key from the collection.
	// It doesn't matter much if two concurrent mongo
	// clients are doing this at the same time because
	// we don't mind if there are more keys than necessary.
	//
	// Note that this query mirrors the logic found in
	// store.rootKeyFromCache.
	key, err := s.keys.findBestRootKey(s.policy)
	if err != nil {
		return nil, nil, errgo.Notef(err, "cannot query existing keys")
	}
	if !key.isValid() {
		// No keys found anywhere, so let's create one.
		var err error
		key, err = s.generateKey()
		if err != nil {
			return nil, nil, errgo.Notef(err, "cannot generate key")
		}
		logger.Infof("new root key id %q; created %v; expires %v", key.id, key.created, key.expires)
		if err := s.keys.insertKey(key); err != nil {
			return nil, nil, errgo.Notef(err, "cannot create root key")
		}
	}
	s.keys.mu.Lock()
	defer s.keys.mu.Unlock()
	s.keys.addCache(key.id, key)
	s.keys.setCurrent(s.policy, key)
	return key.rootKey, key.id, nil
}

// rootKeyFromCache returns a root key from the cached keys.
// If no keys are found that are valid for s.policy, it returns
// the zero key.
func (s *store) rootKeyFromCache() rootKey {
	s.keys.mu.Lock()
	defer s.keys.mu.Unlock()
	if k, ok := s.keys.current[s.policy]; ok && k.isValidWithPolicy(s.policy) {
		return k
	}

	// Find the most recently created key that's consistent with the
	// store policy.
	var current rootKey
	for _, k := range s.keys.cache {
		if k.isValidWithPolicy(s.policy) && k.created.After(current.created) {
			current = k
		}
	}
	if current.isValid() {
		s.keys.current[s.policy] = current
		return current
	}
	return rootKey{}
}

func (s *store) generateKey() (rootKey, error) {
	newKey, err := randomBytes(24)
	if err != nil {
		return rootKey{}, err
	}
	newId, err := randomBytes(16)
	if err != nil {
		return rootKey{}, err
	}
	now := timeNow()
	return rootKey{
		created: now,
		expires: now.Add(s.policy.ExpiryDuration + s.policy.GenerateInterval),
		// TODO return just newId when we know we can always
		// use non-text macaroon ids.
		id:      []byte(fmt.Sprintf("%x", newId)),
		rootKey: newKey,
	}, nil
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		return nil, fmt.Errorf("cannot generate %d random bytes: %v", n, err)
	}
	return b, nil
}

// afterEq reports whether t0 is after or equal to t1.
func afterEq(t0, t1 time.Time) bool {
	return !t0.Before(t1)
}

// beforeEq reports whether t1 is before or equal to t0.
func beforeEq(t0, t1 time.Time) bool {
	return !t0.After(t1)
}
