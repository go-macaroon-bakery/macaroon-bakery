package httpbakery_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync"

	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/httprequest.v1"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/bakerytest"
	"gopkg.in/macaroon-bakery.v2/httpbakery"
)

type KeyringSuite struct {
	jujutesting.LoggingSuite
}

var _ = gc.Suite(&KeyringSuite{})

func (s *KeyringSuite) TestCachePrepopulated(c *gc.C) {
	cache := bakery.NewThirdPartyStore()
	key, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	expectInfo := bakery.ThirdPartyInfo{
		PublicKey: key.Public,
		Version:   bakery.LatestVersion,
	}
	cache.AddInfo("https://0.1.2.3/", expectInfo)
	kr := httpbakery.NewThirdPartyLocator(nil, cache)
	info, err := kr.ThirdPartyInfo(testContext, "https://0.1.2.3/")
	c.Assert(err, gc.IsNil)
	c.Assert(info, jc.DeepEquals, expectInfo)
}

func (s *KeyringSuite) TestCacheMiss(c *gc.C) {
	d := bakerytest.NewDischarger(nil)
	defer d.Close()
	kr := httpbakery.NewThirdPartyLocator(nil, nil)

	expectInfo := bakery.ThirdPartyInfo{
		PublicKey: d.Key.Public,
		Version:   bakery.LatestVersion,
	}
	location := d.Location()
	info, err := kr.ThirdPartyInfo(testContext, location)
	c.Assert(err, gc.IsNil)
	c.Assert(info, jc.DeepEquals, expectInfo)

	// Close down the service and make sure that
	// the key is cached.
	d.Close()

	info, err = kr.ThirdPartyInfo(testContext, location)
	c.Assert(err, gc.IsNil)
	c.Assert(info, jc.DeepEquals, expectInfo)
}

func (s *KeyringSuite) TestInsecureURL(c *gc.C) {
	// Set up a discharger with an non-HTTPS access point.
	d := bakerytest.NewDischarger(nil)
	defer d.Close()
	httpsDischargeURL, err := url.Parse(d.Location())
	c.Assert(err, gc.IsNil)

	srv := httptest.NewServer(httputil.NewSingleHostReverseProxy(httpsDischargeURL))
	defer srv.Close()

	// Check that we are refused because it's an insecure URL.
	kr := httpbakery.NewThirdPartyLocator(nil, nil)
	info, err := kr.ThirdPartyInfo(testContext, srv.URL)
	c.Assert(err, gc.ErrorMatches, `untrusted discharge URL "http://.*"`)
	c.Assert(info, jc.DeepEquals, bakery.ThirdPartyInfo{})

	// Check that it does work when we've enabled AllowInsecure.
	kr.AllowInsecure()
	info, err = kr.ThirdPartyInfo(testContext, srv.URL)
	c.Assert(err, gc.IsNil)
	c.Assert(info, jc.DeepEquals, bakery.ThirdPartyInfo{
		PublicKey: d.Key.Public,
		Version:   bakery.LatestVersion,
	})
}

func (s *KeyringSuite) TestConcurrentThirdPartyInfo(c *gc.C) {
	// This test is designed to fail only if run with the race detector
	// enabled.
	d := bakerytest.NewDischarger(nil)
	defer d.Close()
	kr := httpbakery.NewThirdPartyLocator(nil, nil)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			_, err := kr.ThirdPartyInfo(testContext, d.Location())
			c.Check(err, gc.IsNil)
			defer wg.Done()
		}()
	}
	wg.Wait()
}

func (s *KeyringSuite) TestCustomHTTPClient(c *gc.C) {
	client := &http.Client{
		Transport: errorTransport{},
	}
	kr := httpbakery.NewThirdPartyLocator(client, nil)
	info, err := kr.ThirdPartyInfo(testContext, "https://0.1.2.3/")
	c.Assert(err, gc.ErrorMatches, `(Get|GET) https://0.1.2.3/discharge/info: custom round trip error`)
	c.Assert(info, jc.DeepEquals, bakery.ThirdPartyInfo{})
}

func (s *KeyringSuite) TestThirdPartyInfoForLocation(c *gc.C) {
	d := bakerytest.NewDischarger(nil)
	defer d.Close()
	client := httpbakery.NewHTTPClient()
	info, err := httpbakery.ThirdPartyInfoForLocation(testContext, client, d.Location())
	c.Assert(err, gc.IsNil)
	expectedInfo := bakery.ThirdPartyInfo{
		PublicKey: d.Key.Public,
		Version:   bakery.LatestVersion,
	}
	c.Assert(info, gc.DeepEquals, expectedInfo)

	// Check that it works with client==nil.
	info, err = httpbakery.ThirdPartyInfoForLocation(testContext, nil, d.Location())
	c.Assert(err, gc.IsNil)
	c.Assert(info, gc.DeepEquals, expectedInfo)
}

func (s *KeyringSuite) TestThirdPartyInfoForLocationWrongURL(c *gc.C) {
	client := httpbakery.NewHTTPClient()
	_, err := httpbakery.ThirdPartyInfoForLocation(testContext, client, "http://localhost:0")
	c.Logf("%v", errgo.Details(err))
	c.Assert(err, gc.ErrorMatches,
		`(Get|GET) http://localhost:0/discharge/info: dial tcp 127.0.0.1:0: .*connection refused`)
}

func (s *KeyringSuite) TestThirdPartyInfoForLocationReturnsInvalidJSON(c *gc.C) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "BADJSON")
	}))
	defer ts.Close()
	client := httpbakery.NewHTTPClient()
	_, err := httpbakery.ThirdPartyInfoForLocation(testContext, client, ts.URL)
	c.Assert(err, gc.ErrorMatches,
		fmt.Sprintf(`Get http://.*/discharge/info: unexpected content type text/plain; want application/json; content: BADJSON`))
}

func (s *KeyringSuite) TestThirdPartyInfoForLocationReturnsStatusInternalServerError(c *gc.C) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	client := httpbakery.NewHTTPClient()
	_, err := httpbakery.ThirdPartyInfoForLocation(testContext, client, ts.URL)
	c.Assert(err, gc.ErrorMatches, `Get .*/discharge/info: cannot unmarshal error response \(status 500 Internal Server Error\): unexpected content type .*`)
}

func (s *KeyringSuite) TestThirdPartyInfoForLocationFallbackToOldVersion(c *gc.C) {
	// Start a bakerytest discharger so we benefit from its TLS-verification-skip logic.
	d := bakerytest.NewDischarger(nil)
	defer d.Close()

	key, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)

	// Start a server which serves the publickey endpoint only.
	mux := http.NewServeMux()
	server := httptest.NewTLSServer(mux)
	mux.HandleFunc("/publickey", func(w http.ResponseWriter, req *http.Request) {
		c.Check(req.Method, gc.Equals, "GET")
		httprequest.WriteJSON(w, http.StatusOK, &httpbakery.PublicKeyResponse{
			PublicKey: &key.Public,
		})
	})
	info, err := httpbakery.ThirdPartyInfoForLocation(testContext, httpbakery.NewHTTPClient(), server.URL)
	c.Assert(err, gc.IsNil)
	expectedInfo := bakery.ThirdPartyInfo{
		PublicKey: key.Public,
		Version:   bakery.Version1,
	}
	c.Assert(info, jc.DeepEquals, expectedInfo)
}

type errorTransport struct{}

func (errorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, errgo.New("custom round trip error")
}
