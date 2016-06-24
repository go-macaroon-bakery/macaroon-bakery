package httpbakery_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"

	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/bakerytest"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery"
)

type KeyringSuite struct {
	jujutesting.LoggingSuite
}

var _ = gc.Suite(&KeyringSuite{})

func (s *KeyringSuite) TestCachePrepopulated(c *gc.C) {
	cache := bakery.NewThirdPartyLocatorStore()
	key, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	expectInfo := bakery.ThirdPartyInfo{
		PublicKey: key.Public,
		Version:   bakery.LatestVersion,
	}
	cache.AddInfo("https://0.1.2.3/", expectInfo)
	kr := httpbakery.NewThirdPartyLocator(nil, cache)
	info, err := kr.ThirdPartyInfo("https://0.1.2.3/")
	c.Assert(err, gc.IsNil)
	c.Assert(info, jc.DeepEquals, expectInfo)
}

func (s *KeyringSuite) TestCacheMiss(c *gc.C) {
	d := bakerytest.NewDischarger(nil, nil)
	defer d.Close()
	kr := httpbakery.NewThirdPartyLocator(nil, nil)

	expectInfo := bakery.ThirdPartyInfo{
		PublicKey: *d.Service.PublicKey(),
		Version:   bakery.LatestVersion,
	}
	info, err := kr.ThirdPartyInfo(d.Location())
	c.Assert(err, gc.IsNil)
	c.Assert(info, jc.DeepEquals, expectInfo)

	// Close down the service and make sure that
	// the key is cached.
	d.Close()

	info, err = kr.ThirdPartyInfo(d.Location())
	c.Assert(err, gc.IsNil)
	c.Assert(info, jc.DeepEquals, expectInfo)
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
	kr := httpbakery.NewThirdPartyLocator(nil, nil)
	info, err := kr.ThirdPartyInfo(srv.URL)
	c.Assert(err, gc.ErrorMatches, `untrusted discharge URL "http://.*"`)
	c.Assert(info, jc.DeepEquals, bakery.ThirdPartyInfo{})

	// Check that it does work when we've enabled AllowInsecure.
	kr.AllowInsecure()
	info, err = kr.ThirdPartyInfo(srv.URL)
	c.Assert(err, gc.IsNil)
	c.Assert(info, jc.DeepEquals, bakery.ThirdPartyInfo{
		PublicKey: *d.Service.PublicKey(),
		Version:   bakery.LatestVersion,
	})
}

func (s *KeyringSuite) TestCustomHTTPClient(c *gc.C) {
	client := &http.Client{
		Transport: errorTransport{},
	}
	kr := httpbakery.NewThirdPartyLocator(client, nil)
	info, err := kr.ThirdPartyInfo("https://0.1.2.3/")
	c.Assert(err, gc.ErrorMatches, `Get https://0.1.2.3/discharge/info: custom round trip error`)
	c.Assert(info, jc.DeepEquals, bakery.ThirdPartyInfo{})
}

func (s *KeyringSuite) TestThirdPartyInfoForLocation(c *gc.C) {
	d := bakerytest.NewDischarger(nil, nil)
	defer d.Close()
	client := httpbakery.NewHTTPClient()
	info, err := httpbakery.ThirdPartyInfoForLocation(client, d.Location())
	c.Assert(err, gc.IsNil)
	expectedInfo := bakery.ThirdPartyInfo{
		PublicKey: *d.Service.PublicKey(),
		Version:   bakery.LatestVersion,
	}
	c.Assert(info, gc.DeepEquals, expectedInfo)

	// Check that it works with client==nil.
	info, err = httpbakery.ThirdPartyInfoForLocation(nil, d.Location())
	c.Assert(err, gc.IsNil)
	c.Assert(info, gc.DeepEquals, expectedInfo)
}

func (s *KeyringSuite) TestThirdPartyInfoForLocationWrongURL(c *gc.C) {
	client := httpbakery.NewHTTPClient()
	_, err := httpbakery.ThirdPartyInfoForLocation(client, "http://localhost:0")
	c.Assert(err, gc.ErrorMatches,
		`Get http://localhost:0/discharge/info: dial tcp 127.0.0.1:0: .*connection refused`)
}

func (s *KeyringSuite) TestThirdPartyInfoForLocationReturnsInvalidJSON(c *gc.C) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "BADJSON")
	}))
	defer ts.Close()
	client := httpbakery.NewHTTPClient()
	_, err := httpbakery.ThirdPartyInfoForLocation(client, ts.URL)
	c.Assert(err, gc.ErrorMatches,
		fmt.Sprintf(`unexpected content type text/plain; want application/json; content: BADJSON`))
}

func (s *KeyringSuite) TestThirdPartyInfoForLocationReturnsStatusInternalServerError(c *gc.C) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	client := httpbakery.NewHTTPClient()
	_, err := httpbakery.ThirdPartyInfoForLocation(client, ts.URL)
	c.Assert(err, gc.ErrorMatches,
		fmt.Sprintf(`GET %s/discharge/info: cannot unmarshal error response \(status 500 Internal Server Error\): unexpected content type text/plain; want application/json; content: `, ts.URL))
}

func (s *KeyringSuite) TestThirdPartyInfoForLocationFallbackToOldVersion(c *gc.C) {
	// Start a bakerytest discharger so we benefit from its TLS-verification-skip logic.
	d := bakerytest.NewDischarger(nil, nil)
	defer d.Close()

	key, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)

	// Start a server which serves the publickey endpoint only.
	mux := http.NewServeMux()
	server := httptest.NewTLSServer(mux)
	mux.HandleFunc("/publickey", func(w http.ResponseWriter, req *http.Request) {
		c.Check(req.Method, gc.Equals, "GET")
		data, err := json.Marshal(&httpbakery.PublicKeyResponse{
			PublicKey: &key.Public,
		})
		c.Check(err, gc.IsNil)
		w.Write(data)
	})
	info, err := httpbakery.ThirdPartyInfoForLocation(httpbakery.NewHTTPClient(), server.URL)
	c.ExpectFailure("third party public key fallback doesn't currently work")
	c.Assert(err, gc.IsNil)
	expectedInfo := bakery.ThirdPartyInfo{
		PublicKey: *d.Service.PublicKey(),
		Version:   bakery.Version1,
	}
	c.Assert(info, gc.DeepEquals, expectedInfo)
}

type errorTransport struct{}

func (errorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, errgo.New("custom round trip error")
}
