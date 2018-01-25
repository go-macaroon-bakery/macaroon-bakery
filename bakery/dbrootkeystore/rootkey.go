// Package dbkeystore provides the underlying basis for a bakery.RootKeyStore
// that uses a database as a persistent store and provides flexible policies
// for root key storage lifetime.
package dbrootkeystore

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"

	"github.com/juju/loggo"
	"golang.org/x/net/context"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2/bakery"
)

var logger = loggo.GetLogger("bakery.dbrootkeystore")

// maxPolicyCache holds the maximum number of store policies that can
// hold cached keys in a given RootKeys instance.
//
// 100 is probably overkill, given that practical systems will
// likely only have a small number of active policies on any given
// macaroon collection.
const maxPolicyCache = 100

// RootKeys represents a cache of macaroon root keys.
type RootKeys struct {
	maxCacheSize int
	clock        Clock

	// TODO (rogpeppe) use RWMutex instead of Mutex here so that
	// it's faster in the probably-common case that we
	// have many contended readers.
	mu       sync.Mutex
	oldCache map[string]RootKey
	cache    map[string]RootKey

	// current holds the current root key for each store policy.
	current map[Policy]RootKey
}

// Clock can be used to provide a mockable time
// of day for testing.
type Clock interface {
	Now() time.Time
}

// Backing holds the interface used to store keys in the underlying
// database used as a backing store by RootKeyStore.
type Backing interface {
	// GetKey gets the key with the given id from the
	// backing store. If the key is not found, it should
	// return an error with a bakery.ErrNotFound cause.
	GetKey(id []byte) (RootKey, error)

	// FindLatestKey returns the most recently created root key k
	// such that all of the following conditions hold:
	//
	// 	k.Created >= createdAfter
	//	k.Expires >= expiresAfter
	//	k.Expires <= expiresBefore
	//
	// If no such key was found, the zero root key should be returned
	// with a nil error.
	FindLatestKey(createdAfter, expiresAfter, expiresBefore time.Time) (RootKey, error)

	// InsertKey inserts the given root key into the backing store.
	// It may return an error if the id or key already exist.
	InsertKey(key RootKey) error
}

// RootKey is the type stored in the underlying database.
type RootKey struct {
	// Id holds the id of the root key.
	Id []byte `bson:"_id"`
	// Created holds the time that the root key was created.
	Created time.Time
	// Expires holds the time that the root key expires.
	Expires time.Time
	// RootKey holds the root key secret itself.
	RootKey []byte
}

// IsValid reports whether the root key contains a key. Note that we
// always generate non-empty root keys, so we use this to find
// whether the root key is empty or not.
func (rk RootKey) IsValid() bool {
	return rk.RootKey != nil
}

// IsValidWithPolicy reports whether the given root key
// is valid to use at the given time with the given store policy.
func (rk RootKey) IsValidWithPolicy(p Policy, now time.Time) bool {
	if !rk.IsValid() {
		return false
	}
	return afterEq(rk.Created, now.Add(-p.GenerateInterval)) &&
		afterEq(rk.Expires, now.Add(p.ExpiryDuration)) &&
		beforeEq(rk.Expires, now.Add(p.ExpiryDuration+p.GenerateInterval))
}

