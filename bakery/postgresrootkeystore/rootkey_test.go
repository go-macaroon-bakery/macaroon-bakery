package postgresrootkeystore

import (
	"database/sql"
	"time"

	"github.com/juju/postgrestest"
	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	"golang.org/x/net/context"
	gc "gopkg.in/check.v1"
	errgo "gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2/bakery"
)

var _ = gc.Suite(&RootKeyStoreSuite{})

const testTable = "testrootkeys"

type RootKeyStoreSuite struct {
	testing.CleanupSuite
	testing.LoggingSuite
	db_   *postgrestest.DB
	db    *sql.DB
	store *RootKeys
}

func (s *RootKeyStoreSuite) SetUpSuite(c *gc.C) {
	s.LoggingSuite.SetUpSuite(c)
	s.CleanupSuite.SetUpSuite(c)
}

func (s *RootKeyStoreSuite) TearDownSuite(c *gc.C) {
	s.CleanupSuite.TearDownSuite(c)
	s.LoggingSuite.TearDownSuite(c)
}

func (s *RootKeyStoreSuite) SetUpTest(c *gc.C) {
	s.LoggingSuite.SetUpTest(c)
	s.CleanupSuite.SetUpTest(c)
	db, err := postgrestest.New()
	if err == postgrestest.ErrDisabled {
		c.Skip("postgres testing is disabled")
	}
	c.Assert(err, gc.Equals, nil)
	s.db_ = db
	s.db = db.DB
	s.store = NewRootKeys(s.db, testTable, 1)
}

func (s *RootKeyStoreSuite) TearDownTest(c *gc.C) {
	err := s.store.Close()
	c.Assert(err, gc.Equals, nil)
	err = s.db_.Close()
	c.Assert(err, gc.Equals, nil)
	s.CleanupSuite.TearDownTest(c)
	s.CleanupSuite.TearDownTest(c)
}

var epoch = time.Date(2200, time.January, 1, 0, 0, 0, 0, time.UTC)

var isValidWithPolicyTests = []struct {
	about  string
	policy Policy
	now    time.Time
	key    rootKey
	expect bool
}{{
	about: "success",
	policy: Policy{
		GenerateInterval: 2 * time.Minute,
		ExpiryDuration:   3 * time.Minute,
	},
	now: epoch.Add(20 * time.Minute),
	key: rootKey{
		created: epoch.Add(19 * time.Minute),
		expires: epoch.Add(24 * time.Minute),
		id:      []byte("id"),
		rootKey: []byte("key"),
	},
	expect: true,
}, {
	about: "empty root key",
	policy: Policy{
		GenerateInterval: 2 * time.Minute,
		ExpiryDuration:   3 * time.Minute,
	},
	now:    epoch.Add(20 * time.Minute),
	key:    rootKey{},
	expect: false,
}, {
	about: "created too early",
	policy: Policy{
		GenerateInterval: 2 * time.Minute,
		ExpiryDuration:   3 * time.Minute,
	},
	now: epoch.Add(20 * time.Minute),
	key: rootKey{
		created: epoch.Add(18*time.Minute - time.Millisecond),
		expires: epoch.Add(24 * time.Minute),
		id:      []byte("id"),
		rootKey: []byte("key"),
	},
	expect: false,
}, {
	about: "expires too early",
	policy: Policy{
		GenerateInterval: 2 * time.Minute,
		ExpiryDuration:   3 * time.Minute,
	},
	now: epoch.Add(20 * time.Minute),
	key: rootKey{
		created: epoch.Add(19 * time.Minute),
		expires: epoch.Add(21 * time.Minute),
		id:      []byte("id"),
		rootKey: []byte("key"),
	},
	expect: false,
}, {
	about: "expires too late",
	policy: Policy{
		GenerateInterval: 2 * time.Minute,
		ExpiryDuration:   3 * time.Minute,
	},
	now: epoch.Add(20 * time.Minute),
	key: rootKey{
		created: epoch.Add(19 * time.Minute),
		expires: epoch.Add(25*time.Minute + time.Millisecond),
		id:      []byte("id"),
		rootKey: []byte("key"),
	},
	expect: false,
}}

func (s *RootKeyStoreSuite) TestIsValidWithPolicy(c *gc.C) {
	var now time.Time
	s.PatchValue(&timeNow, func() time.Time {
		return now
	})
	for i, test := range isValidWithPolicyTests {
		c.Logf("test %d: %v", i, test.about)
		now = test.now
		c.Assert(test.key.isValidWithPolicy(test.policy), gc.Equals, test.expect)
	}
}

