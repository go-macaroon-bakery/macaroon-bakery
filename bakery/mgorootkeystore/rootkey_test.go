package mgorootkeystore_test

import (
	"fmt"
	"testing"
	"time"

	qt "github.com/frankban/quicktest"
	"github.com/juju/mgotest"
	"golang.org/x/net/context"
	"gopkg.in/mgo.v2"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/bakery/dbrootkeystore"
	"gopkg.in/macaroon-bakery.v2/bakery/mgorootkeystore"
)

var epoch = time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)

var isValidWithPolicyTests = []struct {
	about  string
	policy mgorootkeystore.Policy
	now    time.Time
	key    dbrootkeystore.RootKey
	expect bool
}{{
	about: "success",
	policy: mgorootkeystore.Policy{
		GenerateInterval: 2 * time.Minute,
		ExpiryDuration:   3 * time.Minute,
	},
	now: epoch.Add(20 * time.Minute),
	key: dbrootkeystore.RootKey{
		Created: epoch.Add(19 * time.Minute),
		Expires: epoch.Add(24 * time.Minute),
		Id:      []byte("id"),
		RootKey: []byte("key"),
	},
	expect: true,
}, {
	about: "empty root key",
	policy: mgorootkeystore.Policy{
		GenerateInterval: 2 * time.Minute,
		ExpiryDuration:   3 * time.Minute,
	},
	now:    epoch.Add(20 * time.Minute),
	key:    dbrootkeystore.RootKey{},
	expect: false,
}, {
	about: "created too early",
	policy: mgorootkeystore.Policy{
		GenerateInterval: 2 * time.Minute,
		ExpiryDuration:   3 * time.Minute,
	},
	now: epoch.Add(20 * time.Minute),
	key: dbrootkeystore.RootKey{
		Created: epoch.Add(18*time.Minute - time.Millisecond),
		Expires: epoch.Add(24 * time.Minute),
		Id:      []byte("id"),
		RootKey: []byte("key"),
	},
	expect: false,
}, {
	about: "expires too early",
	policy: mgorootkeystore.Policy{
		GenerateInterval: 2 * time.Minute,
		ExpiryDuration:   3 * time.Minute,
	},
	now: epoch.Add(20 * time.Minute),
	key: dbrootkeystore.RootKey{
		Created: epoch.Add(19 * time.Minute),
		Expires: epoch.Add(21 * time.Minute),
		Id:      []byte("id"),
		RootKey: []byte("key"),
	},
	expect: false,
}, {
	about: "expires too late",
	policy: mgorootkeystore.Policy{
		GenerateInterval: 2 * time.Minute,
		ExpiryDuration:   3 * time.Minute,
	},
	now: epoch.Add(20 * time.Minute),
	key: dbrootkeystore.RootKey{
		Created: epoch.Add(19 * time.Minute),
		Expires: epoch.Add(25*time.Minute + time.Millisecond),
		Id:      []byte("id"),
		RootKey: []byte("key"),
	},
	expect: false,
}}

func TestIsValidWithPolicy(t *testing.T) {
	c := qt.New(t)
	var now time.Time
	c.Patch(mgorootkeystore.Clock, clockVal(&now))
	for i, test := range isValidWithPolicyTests {
		c.Logf("test %d: %v", i, test.about)
		c.Assert(test.key.IsValidWithPolicy(dbrootkeystore.Policy(test.policy), test.now), qt.Equals, test.expect)
	}
}

