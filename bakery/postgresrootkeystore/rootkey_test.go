package postgresrootkeystore_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	qt "github.com/frankban/quicktest"
	"github.com/frankban/quicktest/qtsuite"
	"github.com/juju/postgrestest"
	"gopkg.in/errgo.v1"

	"github.com/go-macaroon-bakery/macaroon-bakery/v3/bakery"
	"github.com/go-macaroon-bakery/macaroon-bakery/v3/bakery/dbrootkeystore"
	"github.com/go-macaroon-bakery/macaroon-bakery/v3/bakery/postgresrootkeystore"
)

const testTable = "testrootkeys"

type RootKeyStoreSuite struct {
	db_   *postgrestest.DB
	db    *sql.DB
	store *postgresrootkeystore.RootKeys
}

func TestSuite(t *testing.T) {
	qtsuite.Run(qt.New(t), &RootKeyStoreSuite{})
}

func (s *RootKeyStoreSuite) Init(c *qt.C) {
	db, err := postgrestest.New()
	if err == postgrestest.ErrDisabled {
		c.Skip("postgres testing is disabled")
	}
	c.Assert(err, qt.Equals, nil)
	store := postgresrootkeystore.NewRootKeys(db.DB, testTable, 1)
	c.Defer(func() {
		err := s.store.Close()
		c.Check(err, qt.Equals, nil)
		err = db.Close()
		c.Check(err, qt.Equals, nil)
	})
	s.db = db.DB
	s.store = store
}

var epoch = time.Date(2200, time.January, 1, 0, 0, 0, 0, time.UTC)