func (s *RootKeyStoreSuite) TestRootKeyUsesKeysValidWithPolicy(c *gc.C) {
	// We re-use the TestIsValidWithPolicy tests so that we
	// know that the mongo logic uses the same behaviour.
	var now time.Time
	s.PatchValue(&timeNow, func() time.Time {
		return now
	})
	for i, test := range isValidWithPolicyTests {
		c.Logf("test %d: %v", i, test.about)
		if test.key.rootKey == nil {
			// We don't store empty root keys in the database.
			c.Logf("skipping test with empty root key")
			continue
		}
		// Prime the table with the root key document.
		s.primeRootKeys(c, []rootKey{test.key})
		store := NewRootKeys(s.db, testTable, 10).NewStore(test.policy)
		now = test.now
		key, id, err := store.RootKey(context.Background())
		c.Assert(err, gc.IsNil)
		if test.expect {
			c.Assert(string(id), gc.Equals, "id")
			c.Assert(string(key), gc.Equals, "key")
		} else {
			// If it didn't match then RootKey will have
			// generated a new key.
			c.Assert(key, gc.HasLen, 24)
			c.Assert(id, gc.HasLen, 32)
		}
	}
}

func (s *RootKeyStoreSuite) TestRootKey(c *gc.C) {
	now := epoch
	s.PatchValue(&timeNow, func() time.Time {
		return now
	})

	store := NewRootKeys(s.db, testTable, 10).NewStore(Policy{
		GenerateInterval: 2 * time.Minute,
		ExpiryDuration:   5 * time.Minute,
	})
	key, id, err := store.RootKey(context.Background())
	c.Assert(err, gc.IsNil)
	c.Assert(key, gc.HasLen, 24)
	c.Assert(id, gc.HasLen, 32)

	// If we get a key within the generate interval, we should
	// get the same one.
	now = epoch.Add(time.Minute)
	key1, id1, err := store.RootKey(context.Background())
	c.Assert(err, gc.IsNil)
	c.Assert(key1, gc.DeepEquals, key)
	c.Assert(id1, gc.DeepEquals, id)

	// A different store instance should get the same root key.
	store1 := NewRootKeys(s.db, testTable, 10).NewStore(Policy{
		GenerateInterval: 2 * time.Minute,
		ExpiryDuration:   5 * time.Minute,
	})
	key1, id1, err = store1.RootKey(context.Background())
	c.Assert(err, gc.IsNil)
	c.Assert(key1, gc.DeepEquals, key)
	c.Assert(id1, gc.DeepEquals, id)

	// After the generation interval has passed, we should generate a new key.
	now = epoch.Add(2*time.Minute + time.Second)
	key1, id1, err = store.RootKey(context.Background())
	c.Assert(err, gc.IsNil)
	c.Assert(key, gc.HasLen, 24)
	c.Assert(id, gc.HasLen, 32)
	c.Assert(key1, gc.Not(gc.DeepEquals), key)
	c.Assert(id1, gc.Not(gc.DeepEquals), id)

	// The other store should pick it up too.
	key2, id2, err := store1.RootKey(context.Background())
	c.Assert(err, gc.IsNil)
	c.Assert(key2, gc.DeepEquals, key1)
	c.Assert(id2, gc.DeepEquals, id1)
}

func (s *RootKeyStoreSuite) TestRootKeyDefaultGenerateInterval(c *gc.C) {
	now := epoch
	s.PatchValue(&timeNow, func() time.Time {
		return now
	})
	store := NewRootKeys(s.db, testTable, 10).NewStore(Policy{
		ExpiryDuration: 5 * time.Minute,
	})
	key, id, err := store.RootKey(context.Background())
	c.Assert(err, gc.IsNil)

	now = epoch.Add(5 * time.Minute)
	key1, id1, err := store.RootKey(context.Background())
	c.Assert(err, gc.IsNil)
	c.Assert(key1, jc.DeepEquals, key)
	c.Assert(id1, jc.DeepEquals, id)

	now = epoch.Add(5*time.Minute + time.Millisecond)
	key1, id1, err = store.RootKey(context.Background())
	c.Assert(err, gc.IsNil)
	c.Assert(string(key1), gc.Not(gc.Equals), string(key))
	c.Assert(string(id1), gc.Not(gc.Equals), string(id))
}