func TestRootKeyUsesKeysValidWithPolicy(t *testing.T) {
	c := qt.New(t)
	// We re-use the TestIsValidWithPolicy tests so that we
	// know that the mongo logic uses the same behaviour.
	var now time.Time
	c.Patch(mgorootkeystore.Clock, clockVal(&now))
	for _, test := range isValidWithPolicyTests {
		c.Run(test.about, func(c *qt.C) {
			if test.key.RootKey == nil {
				// We don't store empty root keys in the database.
				c.Skip("skipping test with empty root key")
			}
			coll := testColl(c)
			// Prime the collection with the root key document.
			err := coll.Insert(test.key)
			c.Assert(err, qt.IsNil)

			store := mgorootkeystore.NewRootKeys(10).NewStore(coll, test.policy)
			now = test.now
			key, id, err := store.RootKey(context.Background())
			c.Assert(err, qt.IsNil)
			if test.expect {
				c.Assert(string(id), qt.Equals, "id")
				c.Assert(string(key), qt.Equals, "key")
			} else {
				// If it didn't match then RootKey will have
				// generated a new key.
				c.Assert(key, qt.HasLen, 24)
				c.Assert(id, qt.HasLen, 32)
			}
		})
	}
}

func TestRootKey(t *testing.T) {
	c := qt.New(t)
	defer c.Done()
	now := epoch
	c.Patch(mgorootkeystore.Clock, clockVal(&now))
	coll := testColl(c)

	store := mgorootkeystore.NewRootKeys(10).NewStore(coll, mgorootkeystore.Policy{
		GenerateInterval: 2 * time.Minute,
		ExpiryDuration:   5 * time.Minute,
	})
	key, id, err := store.RootKey(context.Background())
	c.Assert(err, qt.IsNil)
	c.Assert(key, qt.HasLen, 24)
	c.Assert(id, qt.HasLen, 32)

	// If we get a key within the generate interval, we should
	// get the same one.
	now = epoch.Add(time.Minute)
	key1, id1, err := store.RootKey(context.Background())
	c.Assert(err, qt.IsNil)
	c.Assert(key1, qt.DeepEquals, key)
	c.Assert(id1, qt.DeepEquals, id)

	// A different store instance should get the same root key.
	store1 := mgorootkeystore.NewRootKeys(10).NewStore(coll, mgorootkeystore.Policy{
		GenerateInterval: 2 * time.Minute,
		ExpiryDuration:   5 * time.Minute,
	})
	key1, id1, err = store1.RootKey(context.Background())
	c.Assert(err, qt.IsNil)
	c.Assert(key1, qt.DeepEquals, key)
	c.Assert(id1, qt.DeepEquals, id)

	// After the generation interval has passed, we should generate a new key.
	now = epoch.Add(2*time.Minute + time.Second)
	key1, id1, err = store.RootKey(context.Background())
	c.Assert(err, qt.IsNil)
	c.Assert(key, qt.HasLen, 24)
	c.Assert(id, qt.HasLen, 32)
	c.Assert(key1, qt.Not(qt.DeepEquals), key)
	c.Assert(id1, qt.Not(qt.DeepEquals), id)

	// The other store should pick it up too.
	key2, id2, err := store1.RootKey(context.Background())
	c.Assert(err, qt.IsNil)
	c.Assert(key2, qt.DeepEquals, key1)
	c.Assert(id2, qt.DeepEquals, id1)
}

func TestRootKeyDefaultGenerateInterval(t *testing.T) {
	c := qt.New(t)
	defer c.Done()
	now := epoch
	c.Patch(mgorootkeystore.Clock, clockVal(&now))
	coll := testColl(c)
	store := mgorootkeystore.NewRootKeys(10).NewStore(coll, mgorootkeystore.Policy{
		ExpiryDuration: 5 * time.Minute,
	})
	key, id, err := store.RootKey(context.Background())
	c.Assert(err, qt.IsNil)

	now = epoch.Add(5 * time.Minute)
	key1, id1, err := store.RootKey(context.Background())
	c.Assert(err, qt.IsNil)
	c.Assert(key1, qt.DeepEquals, key)
	c.Assert(id1, qt.DeepEquals, id)

	now = epoch.Add(5*time.Minute + time.Millisecond)
	key1, id1, err = store.RootKey(context.Background())
	c.Assert(err, qt.IsNil)
	c.Assert(string(key1), qt.Not(qt.Equals), string(key))
	c.Assert(string(id1), qt.Not(qt.Equals), string(id))
}