var IsValidWithPolicyTests = []struct {
	about  string
	policy postgresrootkeystore.Policy
	now    time.Time
	key    dbrootkeystore.RootKey
	expect bool
}{{
	about: "success",
	policy: postgresrootkeystore.Policy{
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
	policy: postgresrootkeystore.Policy{
		GenerateInterval: 2 * time.Minute,
		ExpiryDuration:   3 * time.Minute,
	},
	now:    epoch.Add(20 * time.Minute),
	key:    dbrootkeystore.RootKey{},
	expect: false,
}, {
	about: "created too early",
	policy: postgresrootkeystore.Policy{
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
	policy: postgresrootkeystore.Policy{
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
	policy: postgresrootkeystore.Policy{
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

func (s *RootKeyStoreSuite) TestIsValidWithPolicy(c *qt.C) {
	for i, test := range IsValidWithPolicyTests {
		c.Logf("test %d: %v", i, test.about)
		c.Assert(test.key.IsValidWithPolicy(dbrootkeystore.Policy(test.policy), test.now), qt.Equals, test.expect)
	}
}

func (s *RootKeyStoreSuite) TestRootKeyUsesKeysValidWithPolicy(c *qt.C) {
	// We re-use the TestIsValidWithPolicy tests so that we
	// know that the mongo logic uses the same behaviour.
	var now time.Time
	c.Patch(postgresrootkeystore.Clock, clockVal(&now))
	for i, test := range IsValidWithPolicyTests {
		c.Logf("test %d: %v", i, test.about)
		if test.key.RootKey == nil {
			// We don't store empty root keys in the database.
			c.Logf("skipping test with empty root key")
			continue
		}
		// Prime the table with the root key document.
		s.primeRootKeys(c, []dbrootkeystore.RootKey{test.key})
		store := postgresrootkeystore.NewRootKeys(s.db, testTable, 10).NewStore(test.policy)
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
	}
}

func (s *RootKeyStoreSuite) TestRootKey(c *qt.C) {
	now := epoch
	c.Patch(postgresrootkeystore.Clock, clockVal(&now))

	store := postgresrootkeystore.NewRootKeys(s.db, testTable, 10).NewStore(postgresrootkeystore.Policy{
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
	store1 := postgresrootkeystore.NewRootKeys(s.db, testTable, 10).NewStore(postgresrootkeystore.Policy{
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

func (s *RootKeyStoreSuite) TestRootKeyDefaultGenerateInterval(c *qt.C) {
	now := epoch
	c.Patch(postgresrootkeystore.Clock, clockVal(&now))
	store := postgresrootkeystore.NewRootKeys(s.db, testTable, 10).NewStore(postgresrootkeystore.Policy{
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
	policy   postgresrootkeystore.Policy
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
	policy: postgresrootkeystore.Policy{
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
	policy: postgresrootkeystore.Policy{
		GenerateInterval: 5 * time.Minute,
		ExpiryDuration:   7 * time.Minute,
	},
	expectId: []byte("id1"),
}}

func (s *RootKeyStoreSuite) TestPreferredRootKeyFromDatabase(c *qt.C) {
	var now time.Time
	c.Patch(postgresrootkeystore.Clock, clockVal(&now))
	for i, test := range preferredRootKeyTests {
		c.Logf("%d: %v", i, test.about)
		s.primeRootKeys(c, test.keys)
		store := postgresrootkeystore.NewRootKeys(s.db, testTable, 10).NewStore(test.policy)
		now = test.now
		_, id, err := store.RootKey(context.Background())
		c.Assert(err, qt.IsNil)
		c.Assert(id, qt.DeepEquals, test.expectId)
	}
}

func (s *RootKeyStoreSuite) TestPreferredRootKeyFromCache(c *qt.C) {
	var now time.Time
	c.Patch(postgresrootkeystore.Clock, clockVal(&now))
	for i, test := range preferredRootKeyTests {
		c.Logf("%d: %v", i, test.about)
		s.primeRootKeys(c, test.keys)
		store := postgresrootkeystore.NewRootKeys(s.db, testTable, 10).NewStore(test.policy)
		// Ensure that all the keys are in cache by getting all of them.
		for _, key := range test.keys {
			got, err := store.Get(context.Background(), key.Id)
			c.Assert(err, qt.IsNil)
			c.Assert(got, qt.DeepEquals, key.RootKey)
		}
		// Remove all the keys from the collection so that
		// we know we must be acquiring them from the cache.
		s.primeRootKeys(c, nil)

		c.Logf("all keys removed")

		// Test that RootKey returns the expected key.
		now = test.now
		k, id, err := store.RootKey(context.Background())
		c.Logf("rootKey %#v; id %#v; err %v", k, id, err)
		c.Assert(err, qt.IsNil)
		c.Assert(id, qt.DeepEquals, test.expectId)
	}
}

func (s *RootKeyStoreSuite) TestGet(c *qt.C) {
	now := epoch
	c.Patch(postgresrootkeystore.Clock, clockVal(&now))
	var fetched []string
	c.Patch(postgresrootkeystore.NewBacking, func(keys *postgresrootkeystore.RootKeys) dbrootkeystore.Backing {
		b := postgresrootkeystore.Backing(keys)
		return &funcBacking{
			Backing: b,
			getKey: func(id []byte) (dbrootkeystore.RootKey, error) {
				fetched = append(fetched, string(id))
				return b.GetKey(id)
			},
		}
	})
	store := postgresrootkeystore.NewRootKeys(s.db, testTable, 5).NewStore(postgresrootkeystore.Policy{
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

	c.Logf("testing cache")
	fetched = nil
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

func (s *RootKeyStoreSuite) TestGetCachesMisses(c *qt.C) {
	var fetched []string
	c.Patch(postgresrootkeystore.NewBacking, func(keys *postgresrootkeystore.RootKeys) dbrootkeystore.Backing {
		b := postgresrootkeystore.Backing(keys)
		return &funcBacking{
			Backing: b,
			getKey: func(id []byte) (dbrootkeystore.RootKey, error) {
				fetched = append(fetched, string(id))
				return b.GetKey(id)
			},
		}
	})
	store := postgresrootkeystore.NewRootKeys(s.db, testTable, 5).NewStore(postgresrootkeystore.Policy{
		GenerateInterval: 1 * time.Minute,
		ExpiryDuration:   30 * time.Minute,
	})
	key, err := store.Get(context.Background(), []byte("foo"))
	c.Assert(errgo.Cause(err), qt.Equals, bakery.ErrNotFound)
	c.Assert(key, qt.IsNil)
	c.Assert(fetched, qt.DeepEquals, []string{"foo"})
	fetched = nil

	key, err = store.Get(context.Background(), []byte("foo"))
	c.Assert(err, qt.Equals, bakery.ErrNotFound)
	c.Assert(key, qt.IsNil)
	c.Assert(fetched, qt.IsNil)
}

func (s *RootKeyStoreSuite) TestGetExpiredItemFromCache(c *qt.C) {
	now := epoch
	c.Patch(postgresrootkeystore.Clock, clockVal(&now))
	store := postgresrootkeystore.NewRootKeys(s.db, testTable, 10).NewStore(postgresrootkeystore.Policy{
		ExpiryDuration: 5 * time.Minute,
	})
	_, id, err := store.RootKey(context.Background())
	c.Assert(err, qt.IsNil)

	c.Patch(postgresrootkeystore.NewBacking, func(keys *postgresrootkeystore.RootKeys) dbrootkeystore.Backing {
		return &funcBacking{
			Backing: postgresrootkeystore.Backing(keys),
			getKey: func(id []byte) (dbrootkeystore.RootKey, error) {
				c.Errorf("FindId unexpectedly called")
				return dbrootkeystore.RootKey{}, nil
			},
		}
	})

	now = epoch.Add(15 * time.Minute)

	_, err = store.Get(context.Background(), id)
	c.Assert(err, qt.Equals, bakery.ErrNotFound)
}

const sqlTimeFormat = "2006-01-02 15:04:05.9999-07"

func (s *RootKeyStoreSuite) TestKeyExpiration(c *qt.C) {
	keys := postgresrootkeystore.NewRootKeys(s.db, testTable, 5)

	_, id1, err := keys.NewStore(postgresrootkeystore.Policy{
		ExpiryDuration:   100 * time.Millisecond,
		GenerateInterval: time.Nanosecond,
	}).RootKey(context.Background())
	c.Assert(err, qt.IsNil)

	_, id2, err := keys.NewStore(postgresrootkeystore.Policy{
		ExpiryDuration: time.Hour,
	}).RootKey(context.Background())
	c.Assert(err, qt.IsNil)
	c.Assert(string(id2), qt.Not(qt.Equals), string(id1))

	// Sanity check that the keys are in the collection.
	var n int
	err = s.db.QueryRow(`SELECT count(id) FROM ` + testTable).Scan(&n)
	c.Assert(err, qt.Equals, nil)
	c.Assert(n, qt.Equals, 2)

	// Sleep past the expiry time of the first key.
	time.Sleep(150 * time.Millisecond)

	// Use a store with a short generate interval to force
	// another key to be generated, which should trigger
	// the expiration check (the trigger is on INSERT).
	_, _, err = keys.NewStore(postgresrootkeystore.Policy{
		GenerateInterval: time.Nanosecond,
		ExpiryDuration:   time.Hour,
	}).RootKey(context.Background())
	c.Assert(err, qt.Equals, nil)

	_, err = postgresrootkeystore.Backing(s.store).GetKey(id1)
	c.Assert(errgo.Cause(err), qt.Equals, bakery.ErrNotFound)
}

// primeRootKeys deletes all rows from the root key table
// and inserts the given keys.
func (s *RootKeyStoreSuite) primeRootKeys(c *qt.C, keys []dbrootkeystore.RootKey) {
	// Ignore any error from the delete - it's probably happening
	// because the table does not exist yet.
	s.db.Exec(`DELETE FROM ` + testTable)
	for _, key := range keys {
		err := postgresrootkeystore.Backing(s.store).InsertKey(key)
		c.Assert(err, qt.IsNil)
	}
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

type funcBacking struct {
	dbrootkeystore.Backing
	getKey func(id []byte) (dbrootkeystore.RootKey, error)
}

func (b *funcBacking) GetKey(id []byte) (dbrootkeystore.RootKey, error) {
	if b.getKey == nil {
		return b.Backing.GetKey(id)
	}
	return b.getKey(id)
}