var preferredRootKeyTests = []struct {
	about    string
	now      time.Time
	keys     []rootKey
	policy   Policy
	expectId []byte
}{{
	about: "latest creation time is preferred",
	now:   epoch.Add(5 * time.Minute),
	keys: []rootKey{{
		created: epoch.Add(4 * time.Minute),
		expires: epoch.Add(15 * time.Minute),
		id:      []byte("id0"),
		rootKey: []byte("key0"),
	}, {
		created: epoch.Add(5*time.Minute + 30*time.Second),
		expires: epoch.Add(16 * time.Minute),
		id:      []byte("id1"),
		rootKey: []byte("key1"),
	}, {
		created: epoch.Add(5 * time.Minute),
		expires: epoch.Add(16 * time.Minute),
		id:      []byte("id2"),
		rootKey: []byte("key2"),
	}},
	policy: Policy{
		GenerateInterval: 5 * time.Minute,
		ExpiryDuration:   7 * time.Minute,
	},
	expectId: []byte("id1"),
}, {
	about: "ineligible keys are exluded",
	now:   epoch.Add(5 * time.Minute),
	keys: []rootKey{{
		created: epoch.Add(4 * time.Minute),
		expires: epoch.Add(15 * time.Minute),
		id:      []byte("id0"),
		rootKey: []byte("key0"),
	}, {
		created: epoch.Add(5 * time.Minute),
		expires: epoch.Add(16*time.Minute + 30*time.Second),
		id:      []byte("id1"),
		rootKey: []byte("key1"),
	}, {
		created: epoch.Add(6 * time.Minute),
		expires: epoch.Add(time.Hour),
		id:      []byte("id2"),
		rootKey: []byte("key2"),
	}},
	policy: Policy{
		GenerateInterval: 5 * time.Minute,
		ExpiryDuration:   7 * time.Minute,
	},
	expectId: []byte("id1"),
}}

func (s *RootKeyStoreSuite) TestPreferredRootKeyFromDatabase(c *gc.C) {
	var now time.Time
	s.PatchValue(&timeNow, func() time.Time {
		return now
	})
	for i, test := range preferredRootKeyTests {
		c.Logf("%d: %v", i, test.about)
		s.primeRootKeys(c, test.keys)
		store := NewRootKeys(s.db, testTable, 10).NewStore(test.policy)
		now = test.now
		_, id, err := store.RootKey(context.Background())
		c.Assert(err, gc.IsNil)
		c.Assert(id, gc.DeepEquals, test.expectId)
	}
}

func (s *RootKeyStoreSuite) TestPreferredRootKeyFromCache(c *gc.C) {
	var now time.Time
	s.PatchValue(&timeNow, func() time.Time {
		return now
	})
	for i, test := range preferredRootKeyTests {
		c.Logf("%d: %v", i, test.about)
		s.primeRootKeys(c, test.keys)
		store := NewRootKeys(s.db, testTable, 10).NewStore(test.policy)
		// Ensure that all the keys are in cache by getting all of them.
		for _, key := range test.keys {
			got, err := store.Get(context.Background(), key.id)
			c.Assert(err, gc.IsNil)
			c.Assert(got, jc.DeepEquals, key.rootKey)
		}
		// Remove all the keys from the collection so that
		// we know we must be acquiring them from the cache.
		s.primeRootKeys(c, nil)

		c.Logf("all keys removed")

		// Test that RootKey returns the expected key.
		now = test.now
		k, id, err := store.RootKey(context.Background())
		c.Logf("rootKey %#v; id %#v; err %v", k, id, err)
		c.Assert(err, gc.IsNil)
		c.Assert(id, jc.DeepEquals, test.expectId)
	}
}

