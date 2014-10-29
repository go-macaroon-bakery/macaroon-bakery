package main

import (
	"net/http"
	"testing"

	"github.com/rogpeppe/macaroon/caveatid"
	gc "gopkg.in/check.v1"
)

func TestPackage(t *testing.T) {
	gc.TestingT(t)
}

type exampleSuite struct {
	authEndpoint   string
	authPublicKey  *[32]byte
	serverEndpoint string
}

var _ = gc.Suite(&exampleSuite{})

func (s *exampleSuite) SetUpSuite(c *gc.C) {
	key, err := caveatid.GenerateKey()
	c.Assert(err, gc.IsNil)
	s.authPublicKey = key.PublicKey()
	s.authEndpoint, err = serve(func(endpoint string) (http.Handler, error) {
		return authService(endpoint, key)
	})
	c.Assert(err, gc.IsNil)
	s.serverEndpoint, err = serve(func(endpoint string) (http.Handler, error) {
		return targetService(endpoint, s.authEndpoint, s.authPublicKey)
	})
	c.Assert(err, gc.IsNil)
}

func (s *exampleSuite) TestExample(c *gc.C) {
	c.Logf("gold request")
	resp, err := clientRequest(s.serverEndpoint + "/gold")
	c.Assert(err, gc.IsNil)
	c.Assert(resp, gc.Equals, "all is golden")

	c.Logf("silver request")
	resp, err = clientRequest(s.serverEndpoint + "/silver")
	c.Assert(err, gc.IsNil)
	c.Assert(resp, gc.Equals, "every cloud has a silver lining")
}

func (s *exampleSuite) BenchmarkExample(c *gc.C) {
	c.ResetTimer()
	for i := 0; i < c.N; i++ {
		resp, err := clientRequest(s.serverEndpoint)
		c.Assert(err, gc.IsNil)
		c.Assert(resp, gc.Equals, "hello, world\n")
	}
}
