package httpbakery_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync"
	"testing"

	qt "github.com/frankban/quicktest"
	"gopkg.in/errgo.v1"
	"gopkg.in/httprequest.v1"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/bakerytest"
	"gopkg.in/macaroon-bakery.v2/httpbakery"
)

func TestCachePrepopulated(t *testing.T) {
	c := qt.New(t)
	cache := bakery.NewThirdPartyStore()
	key, err := bakery.GenerateKey()
	c.Assert(err, qt.IsNil)
	expectInfo := bakery.ThirdPartyInfo{
		PublicKey: key.Public,
		Version:   bakery.LatestVersion,
	}
	cache.AddInfo("https://0.1.2.3/", expectInfo)
	kr := httpbakery.NewThirdPartyLocator(nil, cache)
	info, err := kr.ThirdPartyInfo(testContext, "https://0.1.2.3/")
	c.Assert(err, qt.IsNil)
	c.Assert(info, qt.DeepEquals, expectInfo)
}

func TestCachePrepopulatedInsecure(t *testing.T) {
	c := qt.New(t)
	// We allow an insecure URL in a prepopulated cache.
	cache := bakery.NewThirdPartyStore()
	key, err := bakery.GenerateKey()
	c.Assert(err, qt.Equals, nil)
	expectInfo := bakery.ThirdPartyInfo{
		PublicKey: key.Public,
		Version:   bakery.LatestVersion,
	}
	cache.AddInfo("http://0.1.2.3/", expectInfo)
	kr := httpbakery.NewThirdPartyLocator(nil, cache)
	info, err := kr.ThirdPartyInfo(testContext, "http://0.1.2.3/")
	c.Assert(err, qt.Equals, nil)
	c.Assert(info, qt.DeepEquals, expectInfo)
}

func TestCacheMiss(t *testing.T) {
	c := qt.New(t)
	d := bakerytest.NewDischarger(nil)
	defer d.Close()
	kr := httpbakery.NewThirdPartyLocator(nil, nil)

	expectInfo := bakery.ThirdPartyInfo{
		PublicKey: d.Key.Public,
		Version:   bakery.LatestVersion,
	}
	location := d.Location()
	info, err := kr.ThirdPartyInfo(testContext, location)
	c.Assert(err, qt.IsNil)
	c.Assert(info, qt.DeepEquals, expectInfo)

	// Close down the service and make sure that
	// the key is cached.
	d.Close()

	info, err = kr.ThirdPartyInfo(testContext, location)
	c.Assert(err, qt.IsNil)
	c.Assert(info, qt.DeepEquals, expectInfo)
}

func TestInsecureURL(t *testing.T) {
	c := qt.New(t)
	// Set up a discharger with an non-HTTPS access point.
	d := bakerytest.NewDischarger(nil)
	defer d.Close()
	httpsDischargeURL, err := url.Parse(d.Location())
	c.Assert(err, qt.IsNil)

	srv := httptest.NewServer(httputil.NewSingleHostReverseProxy(httpsDischargeURL))
	defer srv.Close()

	// Check that we are refused because it's an insecure URL.
	kr := httpbakery.NewThirdPartyLocator(nil, nil)
	info, err := kr.ThirdPartyInfo(testContext, srv.URL)
	c.Assert(err, qt.ErrorMatches, `untrusted discharge URL "http://.*"`)
	c.Assert(info, qt.DeepEquals, bakery.ThirdPartyInfo{})

	// Check that it does work when we've enabled AllowInsecure.
	kr.AllowInsecure()
	info, err = kr.ThirdPartyInfo(testContext, srv.URL)
	c.Assert(err, qt.IsNil)
	c.Assert(info, qt.DeepEquals, bakery.ThirdPartyInfo{
		PublicKey: d.Key.Public,
		Version:   bakery.LatestVersion,
	})
}

func TestConcurrentThirdPartyInfo(t *testing.T) {
	c := qt.New(t)
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
			c.Check(err, qt.IsNil)
			defer wg.Done()
		}()
	}
	wg.Wait()
}

