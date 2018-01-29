package dbrootkeystore_test

import (
	"time"

	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	"golang.org/x/net/context"
	gc "gopkg.in/check.v1"
	errgo "gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/bakery/dbrootkeystore"
)

type RootKeyStoreSuite struct {
	testing.LoggingSuite
}

var _ = gc.Suite(&RootKeyStoreSuite{})

var epoch = time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)

var isValidWithPolicyTests = []struct {
	about  string
	policy dbrootkeystore.Policy
	now    time.Time
	key    dbrootkeystore.RootKey
	expect bool
}{{
	about: "success",
	policy: dbrootkeystore.Policy{
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
	policy: dbrootkeystore.Policy{
		GenerateInterval: 2 * time.Minute,
		ExpiryDuration:   3 * time.Minute,
	},
	now:    epoch.Add(20 * time.Minute),
	key:    dbrootkeystore.RootKey{},
	expect: false,
}, {
	about: "created too early",
	policy: dbrootkeystore.Policy{
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
	policy: dbrootkeystore.Policy{
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
	policy: dbrootkeystore.Policy{
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

func (s *RootKeyStoreSuite) TestIsValidWithPolicy(c *gc.C) {
	for i, test := range isValidWithPolicyTests {
		c.Logf("test %d: %v", i, test.about)
		c.Assert(test.key.IsValidWithPolicy(test.policy, test.now), gc.Equals, test.expect)
	}
}

func (s *RootKeyStoreSuite) TestRootKeyUsesKeysValidWithPolicy(c *gc.C) {
	// We re-use the TestIsValidWithPolicy tests so that we
	// know that the database-backed logic uses the same behaviour.
	for i, test := range isValidWithPolicyTests {
		c.Logf("test %d: %v", i, test.about)
		if test.key.RootKey == nil {
			// We don't store empty root keys in the database.
			c.Logf("skipping test with empty root key")
			continue
		}
		// Prime the collection with the root key document.
		b := memBackingWithKeys([]dbrootkeystore.RootKey{test.key})
		store := dbrootkeystore.NewRootKeys(10, stoppedClock(test.now)).NewStore(b, test.policy)
		key, id, err := store.RootKey(context.Background())
		c.Assert(err, gc.Equals, nil)
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
	clock := clockVal(&now)
	b := make(memBacking)

	store := dbrootkeystore.NewRootKeys(10, clock).NewStore(b, dbrootkeystore.Policy{
		GenerateInterval: 2 * time.Minute,
		ExpiryDuration:   5 * time.Minute,
	})
	key, id, err := store.RootKey(context.Background())
	c.Assert(err, gc.Equals, nil)
	c.Assert(key, gc.HasLen, 24)
	c.Assert(id, gc.HasLen, 32)

	// If we get a key within the generate interval, we should
	// get the same one.
	now = epoch.Add(time.Minute)
	key1, id1, err := store.RootKey(context.Background())
	c.Assert(err, gc.Equals, nil)
	c.Assert(key1, gc.DeepEquals, key)
	c.Assert(id1, gc.DeepEquals, id)

	// A different store instance should get the same root key.
	store1 := dbrootkeystore.NewRootKeys(10, clock).NewStore(b, dbrootkeystore.Policy{
		GenerateInterval: 2 * time.Minute,
		ExpiryDuration:   5 * time.Minute,
	})
	key1, id1, err = store1.RootKey(context.Background())
	c.Assert(err, gc.Equals, nil)
	c.Assert(key1, gc.DeepEquals, key)
	c.Assert(id1, gc.DeepEquals, id)

	// After the generation interval has passed, we should generate a new key.
	now = epoch.Add(2*time.Minute + time.Second)
	key1, id1, err = store.RootKey(context.Background())
	c.Assert(err, gc.Equals, nil)
	c.Assert(key, gc.HasLen, 24)
	c.Assert(id, gc.HasLen, 32)
	c.Assert(key1, gc.Not(gc.DeepEquals), key)
	c.Assert(id1, gc.Not(gc.DeepEquals), id)

	// The other store should pick it up too.
	key2, id2, err := store1.RootKey(context.Background())
	c.Assert(err, gc.Equals, nil)
	c.Assert(key2, gc.DeepEquals, key1)
	c.Assert(id2, gc.DeepEquals, id1)
}

func (s *RootKeyStoreSuite) TestRootKeyDefaultGenerateInterval(c *gc.C) {
	now := epoch
	clock := clockVal(&now)
	b := make(memBacking)
	store := dbrootkeystore.NewRootKeys(10, clock).NewStore(b, dbrootkeystore.Policy{
		ExpiryDuration: 5 * time.Minute,
	})
	key, id, err := store.RootKey(context.Background())
	c.Assert(err, gc.Equals, nil)

	now = epoch.Add(5 * time.Minute)
	key1, id1, err := store.RootKey(context.Background())
	c.Assert(err, gc.Equals, nil)
	c.Assert(key1, jc.DeepEquals, key)
	c.Assert(id1, jc.DeepEquals, id)

	now = epoch.Add(5*time.Minute + time.Millisecond)
	key1, id1, err = store.RootKey(context.Background())
	c.Assert(err, gc.Equals, nil)
	c.Assert(string(key1), gc.Not(gc.Equals), string(key))
	c.Assert(string(id1), gc.Not(gc.Equals), string(id))
}

var preferredRootKeyTests = []struct {
	about    string
	now      time.Time
	keys     []dbrootkeystore.RootKey
	policy   dbrootkeystore.Policy
	expectId string
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
	policy: dbrootkeystore.Policy{
		GenerateInterval: 5 * time.Minute,
		ExpiryDuration:   7 * time.Minute,
	},
	expectId: "id1",
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
	policy: dbrootkeystore.Policy{
		GenerateInterval: 5 * time.Minute,
		ExpiryDuration:   7 * time.Minute,
	},
	expectId: "id1",
}}

func (s *RootKeyStoreSuite) TestPreferredRootKeyFromDatabase(c *gc.C) {
	for i, test := range preferredRootKeyTests {
		c.Logf("%d: %v", i, test.about)
		b := memBackingWithKeys(test.keys)
		store := dbrootkeystore.NewRootKeys(10, stoppedClock(test.now)).NewStore(b, test.policy)
		_, id, err := store.RootKey(context.Background())
		c.Assert(err, gc.Equals, nil)
		c.Assert(string(id), gc.DeepEquals, test.expectId)
	}
}

func (s *RootKeyStoreSuite) TestPreferredRootKeyFromCache(c *gc.C) {
	for i, test := range preferredRootKeyTests {
		c.Logf("%d: %v", i, test.about)
		b := memBackingWithKeys(test.keys)
		store := dbrootkeystore.NewRootKeys(10, stoppedClock(test.now)).NewStore(b, test.policy)
		// Ensure that all the keys are in cache by getting all of them.
		for _, key := range test.keys {
			got, err := store.Get(context.Background(), key.Id)
			c.Assert(err, gc.Equals, nil)
			c.Assert(got, jc.DeepEquals, key.RootKey)
		}
		// Remove all the keys from the collection so that
		// we know we must be acquiring them from the cache.
		for id := range b {
			delete(b, id)
		}

		// Test that RootKey returns the expected key.
		_, id, err := store.RootKey(context.Background())
		c.Assert(err, gc.Equals, nil)
		c.Assert(string(id), jc.DeepEquals, test.expectId)
	}
}

func (s *RootKeyStoreSuite) TestGet(c *gc.C) {
	now := epoch
	clock := clockVal(&now)

	mb := make(memBacking)
	b := &funcBacking{Backing: mb}
	store := dbrootkeystore.NewRootKeys(5, clock).NewStore(b, dbrootkeystore.Policy{
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
		c.Assert(err, gc.Equals, nil)
		c.Assert(keyIds[string(id)], gc.Equals, false)
		keys = append(keys, idKey{string(id), key})
		now = now.Add(time.Minute + time.Second)
	}
	for i, k := range keys {
		key, err := store.Get(context.Background(), []byte(k.id))
		c.Assert(err, gc.Equals, nil, gc.Commentf("key %d (%s)", i, k.id))
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
	b.getKey = func(id []byte) (dbrootkeystore.RootKey, error) {
		fetched = append(fetched, string(id))
		return mb.GetKey(id)
	}
	c.Logf("testing cache")

	for i := len(keys) - 1; i >= 0; i-- {
		k := keys[i]
		key, err := store.Get(context.Background(), []byte(k.id))
		c.Assert(err, gc.Equals, nil)
		c.Assert(err, gc.Equals, nil, gc.Commentf("key %d (%s)", i, k.id))
		c.Assert(key, gc.DeepEquals, k.key, gc.Commentf("key %d (%s)", i, k.id))
	}
	c.Assert(len(fetched), gc.Equals, len(keys)-6)
	for i, id := range fetched {
		c.Assert(id, gc.Equals, keys[len(keys)-6-i-1].id)
	}
}

func (s *RootKeyStoreSuite) TestGetCachesMisses(c *gc.C) {
	var fetched []string
	mb := make(memBacking)
	b := &funcBacking{
		Backing: mb,
		getKey: func(id []byte) (dbrootkeystore.RootKey, error) {
			fetched = append(fetched, string(id))
			return mb.GetKey(id)
		},
	}
	store := dbrootkeystore.NewRootKeys(5, nil).NewStore(b, dbrootkeystore.Policy{
		GenerateInterval: 1 * time.Minute,
		ExpiryDuration:   30 * time.Minute,
	})
	key, err := store.Get(context.Background(), []byte("foo"))
	c.Assert(err, gc.Equals, bakery.ErrNotFound)
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
	clock := clockVal(&now)
	b := &funcBacking{
		Backing: make(memBacking),
	}
	store := dbrootkeystore.NewRootKeys(10, clock).NewStore(b, dbrootkeystore.Policy{
		ExpiryDuration: 5 * time.Minute,
	})
	_, id, err := store.RootKey(context.Background())
	c.Assert(err, gc.Equals, nil)

	b.getKey = func(id []byte) (dbrootkeystore.RootKey, error) {
		c.Errorf("GetKey unexpectedly called")
		return dbrootkeystore.RootKey{}, errgo.New("unexpected call to GetKey")
	}
	now = epoch.Add(15 * time.Minute)

	_, err = store.Get(context.Background(), id)
	c.Assert(err, gc.Equals, bakery.ErrNotFound)
}

func memBackingWithKeys(keys []dbrootkeystore.RootKey) memBacking {
	b := make(memBacking)
	for _, key := range keys {
		err := b.InsertKey(key)
		if err != nil {
			panic(err)
		}
	}
	return b
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

type memBacking map[string]dbrootkeystore.RootKey

func (b memBacking) GetKey(id []byte) (dbrootkeystore.RootKey, error) {
	key, ok := b[string(id)]
	if !ok {
		return dbrootkeystore.RootKey{}, bakery.ErrNotFound
	}
	return key, nil
}

func (b memBacking) FindLatestKey(createdAfter, expiresAfter, expiresBefore time.Time) (dbrootkeystore.RootKey, error) {
	var best dbrootkeystore.RootKey
	for _, k := range b {
		if afterEq(k.Created, createdAfter) &&
			afterEq(k.Expires, expiresAfter) &&
			beforeEq(k.Expires, expiresBefore) &&
			k.Created.After(best.Created) {
			best = k
		}
	}
	return best, nil
}

func (b memBacking) InsertKey(key dbrootkeystore.RootKey) error {
	if _, ok := b[string(key.Id)]; ok {
		return errgo.Newf("duplicate key")
	}
	b[string(key.Id)] = key
	return nil
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

// afterEq reports whether t0 is after or equal to t1.
func afterEq(t0, t1 time.Time) bool {
	return !t0.Before(t1)
}

// beforeEq reports whether t1 is before or equal to t0.
func beforeEq(t0, t1 time.Time) bool {
	return !t0.After(t1)
}

func stoppedClock(t time.Time) dbrootkeystore.Clock {
	return clockFunc(func() time.Time {
		return t
	})
}
