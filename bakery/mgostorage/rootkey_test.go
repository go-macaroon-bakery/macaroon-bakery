package mgostorage_test

import (
	"time"

	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/mgo.v2"

	"gopkg.in/macaroon-bakery.v1/bakery"
	"gopkg.in/macaroon-bakery.v1/bakery/mgostorage"
)

type RootKeyStorageSuite struct {
	testing.IsolatedMgoSuite
}

var _ = gc.Suite(&RootKeyStorageSuite{})

var epoch = time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)

var isValidWithPolicyTests = []struct {
	about  string
	policy mgostorage.Policy
	now    time.Time
	key    mgostorage.RootKey
	expect bool
}{{
	about: "success",
	policy: mgostorage.Policy{
		GenerateInterval: 2 * time.Minute,
		ExpiryDuration:   3 * time.Minute,
	},
	now: epoch.Add(20 * time.Minute),
	key: mgostorage.RootKey{
		Created: epoch.Add(19 * time.Minute),
		Expires: epoch.Add(24 * time.Minute),
		Id:      "id",
		RootKey: []byte("key"),
	},
	expect: true,
}, {
	about: "empty root key",
	policy: mgostorage.Policy{
		GenerateInterval: 2 * time.Minute,
		ExpiryDuration:   3 * time.Minute,
	},
	now:    epoch.Add(20 * time.Minute),
	key:    mgostorage.RootKey{},
	expect: false,
}, {
	about: "created too early",
	policy: mgostorage.Policy{
		GenerateInterval: 2 * time.Minute,
		ExpiryDuration:   3 * time.Minute,
	},
	now: epoch.Add(20 * time.Minute),
	key: mgostorage.RootKey{
		Created: epoch.Add(18*time.Minute - time.Millisecond),
		Expires: epoch.Add(24 * time.Minute),
		Id:      "id",
		RootKey: []byte("key"),
	},
	expect: false,
}, {
	about: "expires too early",
	policy: mgostorage.Policy{
		GenerateInterval: 2 * time.Minute,
		ExpiryDuration:   3 * time.Minute,
	},
	now: epoch.Add(20 * time.Minute),
	key: mgostorage.RootKey{
		Created: epoch.Add(19 * time.Minute),
		Expires: epoch.Add(21 * time.Minute),
		Id:      "id",
		RootKey: []byte("key"),
	},
	expect: false,
}, {
	about: "expires too late",
	policy: mgostorage.Policy{
		GenerateInterval: 2 * time.Minute,
		ExpiryDuration:   3 * time.Minute,
	},
	now: epoch.Add(20 * time.Minute),
	key: mgostorage.RootKey{
		Created: epoch.Add(19 * time.Minute),
		Expires: epoch.Add(25*time.Minute + time.Millisecond),
		Id:      "id",
		RootKey: []byte("key"),
	},
	expect: false,
}}

func (s *RootKeyStorageSuite) TestIsValidWithPolicy(c *gc.C) {
	var now time.Time
	s.PatchValue(mgostorage.TimeNow, func() time.Time {
		return now
	})
	for i, test := range isValidWithPolicyTests {
		c.Logf("test %d: %v", i, test.about)
		now = test.now
		c.Assert(mgostorage.IsValidWithPolicy(test.key, test.policy), gc.Equals, test.expect)
	}
}

func (s *RootKeyStorageSuite) TestRootKeyUsesKeysValidWithPolicy(c *gc.C) {
	// We re-use the TestIsValidWithPolicy tests so that we
	// know that the mongo logic uses the same behaviour.
	var now time.Time
	s.PatchValue(mgostorage.TimeNow, func() time.Time {
		return now
	})
	for i, test := range isValidWithPolicyTests {
		c.Logf("test %d: %v", i, test.about)
		if test.key.RootKey == nil {
			// We don't store empty root keys in the database.
			c.Logf("skipping test with empty root key")
			continue
		}
		// Prime the collection with the root key document.
		_, err := s.coll().RemoveAll(nil)
		c.Assert(err, gc.IsNil)
		err = s.coll().Insert(test.key)
		c.Assert(err, gc.IsNil)

		store := mgostorage.NewRootKeys(10).NewStorage(s.coll(), test.policy)
		now = test.now
		key, id, err := store.RootKey()
		c.Assert(err, gc.IsNil)
		if test.expect {
			c.Assert(id, gc.Equals, "id")
			c.Assert(string(key), gc.Equals, "key")
		} else {
			// If it didn't match then RootKey will have
			// generated a new key.
			c.Assert(key, gc.HasLen, 24)
			c.Assert(id, gc.Matches, "[0-9a-f]{32}")
		}
	}
}

