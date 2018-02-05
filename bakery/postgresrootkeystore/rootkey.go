// Package postgreskeystore provides an implementation of bakery.RootKeyStore
// that uses Postgres as a persistent store.
package postgresrootkeystore

import (
	"database/sql"
	"sync"
	"time"

	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/bakery/dbrootkeystore"
)

// Variables defined so they can be overidden for testing.
var (
	clock      dbrootkeystore.Clock
	newBacking = func(s *RootKeys) dbrootkeystore.Backing {
		return backing{s}
	}
)

// TODO it would be nice if could make Policy
// a type alias for dbrootkeystore.Policy,
// but we want to be able to support versions
// of Go from before type aliases were introduced.

// Policy holds a store policy for root keys.
type Policy dbrootkeystore.Policy

// maxPolicyCache holds the maximum number of store policies that can
// hold cached keys in a given RootKeys instance.
//
// 100 is probably overkill, given that practical systems will
// likely only have a small number of active policies on any given
// macaroon collection.
const maxPolicyCache = 100

// RootKeys represents a cache of macaroon root keys.
type RootKeys struct {
	keys *dbrootkeystore.RootKeys

	db    *sql.DB
	table string
	stmts [numStmts]*sql.Stmt

	// initDBOnce guards initDBErr.
	initDBOnce sync.Once
	initDBErr  error
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
		keys:  dbrootkeystore.NewRootKeys(maxCacheSize, clock),
		db:    db,
		table: table,
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

// NewStore returns a new RootKeyStore implementation that
// stores and obtains root keys from the given collection.
//
// Root keys will be generated and stored following the
// given store policy.
//
// It is expected that all collections passed to a given Store's
// NewStore method should refer to the same underlying collection.
func (s *RootKeys) NewStore(policy Policy) bakery.RootKeyStore {
	b := newBacking(s)
	return s.keys.NewStore(b, dbrootkeystore.Policy(policy))
}

// backing implements dbrootkeystore.Backing by using Postgres as
// a backing store.
type backing struct {
	keys *RootKeys
}

// GetKey implements dbrootkeystore.Backing.GetKey.
func (b backing) GetKey(id []byte) (dbrootkeystore.RootKey, error) {
	return b.keys.getKey(id)
}

// InsertKey implements dbrootkeystore.Backing.InsertKey.
func (b backing) InsertKey(key dbrootkeystore.RootKey) error {
	return b.keys.insertKey(key)
}

// FindLatestKey implements dbrootkeystore.Backing.FindLatestKey.
func (b backing) FindLatestKey(createdAfter, expiresAfter, expiresBefore time.Time) (dbrootkeystore.RootKey, error) {
	return b.keys.findLatestKey(createdAfter, expiresAfter, expiresBefore)
}