func (s *RootKeyStoreSuite) TestGet(c *gc.C) {
	now := epoch
	s.PatchValue(&timeNow, func() time.Time {
		return now
	})
	store := NewRootKeys(s.db, testTable, 5).NewStore(Policy{
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
		c.Assert(err, gc.IsNil)
		c.Assert(keyIds[string(id)], gc.Equals, false)
		keys = append(keys, idKey{string(id), key})
		now = now.Add(time.Minute + time.Second)
	}
	for i, k := range keys {
		key, err := store.Get(context.Background(), []byte(k.id))
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
	s.PatchValue(&rootKeysFindId, func(s *RootKeys, id []byte) (rootKey, error) {
		fetched = append(fetched, string(id))
		return s.findId(id)
	})
	c.Logf("testing cache")

	for i := len(keys) - 1; i >= 0; i-- {
		k := keys[i]
		key, err := store.Get(context.Background(), []byte(k.id))
		c.Assert(err, gc.IsNil)
		c.Assert(err, gc.IsNil, gc.Commentf("key %d (%s)", i, k.id))
		c.Assert(key, gc.DeepEquals, k.key, gc.Commentf("key %d (%s)", i, k.id))
	}
	c.Assert(len(fetched), gc.Equals, len(keys)-6)
	for i, id := range fetched {
		c.Assert(id, gc.Equals, keys[len(keys)-6-i-1].id)
	}
}

func (s *RootKeyStoreSuite) TestGetCachesMisses(c *gc.C) {
	store := NewRootKeys(s.db, testTable, 5).NewStore(Policy{
		GenerateInterval: 1 * time.Minute,
		ExpiryDuration:   30 * time.Minute,
	})
	var fetched []string
	s.PatchValue(&rootKeysFindId, func(s *RootKeys, id []byte) (rootKey, error) {
		fetched = append(fetched, string(id))
		return s.findId(id)
	})
	key, err := store.Get(context.Background(), []byte("foo"))
	c.Assert(errgo.Cause(err), gc.Equals, bakery.ErrNotFound)
	c.Assert(key, gc.IsNil)
	c.Assert(fetched, jc.DeepEquals, []string{"foo"})
	fetched = nil

	key, err = store.Get(context.Background(), []byte("foo"))
	c.Assert(err, gc.Equals, bakery.ErrNotFound)
	c.Assert(key, gc.IsNil)
	c.Assert(fetched, gc.IsNil)
}

func (s *RootKeyStoreSuite) TestGetExpiredItemFromCache(c *gc.C) {
	now := epoch
	s.PatchValue(&timeNow, func() time.Time {
		return now
	})
	store := NewRootKeys(s.db, testTable, 10).NewStore(Policy{
		ExpiryDuration: 5 * time.Minute,
	})
	_, id, err := store.RootKey(context.Background())
	c.Assert(err, gc.IsNil)

	s.PatchValue(&rootKeysFindId, func(s *RootKeys, id []byte) (rootKey, error) {
		c.Errorf("FindId unexpectedly called")
		return rootKey{}, nil
	})

	now = epoch.Add(15 * time.Minute)

	_, err = store.Get(context.Background(), id)
	c.Assert(err, gc.Equals, bakery.ErrNotFound)
}

const sqlTimeFormat = "2006-01-02 15:04:05.9999-07"

func (s *RootKeyStoreSuite) TestKeyExpiration(c *gc.C) {
	keys := NewRootKeys(s.db, testTable, 5)

	_, id1, err := keys.NewStore(Policy{
		ExpiryDuration:   100 * time.Millisecond,
		GenerateInterval: time.Nanosecond,
	}).RootKey(context.Background())
	c.Assert(err, gc.IsNil)

	_, id2, err := keys.NewStore(Policy{
		ExpiryDuration: time.Hour,
	}).RootKey(context.Background())
	c.Assert(err, gc.IsNil)
	c.Assert(string(id2), gc.Not(gc.Equals), string(id1))

	// Sanity check that the keys are in the collection.
	var n int
	err = s.db.QueryRow(`SELECT count(id) FROM ` + testTable).Scan(&n)
	c.Assert(err, gc.Equals, nil)
	c.Assert(n, gc.Equals, 2)

	// Sleep past the expiry time of the first key.
	time.Sleep(150 * time.Millisecond)

	// Use a store with a short generate interval to force
	// another key to be generated, which should trigger
	// the expiration check (the trigger is on INSERT).
	_, _, err = keys.NewStore(Policy{
		GenerateInterval: time.Nanosecond,
		ExpiryDuration:   time.Hour,
	}).RootKey(context.Background())
	c.Assert(err, gc.Equals, nil)

	_, err = s.store.findId(id1)
	c.Assert(errgo.Cause(err), gc.Equals, bakery.ErrNotFound)
}

// primeRootKeys deletes all rows from the root key table
// and inserts the given keys.
func (s *RootKeyStoreSuite) primeRootKeys(c *gc.C, keys []rootKey) {
	// Ignore any error from the delete - it's probably happening
	// because the table does not exist yet.
	s.db.Exec(`DELETE FROM ` + testTable)
	for _, key := range keys {
		err := s.store.insertKey(key)
		c.Assert(err, gc.IsNil)
	}
}