func (s *RootKeyStorageSuite) TestRootKey(c *gc.C) {
	now := epoch
	s.PatchValue(mgostorage.TimeNow, func() time.Time {
		return now
	})

	store := mgostorage.NewRootKeys(10).NewStorage(s.coll(), mgostorage.Policy{
		GenerateInterval: 2 * time.Minute,
		ExpiryDuration:   5 * time.Minute,
	})
	key, id, err := store.RootKey()
	c.Assert(err, gc.IsNil)
	c.Assert(key, gc.HasLen, 24)
	c.Assert(id, gc.Matches, "[0-9a-f]{32}")

	// If we get a key within the generate interval, we should
	// get the same one.
	now = epoch.Add(time.Minute)
	key1, id1, err := store.RootKey()
	c.Assert(err, gc.IsNil)
	c.Assert(key1, gc.DeepEquals, key)
	c.Assert(id1, gc.Equals, id)

	// A different storage instance should get the same root key.
	store1 := mgostorage.NewRootKeys(10).NewStorage(s.coll(), mgostorage.Policy{
		GenerateInterval: 2 * time.Minute,
		ExpiryDuration:   5 * time.Minute,
	})
	key1, id1, err = store1.RootKey()
	c.Assert(err, gc.IsNil)
	c.Assert(key1, gc.DeepEquals, key)
	c.Assert(id1, gc.Equals, id)

	// After the generation interval has passed, we should generate a new key.
	now = epoch.Add(2*time.Minute + time.Second)
	key1, id1, err = store.RootKey()
	c.Assert(err, gc.IsNil)
	c.Assert(key, gc.HasLen, 24)
	c.Assert(id, gc.Matches, "[0-9a-f]{32}")
	c.Assert(key1, gc.Not(gc.DeepEquals), key)
	c.Assert(id1, gc.Not(gc.Equals), id)

	// The other store should pick it up too.
	key2, id2, err := store1.RootKey()
	c.Assert(err, gc.IsNil)
	c.Assert(key2, gc.DeepEquals, key1)
	c.Assert(id2, gc.Equals, id1)
}

func (s *RootKeyStorageSuite) TestRootKeyDefaultGenerateInterval(c *gc.C) {
	now := epoch
	s.PatchValue(mgostorage.TimeNow, func() time.Time {
		return now
	})
	store := mgostorage.NewRootKeys(10).NewStorage(s.coll(), mgostorage.Policy{
		ExpiryDuration: 5 * time.Minute,
	})
	key, id, err := store.RootKey()
	c.Assert(err, gc.IsNil)

	now = epoch.Add(5 * time.Minute)
	key1, id1, err := store.RootKey()
	c.Assert(err, gc.IsNil)
	c.Assert(key1, jc.DeepEquals, key)
	c.Assert(id1, gc.Equals, id)

	now = epoch.Add(5*time.Minute + time.Millisecond)
	key1, id1, err = store.RootKey()
	c.Assert(err, gc.IsNil)
	c.Assert(key1, gc.Not(gc.DeepEquals), key)
	c.Assert(id1, gc.Not(gc.Equals), id)
}

