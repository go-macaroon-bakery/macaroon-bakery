package bakery_test

import (
	"testing"

	qt "github.com/frankban/quicktest"

	"gopkg.in/macaroon-bakery.v3/bakery"
)

func TestMemStore(t *testing.T) {
	c := qt.New(t)
	store := bakery.NewMemRootKeyStore()
	key, err := store.Get(nil, []byte("x"))
	c.Assert(err, qt.Equals, bakery.ErrNotFound)
	c.Assert(key, qt.IsNil)

	key, err = store.Get(nil, []byte("0"))
	c.Assert(err, qt.Equals, bakery.ErrNotFound)
	c.Assert(key, qt.IsNil)

	key, id, err := store.RootKey(nil)
	c.Assert(err, qt.IsNil)
	c.Assert(key, qt.HasLen, 24)
	c.Assert(string(id), qt.Equals, "0")

	key1, id1, err := store.RootKey(nil)
	c.Assert(err, qt.IsNil)
	c.Assert(key1, qt.DeepEquals, key)
	c.Assert(id1, qt.DeepEquals, id)

	key2, err := store.Get(nil, id)
	c.Assert(err, qt.IsNil)
	c.Assert(key2, qt.DeepEquals, key)

	_, err = store.Get(nil, []byte("1"))
	c.Assert(err, qt.Equals, bakery.ErrNotFound)
}
