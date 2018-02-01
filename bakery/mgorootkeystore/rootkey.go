// Package mgorootkeystore provides an implementation of bakery.RootKeyStore
// that uses MongoDB as a persistent store.
package mgorootkeystore

import (
	"time"

	"gopkg.in/errgo.v1"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/bakery/dbrootkeystore"
)

// Functions defined as variables so they can be overidden
// for testing.
var (
	clock               dbrootkeystore.Clock
	mgoCollectionFindId = (*mgo.Collection).FindId
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
}

// NewRootKeys returns a root-keys cache that
// is limited in size to approximately the given size.
//
// The NewStore method returns a store implementation
// that uses a specific mongo collection and store
// policy.
func NewRootKeys(maxCacheSize int) *RootKeys {
	return &RootKeys{
		keys: dbrootkeystore.NewRootKeys(maxCacheSize, clock),
	}
}

// NewStore returns a new RootKeyStore implementation that
// stores and obtains root keys from the given collection.
//
// Root keys will be generated and stored following the
// given store policy.
//
// It is expected that all collections passed to a given Store's
// NewStore method should refer to the same underlying collection.
func (s *RootKeys) NewStore(c *mgo.Collection, policy Policy) bakery.RootKeyStore {
	return s.keys.NewStore(backing{c}, dbrootkeystore.Policy(policy))
}

var indexes = []mgo.Index{{
	Key: []string{"-created"},
}, {
	Key:         []string{"expires"},
	ExpireAfter: time.Second,
}}

// EnsureIndex ensures that the required indexes exist on the
// collection that will be used for root key store.
// This should be called at least once before using NewStore.
func (s *RootKeys) EnsureIndex(c *mgo.Collection) error {
	for _, idx := range indexes {
		if err := c.EnsureIndex(idx); err != nil {
			return errgo.Notef(err, "cannot ensure index for %q on %q", idx.Key, c.Name)
		}
	}
	return nil
}

type backing struct {
	coll *mgo.Collection
}

func (b backing) GetKey(id []byte) (dbrootkeystore.RootKey, error) {
	var key dbrootkeystore.RootKey
	err := mgoCollectionFindId(b.coll, id).One(&key)
	if err != nil {
		if err == mgo.ErrNotFound {
			return b.getLegacyFromMongo(string(id))
		}
		return dbrootkeystore.RootKey{}, errgo.Notef(err, "cannot get key from database")
	}
	// TODO migrate the key from the old format to the new format.
	return key, nil
}

// getLegacyFromMongo gets a value from the old version of the
// root key document which used a string key rather than a []byte
// key.
func (b backing) getLegacyFromMongo(id string) (dbrootkeystore.RootKey, error) {
	var key dbrootkeystore.RootKey
	err := mgoCollectionFindId(b.coll, id).One(&key)
	if err != nil {
		if err == mgo.ErrNotFound {
			return dbrootkeystore.RootKey{}, bakery.ErrNotFound
		}
		return dbrootkeystore.RootKey{}, errgo.Notef(err, "cannot get key from database")
	}
	return key, nil
}

func (b backing) FindLatestKey(createdAfter, expiresAfter, expiresBefore time.Time) (dbrootkeystore.RootKey, error) {
	var key dbrootkeystore.RootKey
	err := b.coll.Find(bson.D{{
		"created", bson.D{{"$gte", createdAfter}},
	}, {
		"expires", bson.D{
			{"$gte", expiresAfter},
			{"$lte", expiresBefore},
		},
	}}).Sort("-created").One(&key)
	if err != nil && err != mgo.ErrNotFound {
		return dbrootkeystore.RootKey{}, errgo.Notef(err, "cannot query existing keys")
	}
	return key, nil
}

func (b backing) InsertKey(key dbrootkeystore.RootKey) error {
	if err := b.coll.Insert(key); err != nil {
		return errgo.Notef(err, "mongo insert failed")
	}
	return nil
}