var preferredRootKeyTests = []struct {
	about    string
	now      time.Time
	keys     []mgostorage.RootKey
	policy   mgostorage.Policy
	expectId string
}{{
	about: "latest creation time is preferred",
	now:   epoch.Add(5 * time.Minute),
	keys: []mgostorage.RootKey{{
		Created: epoch.Add(4 * time.Minute),
		Expires: epoch.Add(15 * time.Minute),
		Id:      "id0",
		RootKey: []byte("key0"),
	}, {
		Created: epoch.Add(5*time.Minute + 30*time.Second),
		Expires: epoch.Add(16 * time.Minute),
		Id:      "id1",
		RootKey: []byte("key1"),
	}, {
		Created: epoch.Add(5 * time.Minute),
		Expires: epoch.Add(16 * time.Minute),
		Id:      "id2",
		RootKey: []byte("key2"),
	}},
	policy: mgostorage.Policy{
		GenerateInterval: 5 * time.Minute,
		ExpiryDuration:   7 * time.Minute,
	},
	expectId: "id1",
}, {
	about: "ineligible keys are exluded",
	now:   epoch.Add(5 * time.Minute),
	keys: []mgostorage.RootKey{{
		Created: epoch.Add(4 * time.Minute),
		Expires: epoch.Add(15 * time.Minute),
		Id:      "id0",
		RootKey: []byte("key0"),
	}, {
		Created: epoch.Add(5 * time.Minute),
		Expires: epoch.Add(16*time.Minute + 30*time.Second),
		Id:      "id1",
		RootKey: []byte("key1"),
	}, {
		Created: epoch.Add(6 * time.Minute),
		Expires: epoch.Add(time.Hour),
		Id:      "id2",
		RootKey: []byte("key2"),
	}},
	policy: mgostorage.Policy{
		GenerateInterval: 5 * time.Minute,
		ExpiryDuration:   7 * time.Minute,
	},
	expectId: "id1",
}}

func (s *RootKeyStorageSuite) TestPreferredRootKeyFromDatabase(c *gc.C) {
	var now time.Time
	s.PatchValue(mgostorage.TimeNow, func() time.Time {
		return now
	})
	for i, test := range preferredRootKeyTests {
		c.Logf("%d: %v", i, test.about)
		_, err := s.coll().RemoveAll(nil)
		c.Assert(err, gc.IsNil)
		for _, key := range test.keys {
			err := s.coll().Insert(key)
			c.Assert(err, gc.IsNil)
		}
		store := mgostorage.NewRootKeys(10).NewStorage(s.coll(), test.policy)
		now = test.now
		_, id, err := store.RootKey()
		c.Assert(err, gc.IsNil)
		c.Assert(id, gc.Equals, test.expectId)
	}
}

func (s *RootKeyStorageSuite) TestPreferredRootKeyFromCache(c *gc.C) {
	var now time.Time
	s.PatchValue(mgostorage.TimeNow, func() time.Time {
		return now
	})
	for i, test := range preferredRootKeyTests {
		c.Logf("%d: %v", i, test.about)
		for _, key := range test.keys {
			err := s.coll().Insert(key)
			c.Assert(err, gc.IsNil)
		}
		store := mgostorage.NewRootKeys(10).NewStorage(s.coll(), test.policy)
		// Ensure that all the keys are in cache by getting all of them.
		for _, key := range test.keys {
			got, err := store.Get(key.Id)
			c.Assert(err, gc.IsNil)
			c.Assert(got, jc.DeepEquals, key.RootKey)
		}
		// Remove all the keys from the collection so that
		// we know we must be acquiring them from the cache.
		_, err := s.coll().RemoveAll(nil)
		c.Assert(err, gc.IsNil)

		// Test that RootKey returns the expected key.
		now = test.now
		_, id, err := store.RootKey()
		c.Assert(err, gc.IsNil)
		c.Assert(id, gc.Equals, test.expectId)
	}
}

