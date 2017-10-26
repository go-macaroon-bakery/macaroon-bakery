package bakery_test

import (
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"gopkg.in/macaroon-bakery.v2/bakery"
)

type StoreSuite struct{}

var _ = gc.Suite(&StoreSuite{})

func (*StoreSuite) TestMemStore(c *gc.C) {
	store := bakery.NewMemRootKeyStore()
	key, err := store.Get(nil, []byte("x"))
	c.Assert(err, gc.Equals, bakery.ErrNotFound)
	c.Assert(key, gc.IsNil)

	key, err = store.Get(nil, []byte("0"))
	c.Assert(err, gc.Equals, bakery.ErrNotFound)
	c.Assert(key, gc.IsNil)

	key, id, err := store.RootKey(nil)
	c.Assert(err, gc.IsNil)
	c.Assert(key, gc.HasLen, 24)
	c.Assert(string(id), gc.Equals, "0")

	key1, id1, err := store.RootKey(nil)
	c.Assert(err, gc.IsNil)
	c.Assert(key1, jc.DeepEquals, key)
	c.Assert(id1, gc.DeepEquals, id)

	key2, err := store.Get(nil, id)
	c.Assert(err, gc.IsNil)
	c.Assert(key2, jc.DeepEquals, key)

	_, err = store.Get(nil, []byte("1"))
	c.Assert(err, gc.Equals, bakery.ErrNotFound)
}
