package httpbakery_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"

	jujutesting "github.com/juju/testing"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v1/bakery"
	"gopkg.in/macaroon-bakery.v1/bakerytest"
	"gopkg.in/macaroon-bakery.v1/httpbakery"
)

type KeyringSuite struct {
	jujutesting.LoggingSuite
}

var _ = gc.Suite(&KeyringSuite{})

func (s *KeyringSuite) TestCachePrepopulated(c *gc.C) {
	cache := bakery.NewPublicKeyRing()
	key, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	cache.AddPublicKeyForLocation("https://0.1.2.3/", true, &key.Public)
	kr := httpbakery.NewPublicKeyRing(nil, cache)
	pk, err := kr.PublicKeyForLocation("https://0.1.2.3/")
	c.Assert(err, gc.IsNil)
	c.Assert(*pk, gc.Equals, key.Public)
}

func (s *KeyringSuite) TestCacheMiss(c *gc.C) {
	d := bakerytest.NewDischarger(nil, nil)
	defer d.Close()
	kr := httpbakery.NewPublicKeyRing(nil, nil)

	expectPublicKey := d.Service.PublicKey()
	pk, err := kr.PublicKeyForLocation(d.Location())
	c.Assert(err, gc.IsNil)
	c.Assert(*pk, gc.Equals, *expectPublicKey)

	// Close down the service and make sure that
	// the key is cached.
	d.Close()

	pk, err = kr.PublicKeyForLocation(d.Location())
	c.Assert(err, gc.IsNil)
	c.Assert(*pk, gc.Equals, *expectPublicKey)
}

func (s *KeyringSuite) TestInsecureURL(c *gc.C) {
	// Set up a discharger with an non-HTTPS access point.
	d := bakerytest.NewDischarger(nil, nil)
	defer d.Close()
	httpsDischargeURL, err := url.Parse(d.Location())
	c.Assert(err, gc.IsNil)

	srv := httptest.NewServer(httputil.NewSingleHostReverseProxy(httpsDischargeURL))
	defer srv.Close()

	// Check that we are refused because it's an insecure URL.
	kr := httpbakery.NewPublicKeyRing(nil, nil)
	pk, err := kr.PublicKeyForLocation(srv.URL)
	c.Assert(err, gc.ErrorMatches, `untrusted discharge URL "http://.*"`)
	c.Assert(pk, gc.IsNil)

	// Check that it does work when we've enabled AllowInsecure.
	kr.AllowInsecure()
	pk, err = kr.PublicKeyForLocation(srv.URL)
	c.Assert(err, gc.IsNil)
	c.Assert(*pk, gc.Equals, *d.Service.PublicKey())
}

func (s *KeyringSuite) TestCustomHTTPClient(c *gc.C) {
	client := &http.Client{
		Transport: errorTransport{},
	}
	kr := httpbakery.NewPublicKeyRing(client, nil)
	pk, err := kr.PublicKeyForLocation("https://0.1.2.3/")
	c.Assert(err, gc.ErrorMatches, `cannot get public key from "https://0.1.2.3/publickey": Get https://0.1.2.3/publickey: custom round trip error`)
	c.Assert(pk, gc.IsNil)
}

func (s *KeyringSuite) TestPublicKey(c *gc.C) {
	d := bakerytest.NewDischarger(nil, noCaveatChecker)
	defer d.Close()
	client := httpbakery.NewHTTPClient()
	publicKey, err := httpbakery.PublicKeyForLocation(client, d.Location())
	c.Assert(err, gc.IsNil)
	expectedKey := d.Service.PublicKey()
	c.Assert(publicKey, gc.DeepEquals, expectedKey)

	// Check that it works with client==nil.
	publicKey, err = httpbakery.PublicKeyForLocation(nil, d.Location())
	c.Assert(err, gc.IsNil)
	c.Assert(publicKey, gc.DeepEquals, expectedKey)
}

func (s *KeyringSuite) TestPublicKeyWrongURL(c *gc.C) {
	client := httpbakery.NewHTTPClient()
	_, err := httpbakery.PublicKeyForLocation(client, "http://localhost:0")
	c.Assert(err, gc.ErrorMatches,
		`cannot get public key from "http://localhost:0/publickey": Get http://localhost:0/publickey: dial tcp 127.0.0.1:0: .*connection refused`)
}

func (s *KeyringSuite) TestPublicKeyReturnsInvalidJSON(c *gc.C) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "BADJSON")
	}))
	defer ts.Close()
	client := httpbakery.NewHTTPClient()
	_, err := httpbakery.PublicKeyForLocation(client, ts.URL)
	c.Assert(err, gc.ErrorMatches,
		fmt.Sprintf(`failed to decode response from "%s/publickey": invalid character 'B' looking for beginning of value`, ts.URL))
}

func (s *KeyringSuite) TestPublicKeyReturnsStatusInternalServerError(c *gc.C) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	client := httpbakery.NewHTTPClient()
	_, err := httpbakery.PublicKeyForLocation(client, ts.URL)
	c.Assert(err, gc.ErrorMatches,
		fmt.Sprintf(`cannot get public key from "%s/publickey": got status 500 Internal Server Error`, ts.URL))
}

type errorTransport struct{}

func (errorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, errgo.New("custom round trip error")
}
