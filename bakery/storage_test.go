package bakery_test

import (
	"fmt"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"gopkg.in/macaroon-bakery.v1/bakery"
)

type StorageSuite struct{}

var _ = gc.Suite(&StorageSuite{})

func (*StorageSuite) TestMemStorage(c *gc.C) {
	store := bakery.NewMemStorage()
	err := store.Put("foo", "bar")
	c.Assert(err, gc.IsNil)
	item, err := store.Get("foo")
	c.Assert(err, gc.IsNil)
	c.Assert(item, gc.Equals, "bar")

	err = store.Put("bletch", "blat")
	c.Assert(err, gc.IsNil)
	item, err = store.Get("bletch")
	c.Assert(err, gc.IsNil)
	c.Assert(item, gc.Equals, "blat")

	item, err = store.Get("nothing")
	c.Assert(err, gc.Equals, bakery.ErrNotFound)
	c.Assert(item, gc.Equals, "")

	err = store.Del("bletch")
	c.Assert(err, gc.IsNil)

	item, err = store.Get("bletch")
	c.Assert(err, gc.Equals, bakery.ErrNotFound)
	c.Assert(item, gc.Equals, "")
}

func (*StorageSuite) TestConcurrentMemStorage(c *gc.C) {
	// If locking is not done right, this test will
	// definitely trigger the race detector.
	done := make(chan struct{})
	store := bakery.NewMemStorage()
	for i := 0; i < 3; i++ {
		i := i
		go func() {
			k := fmt.Sprint(i)
			err := store.Put(k, k)
			c.Check(err, gc.IsNil)
			v, err := store.Get(k)
			c.Check(v, gc.Equals, k)
			err = store.Del(k)
			c.Check(err, gc.IsNil)
			done <- struct{}{}
		}()
	}
	for i := 0; i < 3; i++ {
		<-done
	}
}

func (*StorageSuite) TestMemRootKeyStorage(c *gc.C) {
	store := bakery.NewMemRootKeyStorage()
	key, err := store.Get("x")
	c.Assert(err, gc.Equals, bakery.ErrNotFound)
	c.Assert(key, gc.IsNil)

	key, err = store.Get("0")
	c.Assert(err, gc.Equals, bakery.ErrNotFound)
	c.Assert(key, gc.IsNil)

	key, id, err := store.RootKey()
	c.Assert(err, gc.IsNil)
	c.Assert(key, gc.HasLen, 24)
	c.Assert(id, gc.Equals, "0")

	key1, id1, err := store.RootKey()
	c.Assert(err, gc.IsNil)
	c.Assert(key1, jc.DeepEquals, key)
	c.Assert(id1, gc.Equals, id)

	key2, err := store.Get(id)
	c.Assert(err, gc.IsNil)
	c.Assert(key2, jc.DeepEquals, key)

	_, err = store.Get("1")
	c.Assert(err, gc.Equals, bakery.ErrNotFound)
}
