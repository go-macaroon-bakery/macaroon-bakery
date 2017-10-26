package main

import (
	"net/http"
	"testing"

	jujutesting "github.com/juju/testing"
	gc "gopkg.in/check.v1"

	"gopkg.in/macaroon-bakery.v2/bakery"
)

func TestPackage(t *testing.T) {
	gc.TestingT(t)
}

type exampleSuite struct {
	jujutesting.LoggingSuite
	authEndpoint  string
	authPublicKey *bakery.PublicKey
}

var _ = gc.Suite(&exampleSuite{})

func (s *exampleSuite) SetUpSuite(c *gc.C) {
	s.LoggingSuite.SetUpSuite(c)
	key, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	s.authPublicKey = &key.Public
	s.authEndpoint, err = serve(func(endpoint string) (http.Handler, error) {
		return authService(endpoint, key)
	})
	c.Assert(err, gc.IsNil)
}

func (s *exampleSuite) TestExample(c *gc.C) {
	client := newClient()
	serverEndpoint, err := serve(func(endpoint string) (http.Handler, error) {
		return targetService(endpoint, s.authEndpoint, s.authPublicKey)
	})
	c.Assert(err, gc.IsNil)
	c.Logf("gold request")
	resp, err := clientRequest(client, serverEndpoint+"/gold")
	c.Assert(err, gc.IsNil)
	c.Assert(resp, gc.Equals, "all is golden")

	c.Logf("silver request")
	resp, err = clientRequest(client, serverEndpoint+"/silver")
	c.Assert(err, gc.IsNil)
	c.Assert(resp, gc.Equals, "every cloud has a silver lining")
}

func (s *exampleSuite) BenchmarkExample(c *gc.C) {
	client := newClient()
	serverEndpoint, err := serve(func(endpoint string) (http.Handler, error) {
		return targetService(endpoint, s.authEndpoint, s.authPublicKey)
	})
	c.Assert(err, gc.IsNil)
	c.ResetTimer()
	for i := 0; i < c.N; i++ {
		resp, err := clientRequest(client, serverEndpoint)
		c.Assert(err, gc.IsNil)
		c.Assert(resp, gc.Equals, "hello, world\n")
	}
}
