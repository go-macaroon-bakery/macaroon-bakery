package mgostorage_test

import (
	"errors"

	"github.com/juju/testing"
	gc "gopkg.in/check.v1"
	"gopkg.in/mgo.v2"

	"github.com/go-macaroon-bakery/macaroon-bakery/bakery"
	storage "github.com/go-macaroon-bakery/macaroon-bakery/bakery/mgostorage"
)

type StorageIntegrationSuite struct {
	testing.MgoSuite
	session *mgo.Session
	store   bakery.Storage
	service *bakery.Service
}

var _ = gc.Suite(&StorageIntegrationSuite{})

func (s *StorageIntegrationSuite) SetUpTest(c *gc.C) {
	s.MgoSuite.SetUpTest(c)
	s.session = testing.MgoServer.MustDial()

	store, err := storage.NewMgoStorage(s.session, "test", "items")
	c.Assert(err, gc.IsNil)

	s.store = store

	keypair, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)

	params := bakery.NewServiceParams{Location: "local", Store: s.store, Key: keypair}
	service, err := bakery.NewService(params)
	c.Assert(err, gc.IsNil)
	c.Assert(service, gc.NotNil)

	s.service = service
}

func (s *StorageIntegrationSuite) TearDownTest(c *gc.C) {
	s.session.Close()
	s.MgoSuite.TearDownTest(c)
}

type testChecker struct{}

func (tc *testChecker) CheckFirstPartyCaveat(caveat string) error {
	if caveat != "is-authorised bob" {
		return errors.New("not bob")
	}
	return nil
}

func (s *StorageIntegrationSuite) TestCreateMacaroon(c *gc.C) {
	macaroon, err := s.service.NewMacaroon(
		"123",
		[]byte("abc"),
		[]bakery.Caveat{bakery.Caveat{Location: "", Condition: "is-authorised bob"}},
	)
	c.Assert(err, gc.IsNil)
	c.Assert(macaroon, gc.NotNil)

	item, err := s.store.Get("123")
	c.Assert(err, gc.IsNil)
	c.Assert(item, gc.DeepEquals, `{"RootKey":"YWJj"}`)

	request := s.service.NewRequest(&testChecker{})
	c.Assert(request, gc.NotNil)

	request.AddClientMacaroon(macaroon)
	err = request.Check()
	c.Assert(err, gc.IsNil)
}
