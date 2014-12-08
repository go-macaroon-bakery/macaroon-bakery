package mgostorage_test

import (
	"fmt"

	"github.com/juju/testing"
	gc "gopkg.in/check.v1"
	"gopkg.in/mgo.v2"

	"github.com/go-macaroon-bakery/macaroon-bakery/bakery"
	storage "github.com/go-macaroon-bakery/macaroon-bakery/bakery/mgostorage"
)

type StorageSuite struct {
	testing.MgoSuite
	session *mgo.Session
	store   bakery.Storage
}

var _ = gc.Suite(&StorageSuite{})

func (s *StorageSuite) SetUpTest(c *gc.C) {
	s.MgoSuite.SetUpTest(c)
	s.session = testing.MgoServer.MustDial()

	store, err := storage.NewMgoStorage(s.session, "test", "items")
	c.Assert(err, gc.IsNil)

	s.store = store
}

func (s *StorageSuite) TearDownTest(c *gc.C) {
	s.session.Close()
	s.MgoSuite.TearDownTest(c)
}

func (s *StorageSuite) TestMgoStorage(c *gc.C) {
	err := s.store.Put("foo", "bar")
	c.Assert(err, gc.IsNil)
	item, err := s.store.Get("foo")
	c.Assert(err, gc.IsNil)
	c.Assert(item, gc.Equals, "bar")

	err = s.store.Put("bletch", "blat")
	c.Assert(err, gc.IsNil)
	item, err = s.store.Get("bletch")
	c.Assert(err, gc.IsNil)
	c.Assert(item, gc.Equals, "blat")

	item, err = s.store.Get("nothing")
	c.Assert(err, gc.Equals, bakery.ErrNotFound)
	c.Assert(item, gc.Equals, "")

	err = s.store.Del("bletch")
	c.Assert(err, gc.IsNil)

	item, err = s.store.Get("bletch")
	c.Assert(err, gc.Equals, bakery.ErrNotFound)
	c.Assert(item, gc.Equals, "")
}

func (s *StorageSuite) TestMgoStorageUpsert(c *gc.C) {
	err := s.store.Put("foo", "bar")
	c.Assert(err, gc.IsNil)
	item, err := s.store.Get("foo")
	c.Assert(err, gc.IsNil)
	c.Assert(item, gc.Equals, "bar")

	err = s.store.Put("foo", "ba-ba")
	c.Assert(err, gc.IsNil)
	item, err = s.store.Get("foo")
	c.Assert(err, gc.IsNil)
	c.Assert(item, gc.Equals, "ba-ba")

}

func (s *StorageSuite) TestConcurrentMgoStorage(c *gc.C) {
	done := make(chan struct{})
	for i := 0; i < 3; i++ {
		i := i
		go func() {
			k := fmt.Sprint(i)
			err := s.store.Put(k, k)
			c.Check(err, gc.IsNil)
			v, err := s.store.Get(k)
			c.Check(v, gc.Equals, k)
			err = s.store.Del(k)
			c.Check(err, gc.IsNil)
			done <- struct{}{}
		}()
	}
	for i := 0; i < 3; i++ {
		<-done
	}
}