var preferredRootKeyTests = []struct {
	about    string
	now      time.Time
	keys     []dbrootkeystore.RootKey
	policy   mgorootkeystore.Policy
	expectId []byte
}{{
	about: "latest creation time is preferred",
	now:   epoch.Add(5 * time.Minute),
	keys: []dbrootkeystore.RootKey{{
		Created: epoch.Add(4 * time.Minute),
		Expires: epoch.Add(15 * time.Minute),
		Id:      []byte("id0"),
		RootKey: []byte("key0"),
	}, {
		Created: epoch.Add(5*time.Minute + 30*time.Second),
		Expires: epoch.Add(16 * time.Minute),
		Id:      []byte("id1"),
		RootKey: []byte("key1"),
	}, {
		Created: epoch.Add(5 * time.Minute),
		Expires: epoch.Add(16 * time.Minute),
		Id:      []byte("id2"),
		RootKey: []byte("key2"),
	}},
	policy: mgorootkeystore.Policy{
		GenerateInterval: 5 * time.Minute,
		ExpiryDuration:   7 * time.Minute,
	},
	expectId: []byte("id1"),
}, {
	about: "ineligible keys are exluded",
	now:   epoch.Add(5 * time.Minute),
	keys: []dbrootkeystore.RootKey{{
		Created: epoch.Add(4 * time.Minute),
		Expires: epoch.Add(15 * time.Minute),
		Id:      []byte("id0"),
		RootKey: []byte("key0"),
	}, {
		Created: epoch.Add(5 * time.Minute),
		Expires: epoch.Add(16*time.Minute + 30*time.Second),
		Id:      []byte("id1"),
		RootKey: []byte("key1"),
	}, {
		Created: epoch.Add(6 * time.Minute),
		Expires: epoch.Add(time.Hour),
		Id:      []byte("id2"),
		RootKey: []byte("key2"),
	}},
	policy: mgorootkeystore.Policy{
		GenerateInterval: 5 * time.Minute,
		ExpiryDuration:   7 * time.Minute,
	},
	expectId: []byte("id1"),
}}

func TestPreferredRootKeyFromDatabase(t *testing.T) {
	c := qt.New(t)
	defer c.Done()
	var now time.Time
	c.Patch(mgorootkeystore.Clock, clockVal(&now))
	for _, test := range preferredRootKeyTests {
		c.Run(test.about, func(c *qt.C) {
			coll := testColl(c)
			for _, key := range test.keys {
				err := coll.Insert(key)
				c.Assert(err, qt.IsNil)
			}
			store := mgorootkeystore.NewRootKeys(10).NewStore(coll, test.policy)
			now = test.now
			_, id, err := store.RootKey(context.Background())
			c.Assert(err, qt.IsNil)
			c.Assert(id, qt.DeepEquals, test.expectId)
		})
	}
}

func TestPreferredRootKeyFromCache(t *testing.T) {
	c := qt.New(t)
	defer c.Done()
	var now time.Time
	c.Patch(mgorootkeystore.Clock, clockVal(&now))
	for _, test := range preferredRootKeyTests {
		c.Run(test.about, func(c *qt.C) {
			coll := testColl(c)
			for _, key := range test.keys {
				err := coll.Insert(key)
				c.Assert(err, qt.IsNil)
			}
			store := mgorootkeystore.NewRootKeys(10).NewStore(coll, test.policy)
			// Ensure that all the keys are in cache by getting all of them.
			for _, key := range test.keys {
				got, err := store.Get(context.Background(), key.Id)
				c.Assert(err, qt.IsNil)
				c.Assert(got, qt.DeepEquals, key.RootKey)
			}
			// Remove all the keys from the collection so that
			// we know we must be acquiring them from the cache.
			_, err := coll.RemoveAll(nil)
			c.Assert(err, qt.IsNil)

			// Test that RootKey returns the expected key.
			now = test.now
			_, id, err := store.RootKey(context.Background())
			c.Assert(err, qt.IsNil)
			c.Assert(id, qt.DeepEquals, test.expectId)
		})
	}
}