func (s *RootKeyStorageSuite) TestGet(c *gc.C) {
	now := epoch
	s.PatchValue(mgostorage.TimeNow, func() time.Time {
		return now
	})

	store := mgostorage.NewRootKeys(5).NewStorage(s.coll(), mgostorage.Policy{
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
		key, id, err := store.RootKey()
		c.Assert(err, gc.IsNil)
		c.Assert(keyIds[id], gc.Equals, false)
		keys = append(keys, idKey{id, key})
		now = now.Add(time.Minute + time.Second)
	}
	for i, k := range keys {
		key, err := store.Get(k.id)
		c.Assert(err, gc.IsNil, gc.Commentf("key %d (%s)", i, k.id))
		c.Assert(key, gc.DeepEquals, k.key, gc.Commentf("key %d (%s)", i, k.id))
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
	s.PatchValue(mgostorage.MgoCollectionFindId, func(coll *mgo.Collection, id interface{}) *mgo.Query {
		fetched = append(fetched, id.(string))
		return coll.FindId(id)
	})
	c.Logf("testing cache")

	for i := len(keys) - 1; i >= 0; i-- {
		k := keys[i]
		key, err := store.Get(k.id)
		c.Assert(err, gc.IsNil)
		c.Assert(err, gc.IsNil, gc.Commentf("key %d (%s)", i, k.id))
		c.Assert(key, gc.DeepEquals, k.key, gc.Commentf("key %d (%s)", i, k.id))
	}
	c.Assert(len(fetched), gc.Equals, len(keys)-6)
	for i, id := range fetched {
		c.Assert(id, gc.Equals, keys[len(keys)-6-i-1].id)
	}
}

func (s *RootKeyStorageSuite) TestGetCachesMisses(c *gc.C) {
	store := mgostorage.NewRootKeys(5).NewStorage(s.coll(), mgostorage.Policy{
		GenerateInterval: 1 * time.Minute,
		ExpiryDuration:   30 * time.Minute,
	})
	var fetched []string
	s.PatchValue(mgostorage.MgoCollectionFindId, func(coll *mgo.Collection, id interface{}) *mgo.Query {
		fetched = append(fetched, id.(string))
		return coll.FindId(id)
	})
	key, err := store.Get("foo")
	c.Assert(err, gc.Equals, bakery.ErrNotFound)
	c.Assert(key, gc.IsNil)
	c.Assert(fetched, jc.DeepEquals, []string{"foo"})
	fetched = nil

	key, err = store.Get("foo")
	c.Assert(err, gc.Equals, bakery.ErrNotFound)
	c.Assert(key, gc.IsNil)
	c.Assert(fetched, gc.IsNil)
}

func (s *RootKeyStorageSuite) TestGetExpiredItemFromCache(c *gc.C) {
	now := epoch
	s.PatchValue(mgostorage.TimeNow, func() time.Time {
		return now
	})
	store := mgostorage.NewRootKeys(10).NewStorage(s.coll(), mgostorage.Policy{
		ExpiryDuration: 5 * time.Minute,
	})
	_, id, err := store.RootKey()
	c.Assert(err, gc.IsNil)

	s.PatchValue(mgostorage.MgoCollectionFindId, func(*mgo.Collection, interface{}) *mgo.Query {
		c.Errorf("FindId unexpectedly called")
		return nil
	})

	now = epoch.Add(15 * time.Minute)

	_, err = store.Get(id)
	c.Assert(err, gc.Equals, bakery.ErrNotFound)
}

func (s *RootKeyStorageSuite) TestEnsureIndex(c *gc.C) {
	keys := mgostorage.NewRootKeys(5)
	err := keys.EnsureIndex(s.coll())
	c.Assert(err, gc.IsNil)

	// This code can take up to 60s to run; there's no way
	// to force it to run more quickly, but it provides reassurance
	// that the code actually works.
	// Reenable the rest of this test if concerned about index behaviour.

	c.SucceedNow()

	_, id1, err := keys.NewStorage(s.coll(), mgostorage.Policy{
		ExpiryDuration: 100 * time.Millisecond,
	}).RootKey()
	c.Assert(err, gc.IsNil)

	_, id2, err := keys.NewStorage(s.coll(), mgostorage.Policy{
		ExpiryDuration: time.Hour,
	}).RootKey()
	c.Assert(err, gc.IsNil)
	c.Assert(id2, gc.Not(gc.Equals), id1)

	// Sanity check that the keys are in the collection.
	n, err := s.coll().Find(nil).Count()
	c.Assert(err, gc.IsNil)
	c.Assert(n, gc.Equals, 2)
	for i := 0; i < 100; i++ {
		n, err := s.coll().Find(nil).Count()
		c.Assert(err, gc.IsNil)
		switch n {
		case 1:
			c.SucceedNow()
		case 2:
			time.Sleep(time.Second)
		default:
			c.Fatalf("unexpected key count %v", n)
		}
	}
	c.Fatalf("key was never removed from database")
}

func (s *RootKeyStorageSuite) coll() *mgo.Collection {
	return s.Session.DB("test").C("items")
}
