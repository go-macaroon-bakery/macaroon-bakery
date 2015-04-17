package mgostorage_test

import (
	"errors"
	"fmt"

	"github.com/juju/testing"
	gc "gopkg.in/check.v1"
	"gopkg.in/macaroon.v1"
	"gopkg.in/mgo.v2"

	"gopkg.in/macaroon-bakery.v1/bakery"
	"gopkg.in/macaroon-bakery.v1/bakery/checkers"
	"gopkg.in/macaroon-bakery.v1/bakery/mgostorage"
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

	store, err := mgostorage.New(s.session.DB("test").C("items"))
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

type testChecker struct{}

func (tc *testChecker) CheckFirstPartyCaveat(caveat string) error {
	if caveat != "is-authorised bob" {
		return errors.New("not bob")
	}
	return nil
}

func (s *StorageSuite) TestCreateMacaroon(c *gc.C) {
	keypair, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)

	params := bakery.NewServiceParams{Location: "local", Store: s.store, Key: keypair}
	service, err := bakery.NewService(params)
	c.Assert(err, gc.IsNil)
	c.Assert(service, gc.NotNil)

	m, err := service.NewMacaroon(
		"123",
		[]byte("abc"),
		[]checkers.Caveat{checkers.Caveat{Location: "", Condition: "is-authorised bob"}},
	)
	c.Assert(err, gc.IsNil)
	c.Assert(m, gc.NotNil)

	item, err := s.store.Get("123")
	c.Assert(err, gc.IsNil)
	c.Assert(item, gc.DeepEquals, `{"RootKey":"YWJj"}`)

	err = service.Check(macaroon.Slice{m}, &testChecker{})
	c.Assert(err, gc.IsNil)
}