func TestGet(t *testing.T) {
	c := qt.New(t)
	defer c.Done()
	now := epoch
	c.Patch(mgorootkeystore.Clock, clockVal(&now))

	coll := testColl(c)
	store := mgorootkeystore.NewRootKeys(5).NewStore(coll, mgorootkeystore.Policy{
		GenerateInterval: 1 * time.Minute,
		ExpiryDuration:   30 * time.Minute,
	})
	type idKey struct {
		id  string
		key []byte
	}
	var keys []idKey
	keyIds := make(map[string]bool)
	for i := 0; i < 20; i++ {
		key, id, err := store.RootKey(context.Background())
		c.Assert(err, qt.IsNil)
		c.Assert(keyIds[string(id)], qt.Equals, false)
		keys = append(keys, idKey{string(id), key})
		now = now.Add(time.Minute + time.Second)
	}
	for i, k := range keys {
		key, err := store.Get(context.Background(), []byte(k.id))
		c.Assert(err, qt.IsNil, qt.Commentf("key %d (%s)", i, k.id))
		c.Assert(key, qt.DeepEquals, k.key, qt.Commentf("key %d (%s)", i, k.id))
	}
	// Check that the keys are cached.
	//
	// Since the cache size is 5, the most recent 5 items will be in
	// the primary cache; the 5 items before that will be in the old
	// cache and nothing else will be cached.
	//
	// The first time we fetch an item from the old cache, a new
	// primary cache will be allocated, all existing items in the
	// old cache except that item will be evicted, and all items in
	// the current primary cache moved to the old cache.
	//
	// The upshot of that is that all but the first 6 calls to Get
	// should result in a database fetch.

	var fetched []string
	c.Patch(mgorootkeystore.MgoCollectionFindId, func(coll *mgo.Collection, id interface{}) *mgo.Query {
		fetched = append(fetched, string(id.([]byte)))
		return coll.FindId(id)
	})
	c.Logf("testing cache")

	for i := len(keys) - 1; i >= 0; i-- {
		k := keys[i]
		key, err := store.Get(context.Background(), []byte(k.id))
		c.Assert(err, qt.IsNil)
		c.Assert(err, qt.IsNil, qt.Commentf("key %d (%s)", i, k.id))
		c.Assert(key, qt.DeepEquals, k.key, qt.Commentf("key %d (%s)", i, k.id))
	}
	c.Assert(len(fetched), qt.Equals, len(keys)-6)
	for i, id := range fetched {
		c.Assert(id, qt.Equals, keys[len(keys)-6-i-1].id)
	}
}

func TestGetCachesMisses(t *testing.T) {
	c := qt.New(t)
	defer c.Done()
	coll := testColl(c)
	store := mgorootkeystore.NewRootKeys(5).NewStore(coll, mgorootkeystore.Policy{
		GenerateInterval: 1 * time.Minute,
		ExpiryDuration:   30 * time.Minute,
	})
	var fetched []string
	c.Patch(mgorootkeystore.MgoCollectionFindId, func(coll *mgo.Collection, id interface{}) *mgo.Query {
		fetched = append(fetched, fmt.Sprintf("%#v", id))
		return coll.FindId(id)
	})
	key, err := store.Get(context.Background(), []byte("foo"))
	c.Assert(err, qt.Equals, bakery.ErrNotFound)
	c.Assert(key, qt.IsNil)
	// This should check twice first using a []byte second using a string
	c.Assert(fetched, qt.DeepEquals, []string{fmt.Sprintf("%#v", []byte("foo")), fmt.Sprintf("%#v", "foo")})
	fetched = nil

	key, err = store.Get(context.Background(), []byte("foo"))
	c.Assert(err, qt.Equals, bakery.ErrNotFound)
	c.Assert(key, qt.IsNil)
	c.Assert(fetched, qt.IsNil)
}