// NewRootKeys returns a root-keys cache that
// is limited in size to approximately the given size.
//
// The NewStore method returns a store implementation
// that uses specific store policy and backing database
// implementation.
//
// If clock is non-nil, it will be used to find the current
// time, otherwise time.Now will be used.
func NewRootKeys(maxCacheSize int, clock Clock) *RootKeys {
	if clock == nil {
		clock = wallClock{}
	}
	return &RootKeys{
		maxCacheSize: maxCacheSize,
		cache:        make(map[string]RootKey),
		current:      make(map[Policy]RootKey),
		clock:        clock,
	}
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
// It is expected that all Backing instances passed to a given Store's
// NewStore method should refer to the same underlying database.
func (s *RootKeys) NewStore(b Backing, policy Policy) bakery.RootKeyStore {
	if policy.GenerateInterval == 0 {
		policy.GenerateInterval = policy.ExpiryDuration
	}
	return &store{
		keys:    s,
		backing: b,
		policy:  policy,
	}
}

// get gets the root key for the given id, trying the cache first and
// falling back to calling fallback if it's not found there.
//
// If the key does not exist or has expired, it returns
// bakery.ErrNotFound.
//
// Called with s.mu locked.
func (s *RootKeys) get(id []byte, b Backing) (RootKey, error) {
	key, cached, err := s.get0(id, b)
	if err != nil && err != bakery.ErrNotFound {
		return RootKey{}, errgo.Mask(err)
	}
	if err == nil && s.clock.Now().After(key.Expires) {
		key = RootKey{}
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
func (s *RootKeys) get0(id []byte, b Backing) (key RootKey, inCache bool, err error) {
	if k, ok := s.cache[string(id)]; ok {
		if !k.IsValid() {
			return RootKey{}, true, bakery.ErrNotFound
		}
		return k, true, nil
	}
	if k, ok := s.oldCache[string(id)]; ok {
		if !k.IsValid() {
			return RootKey{}, false, bakery.ErrNotFound
		}
		return k, false, nil
	}
	logger.Infof("cache miss for %q", id)
	k, err := b.GetKey(id)
	return k, false, err
}

// addCache adds the given key to the cache.
// Called with s.mu locked.
func (s *RootKeys) addCache(id []byte, k RootKey) {
	if len(s.cache) >= s.maxCacheSize {
		s.oldCache = s.cache
		s.cache = make(map[string]RootKey)
	}
	s.cache[string(id)] = k
}

// setCurrent sets the current key for the given store policy.
// Called with s.mu locked.
func (s *RootKeys) setCurrent(policy Policy, key RootKey) {
	if len(s.current) > maxPolicyCache {
		// Sanity check to avoid possibly memory leak:
		// if some client is using arbitrarily many store
		// policies, we don't want s.keys.current to endlessly
		// expand, so just kill the cache if it grows too big.
		// This will result in worse performance but it shouldn't
		// happen in practice and it's better than using endless
		// space.
		s.current = make(map[Policy]RootKey)
	}
	s.current[policy] = key
}

type store struct {
	keys    *RootKeys
	policy  Policy
	backing Backing
}

// Get implements bakery.RootKeyStore.Get.
func (s *store) Get(ctx context.Context, id []byte) ([]byte, error) {
	s.keys.mu.Lock()
	defer s.keys.mu.Unlock()

	key, err := s.keys.get(id, s.backing)
	if err != nil {
		return nil, err
	}
	return key.RootKey, nil
}

// RootKey implements bakery.RootKeyStore.RootKey by
// returning an existing key from the cache when compatible
// with the current policy.
func (s *store) RootKey(context.Context) ([]byte, []byte, error) {
	if key := s.rootKeyFromCache(); key.IsValid() {
		return key.RootKey, key.Id, nil
	}
	logger.Debugf("root key cache miss")
	// Try to find a root key from the collection.
	// It doesn't matter much if two concurrent mongo
	// clients are doing this at the same time because
	// we don't mind if there are more keys than necessary.
	//
	// Note that this query mirrors the logic found in
	// store.rootKeyFromCache.
	key, err := s.findBestRootKey()
	if err != nil {
		return nil, nil, errgo.Notef(err, "cannot query existing keys")
	}
	if !key.IsValid() {
		// No keys found anywhere, so let's create one.
		var err error
		key, err = s.generateKey()
		if err != nil {
			return nil, nil, errgo.Notef(err, "cannot generate key")
		}
		logger.Infof("new root key id %q; created %v; expires %v", key.Id, key.Created, key.Expires)
		if err := s.backing.InsertKey(key); err != nil {
			return nil, nil, errgo.Notef(err, "cannot create root key")
		}
	}
	s.keys.mu.Lock()
	defer s.keys.mu.Unlock()
	s.keys.addCache(key.Id, key)
	s.keys.setCurrent(s.policy, key)
	return key.RootKey, key.Id, nil
}

func (s *store) findBestRootKey() (RootKey, error) {
	now := s.keys.clock.Now()
	createdAfter := now.Add(-s.policy.GenerateInterval)
	expiresAfter := now.Add(s.policy.ExpiryDuration)
	expiresBefore := now.Add(s.policy.ExpiryDuration + s.policy.GenerateInterval)
	return s.backing.FindLatestKey(createdAfter, expiresAfter, expiresBefore)
}

// rootKeyFromCache returns a root key from the cached keys.
// If no keys are found that are valid for s.policy, it returns
// the zero key.
func (s *store) rootKeyFromCache() RootKey {
	s.keys.mu.Lock()
	defer s.keys.mu.Unlock()
	if k, ok := s.keys.current[s.policy]; ok && k.IsValidWithPolicy(s.policy, s.keys.clock.Now()) {
		return k
	}

	// Find the most recently created key that's consistent with the
	// store policy.
	var current RootKey
	for _, k := range s.keys.cache {
		if k.IsValidWithPolicy(s.policy, s.keys.clock.Now()) && k.Created.After(current.Created) {
			current = k
		}
	}
	if current.IsValid() {
		s.keys.current[s.policy] = current
		return current
	}
	return RootKey{}
}

func (s *store) generateKey() (RootKey, error) {
	newKey, err := randomBytes(24)
	if err != nil {
		return RootKey{}, err
	}
	newId, err := randomBytes(16)
	if err != nil {
		return RootKey{}, err
	}
	now := s.keys.clock.Now()
	return RootKey{
		Created: now,
		Expires: now.Add(s.policy.ExpiryDuration + s.policy.GenerateInterval),
		// TODO return just newId when we know we can always
		// use non-text macaroon ids.
		Id:      []byte(fmt.Sprintf("%x", newId)),
		RootKey: newKey,
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

type wallClock struct{}

func (wallClock) Now() time.Time {
	return time.Now()
}