func TestCustomHTTPClient(t *testing.T) {
	c := qt.New(t)
	client := &http.Client{
		Transport: errorTransport{},
	}
	kr := httpbakery.NewThirdPartyLocator(client, nil)
	info, err := kr.ThirdPartyInfo(testContext, "https://0.1.2.3/")
	c.Assert(err, qt.ErrorMatches, `(Get|GET) "?https://0.1.2.3/discharge/info"?: custom round trip error`)
	c.Assert(info, qt.DeepEquals, bakery.ThirdPartyInfo{})
}

func TestThirdPartyInfoForLocation(t *testing.T) {
	c := qt.New(t)
	d := bakerytest.NewDischarger(nil)
	defer d.Close()
	client := httpbakery.NewHTTPClient()
	info, err := httpbakery.ThirdPartyInfoForLocation(testContext, client, d.Location())
	c.Assert(err, qt.IsNil)
	expectedInfo := bakery.ThirdPartyInfo{
		PublicKey: d.Key.Public,
		Version:   bakery.LatestVersion,
	}
	c.Assert(info, qt.DeepEquals, expectedInfo)

	// Check that it works with client==nil.
	info, err = httpbakery.ThirdPartyInfoForLocation(testContext, nil, d.Location())
	c.Assert(err, qt.IsNil)
	c.Assert(info, qt.DeepEquals, expectedInfo)
}

func TestThirdPartyInfoForLocationWrongURL(t *testing.T) {
	c := qt.New(t)
	client := httpbakery.NewHTTPClient()
	_, err := httpbakery.ThirdPartyInfoForLocation(testContext, client, "http://localhost:0")
	c.Logf("%v", errgo.Details(err))
	c.Assert(err, qt.ErrorMatches,
		`(Get|GET) "?http://localhost:0/discharge/info"?: dial tcp 127.0.0.1:0: .*connection refused`)
}

func TestThirdPartyInfoForLocationReturnsInvalidJSON(t *testing.T) {
	c := qt.New(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "BADJSON")
	}))
	defer ts.Close()
	client := httpbakery.NewHTTPClient()
	_, err := httpbakery.ThirdPartyInfoForLocation(testContext, client, ts.URL)
	c.Assert(err, qt.ErrorMatches,
		fmt.Sprintf(`Get http://.*/discharge/info: unexpected content type text/plain; want application/json; content: BADJSON`))
}

func TestThirdPartyInfoForLocationReturnsStatusInternalServerError(t *testing.T) {
	c := qt.New(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	client := httpbakery.NewHTTPClient()
	_, err := httpbakery.ThirdPartyInfoForLocation(testContext, client, ts.URL)
	c.Assert(err, qt.ErrorMatches, `Get .*/discharge/info: cannot unmarshal error response \(status 500 Internal Server Error\): unexpected content type .*`)
}

func TestThirdPartyInfoForLocationFallbackToOldVersion(t *testing.T) {
	c := qt.New(t)
	// Start a bakerytest discharger so we benefit from its TLS-verification-skip logic.
	d := bakerytest.NewDischarger(nil)
	defer d.Close()

	key, err := bakery.GenerateKey()
	c.Assert(err, qt.IsNil)

	// Start a server which serves the publickey endpoint only.
	mux := http.NewServeMux()
	server := httptest.NewTLSServer(mux)
	mux.HandleFunc("/publickey", func(w http.ResponseWriter, req *http.Request) {
		c.Check(req.Method, qt.Equals, "GET")
		httprequest.WriteJSON(w, http.StatusOK, &httpbakery.PublicKeyResponse{
			PublicKey: &key.Public,
		})
	})
	info, err := httpbakery.ThirdPartyInfoForLocation(testContext, httpbakery.NewHTTPClient(), server.URL)
	c.Assert(err, qt.IsNil)
	expectedInfo := bakery.ThirdPartyInfo{
		PublicKey: key.Public,
		Version:   bakery.Version1,
	}
	c.Assert(info, qt.DeepEquals, expectedInfo)
}

type errorTransport struct{}

func (errorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, errgo.New("custom round trip error")
}