func TestGetExpiredItemFromCache(t *testing.T) {
	c := qt.New(t)
	defer c.Done()
	now := epoch
	c.Patch(mgorootkeystore.Clock, clockVal(&now))
	coll := testColl(c)
	store := mgorootkeystore.NewRootKeys(10).NewStore(coll, mgorootkeystore.Policy{
		ExpiryDuration: 5 * time.Minute,
	})
	_, id, err := store.RootKey(context.Background())
	c.Assert(err, qt.IsNil)

	c.Patch(mgorootkeystore.MgoCollectionFindId, func(*mgo.Collection, interface{}) *mgo.Query {
		c.Errorf("FindId unexpectedly called")
		return nil
	})

	now = epoch.Add(15 * time.Minute)

	_, err = store.Get(context.Background(), id)
	c.Assert(err, qt.Equals, bakery.ErrNotFound)
}

func TestEnsureIndex(t *testing.T) {
	c := qt.New(t)
	defer c.Done()
	keys := mgorootkeystore.NewRootKeys(5)
	coll := testColl(c)
	err := keys.EnsureIndex(coll)
	c.Assert(err, qt.IsNil)

	// This code can take up to 60s to run; there's no way
	// to force it to run more quickly, but it provides reassurance
	// that the code actually works.
	// Reenable the rest of this test if concerned about index behaviour.

	c.Skip("test runs too slowly")

	_, id1, err := keys.NewStore(coll, mgorootkeystore.Policy{
		ExpiryDuration: 100 * time.Millisecond,
	}).RootKey(context.Background())

	c.Assert(err, qt.IsNil)

	_, id2, err := keys.NewStore(coll, mgorootkeystore.Policy{
		ExpiryDuration: time.Hour,
	}).RootKey(context.Background())

	c.Assert(err, qt.IsNil)
	c.Assert(id2, qt.Not(qt.Equals), id1)

	// Sanity check that the keys are in the collection.
	n, err := coll.Find(nil).Count()
	c.Assert(err, qt.IsNil)
	c.Assert(n, qt.Equals, 2)
	for i := 0; i < 100; i++ {
		n, err := coll.Find(nil).Count()
		c.Assert(err, qt.IsNil)
		switch n {
		case 1:
			return
		case 2:
			time.Sleep(time.Second)
		default:
			c.Fatalf("unexpected key count %v", n)
		}
	}
	c.Fatalf("key was never removed from database")
}

type legacyRootKey struct {
	Id      string `bson:"_id"`
	Created time.Time
	Expires time.Time
	RootKey []byte
}

func TestLegacy(t *testing.T) {
	c := qt.New(t)
	defer c.Done()
	coll := testColl(c)
	err := coll.Insert(&legacyRootKey{
		Id:      "foo",
		RootKey: []byte("a key"),
		Created: time.Now(),
		Expires: time.Now().Add(10 * time.Minute),
	})
	c.Assert(err, qt.IsNil)
	store := mgorootkeystore.NewRootKeys(10).NewStore(coll, mgorootkeystore.Policy{
		ExpiryDuration: 5 * time.Minute,
	})
	rk, err := store.Get(context.Background(), []byte("foo"))
	c.Assert(err, qt.IsNil)
	c.Assert(string(rk), qt.Equals, "a key")
}

func testColl(c *qt.C) *mgo.Collection {
	db, err := mgotest.New()
	c.Assert(err, qt.Equals, nil)
	c.Defer(func() {
		err := db.Close()
		c.Check(err, qt.Equals, nil)
	})
	return db.C("rootkeyitems")
}

func clockVal(t *time.Time) dbrootkeystore.Clock {
	return clockFunc(func() time.Time {
		return *t
	})
}

type clockFunc func() time.Time

func (f clockFunc) Now() time.Time {
	return f()
}
