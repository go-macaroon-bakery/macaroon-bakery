package main

import (
	"net/http"
	"testing"

	qt "github.com/frankban/quicktest"

	"gopkg.in/macaroon-bakery.v3/bakery"
)

func TestExample(t *testing.T) {
	c := qt.New(t)
	f := newFixture(c)
	client := newClient()
	serverEndpoint, err := serve(func(endpoint string) (http.Handler, error) {
		return targetService(endpoint, f.authEndpoint, f.authPublicKey)
	})
	c.Assert(err, qt.IsNil)
	c.Logf("gold request")
	resp, err := clientRequest(client, serverEndpoint+"/gold")
	c.Assert(err, qt.IsNil)
	c.Assert(resp, qt.Equals, "all is golden")

	c.Logf("silver request")
	resp, err = clientRequest(client, serverEndpoint+"/silver")
	c.Assert(err, qt.IsNil)
	c.Assert(resp, qt.Equals, "every cloud has a silver lining")
}

func BenchmarkExample(b *testing.B) {
	c := qt.New(b)
	f := newFixture(c)
	client := newClient()
	serverEndpoint, err := serve(func(endpoint string) (http.Handler, error) {
		return targetService(endpoint, f.authEndpoint, f.authPublicKey)
	})
	c.Assert(err, qt.IsNil)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := clientRequest(client, serverEndpoint+"/gold")
		c.Assert(err, qt.IsNil)
		c.Assert(resp, qt.Equals, "all is golden")
	}
}

type fixture struct {
	authEndpoint  string
	authPublicKey *bakery.PublicKey
}

func newFixture(c *qt.C) *fixture {
	var f fixture
	key := bakery.MustGenerateKey()
	f.authPublicKey = &key.Public
	var err error
	f.authEndpoint, err = serve(func(endpoint string) (http.Handler, error) {
		return authService(endpoint, key)
	})
	c.Assert(err, qt.IsNil)
	return &f
}
