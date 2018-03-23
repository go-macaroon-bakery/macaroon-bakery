package httpbakery_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"time"

	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	"golang.org/x/net/context"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/httprequest.v1"
	"gopkg.in/macaroon.v2"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2/bakerytest"
	"gopkg.in/macaroon-bakery.v2/httpbakery"
)

type ClientSuite struct {
	jujutesting.LoggingSuite
}

var _ = gc.Suite(&ClientSuite{})

var (
	testOp      = bakery.Op{"test", "test"}
	testContext = context.Background()
)

// TestSingleServiceFirstParty creates a single service
// with a macaroon with one first party caveat.
// It creates a request with this macaroon and checks that the service
// can verify this macaroon as valid.
func (s *ClientSuite) TestSingleServiceFirstParty(c *gc.C) {
	// Create a target service.
	b := newBakery("loc", nil, nil)
	// No discharge required, so pass "unknown" for the third party
	// caveat discharger location so we know that we don't try
	// to discharge the location.
	ts := httptest.NewServer(serverHandler(serverHandlerParams{
		bakery:       b,
		authLocation: "unknown",
	}))
	defer ts.Close()

	// Mint a macaroon for the target service.
	serverMacaroon, err := b.Oven.NewMacaroon(testContext, bakery.LatestVersion, nil, testOp)
	c.Assert(err, gc.IsNil)
	c.Assert(serverMacaroon.M().Location(), gc.Equals, "loc")
	err = b.Oven.AddCaveat(testContext, serverMacaroon, isSomethingCaveat())
	c.Assert(err, gc.IsNil)

	// Create a client request.
	req, err := http.NewRequest("GET", ts.URL, nil)
	c.Assert(err, gc.IsNil)
	client := clientRequestWithCookies(c, ts.URL, macaroon.Slice{serverMacaroon.M()})
	// Somehow the client has accquired the macaroon. Add it to the cookiejar in our request.

	// Make the request to the server.
	resp, err := client.Do(req)
	c.Assert(err, gc.IsNil)
	defer resp.Body.Close()
	assertResponse(c, resp, "done")
}

func (s *ClientSuite) TestSingleServiceFirstPartyWithHeader(c *gc.C) {
	// Create a target service.
	b := newBakery("loc", nil, nil)
	// No discharge required, so pass "unknown" for the third party
	// caveat discharger location so we know that we don't try
	// to discharge the location.
	ts := httptest.NewServer(serverHandler(serverHandlerParams{
		bakery:       b,
		authLocation: "unknown",
	}))
	defer ts.Close()

	// Mint a macaroon for the target service.
	serverMacaroon, err := b.Oven.NewMacaroon(testContext, bakery.LatestVersion, nil, testOp)
	c.Assert(err, gc.IsNil)
	c.Assert(serverMacaroon.M().Location(), gc.Equals, "loc")
	err = b.Oven.AddCaveat(testContext, serverMacaroon, isSomethingCaveat())
	c.Assert(err, gc.IsNil)

	// Serialize the macaroon slice.
	data, err := json.Marshal(macaroon.Slice{serverMacaroon.M()})
	c.Assert(err, gc.IsNil)
	value := base64.StdEncoding.EncodeToString(data)

	// Create a client request.
	req, err := http.NewRequest("GET", ts.URL, nil)
	c.Assert(err, gc.IsNil)
	req.Header.Set(httpbakery.MacaroonsHeader, value)
	client := httpbakery.NewHTTPClient()

	// Make the request to the server.
	resp, err := client.Do(req)
	c.Assert(err, gc.IsNil)
	defer resp.Body.Close()
	assertResponse(c, resp, "done")
}

func (s *ClientSuite) TestRepeatedRequestWithBody(c *gc.C) {
	d := bakerytest.NewDischarger(nil)
	defer d.Close()

	// Create a target service.
	b := newBakery("loc", d, nil)

	ts := httptest.NewServer(serverHandler(serverHandlerParams{
		bakery:         b,
		authLocation:   d.Location(),
		alwaysReadBody: true,
	}))
	defer ts.Close()

	// Try with no authorization, to make sure that httpbakery.Do
	// really will retry the request.

	bodyText := "postbody"
	bodyReader := &readCounter{ReadSeeker: strings.NewReader(bodyText)}

	req, err := http.NewRequest("POST", ts.URL, bodyReader)
	c.Assert(err, gc.IsNil)

	resp, err := httpbakery.NewClient().Do(req)
	c.Assert(err, gc.IsNil)
	defer resp.Body.Close()
	assertResponse(c, resp, "done postbody")

	// Sanity check that the body really was read twice and hence
	// that we are checking the logic we intend to check.
	c.Assert(bodyReader.byteCount, gc.Equals, len(bodyText)*2)
}

func (s ClientSuite) TestWithLargeBody(c *gc.C) {
	// This test is designed to fail when run with the race
	// checker enabled and when go issue #12796
	// is not fixed.

	d := bakerytest.NewDischarger(nil)
	defer d.Close()

	// Create a target service.
	b := newBakery("loc", d, nil)

	ts := httptest.NewServer(serverHandler(serverHandlerParams{
		bakery:       b,
		authLocation: d.Location(),
	}))
	defer ts.Close()

	// Create a client request.
	req, err := http.NewRequest("POST", ts.URL+"/no-body", &largeReader{total: 3 * 1024 * 1024})
	c.Assert(err, gc.IsNil)

	resp, err := httpbakery.NewClient().Do(req)
	c.Assert(err, gc.IsNil)
	resp.Body.Close()

	c.Assert(resp.StatusCode, gc.Equals, http.StatusOK)
}

// largeReader implements a reader that produces up to total bytes
// in 1 byte reads.
type largeReader struct {
	total int
	n     int
}

func (r *largeReader) Read(buf []byte) (int, error) {
	if r.n >= r.total {
		return 0, io.EOF
	}
	r.n++
	return copy(buf, []byte("a")), nil
}

func (r *largeReader) Seek(offset int64, whence int) (int64, error) {
	if offset != 0 || whence != 0 {
		panic("unexpected seek")
	}
	r.n = 0
	return 0, nil
}

func (r *largeReader) Close() error {
	// By setting n to zero, we ensure that if there's
	// a concurrent read, it will also read from n
	// and so the race detector should pick up the
	// problem.
	r.n = 0
	return nil
}

func (s *ClientSuite) TestDischargeServerWithBinaryCaveatId(c *gc.C) {
	assertDischargeServerDischargesConditionForVersion(c, "\xff\x00\x89", bakery.Version2)
}

func (s *ClientSuite) TestDischargeServerWithStringCaveatId(c *gc.C) {
	assertDischargeServerDischargesConditionForVersion(c, "foo", bakery.Version1)
}

func assertDischargeServerDischargesConditionForVersion(c *gc.C, cond string, version bakery.Version) {
	called := 0
	checker := func(ctx context.Context, p httpbakery.ThirdPartyCaveatCheckerParams) ([]checkers.Caveat, error) {
		called++
		c.Check(string(p.Caveat.Condition), gc.Equals, cond)
		return nil, nil
	}
	discharger := bakerytest.NewDischarger(nil)
	discharger.CheckerP = httpbakery.ThirdPartyCaveatCheckerPFunc(checker)

	bKey, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)

	m, err := bakery.NewMacaroon([]byte("root key"), []byte("id"), "location", version, nil)
	c.Assert(err, gc.IsNil)
	err = m.AddCaveat(context.TODO(), checkers.Caveat{
		Location:  discharger.Location(),
		Condition: cond,
	}, bKey, discharger)
	c.Assert(err, gc.IsNil)
	client := httpbakery.NewClient()
	ms, err := client.DischargeAll(context.TODO(), m)
	c.Assert(err, gc.IsNil)
	c.Check(ms, gc.HasLen, 2)
	c.Check(called, gc.Equals, 1)
}

func (s *ClientSuite) TestDoClosesBody(c *gc.C) {
	cn := closeNotifier{
		closed: make(chan struct{}),
	}
	req, err := http.NewRequest("GET", "http://0.1.2.3/", cn)
	c.Assert(err, gc.IsNil)

	_, err = httpbakery.NewClient().Do(req)
	c.Assert(err, gc.NotNil)

	select {
	case <-cn.closed:
	case <-time.After(5 * time.Second):
		c.Fatalf("timed out waiting for request body to be closed")
	}
}

func (s *ClientSuite) TestWithNonSeekableBody(c *gc.C) {
	r := bytes.NewBufferString("hello")
	req, err := http.NewRequest("GET", "http://0.1.2.3/", r)
	c.Assert(err, gc.IsNil)
	_, err = httpbakery.NewClient().Do(req)
	c.Assert(err, gc.ErrorMatches, `request body is not seekable`)
}

func (s *ClientSuite) TestWithNonSeekableCloserBody(c *gc.C) {
	req, err := http.NewRequest("GET", "http://0.1.2.3/", readCloser{})
	c.Assert(err, gc.IsNil)
	_, err = httpbakery.NewClient().Do(req)
	c.Assert(err, gc.ErrorMatches, `request body is not seekable`)
}

type readCloser struct {
}

func (r readCloser) Read(buf []byte) (int, error) {
	return 0, io.EOF
}

func (r readCloser) Close() error {
	return nil
}

type closeNotifier struct {
	closed chan struct{}
}

func (r closeNotifier) Read(buf []byte) (int, error) {
	return 0, io.EOF
}

func (r closeNotifier) Seek(offset int64, whence int) (int64, error) {
	return 0, nil
}

func (r closeNotifier) Close() error {
	close(r.closed)
	return nil
}

func (s *ClientSuite) TestDischargeServerWithMacaraqOnDischarge(c *gc.C) {
	locator := bakery.NewThirdPartyStore()

	var called [3]int

	// create the services from leaf discharger to primary
	// service so that each one can know the location
	// to discharge at.
	db1 := newBakery("loc", locator, nil)
	key2, h2 := newHTTPDischarger(db1, httpbakery.ThirdPartyCaveatCheckerPFunc(func(ctx context.Context, p httpbakery.ThirdPartyCaveatCheckerParams) ([]checkers.Caveat, error) {
		called[2]++
		if string(p.Caveat.Condition) != "is-ok" {
			return nil, fmt.Errorf("unrecognized caveat at srv2")
		}
		return nil, nil
	}))
	srv2 := httptest.NewServer(h2)
	defer srv2.Close()
	locator.AddInfo(srv2.URL, bakery.ThirdPartyInfo{
		PublicKey: key2,
		Version:   bakery.LatestVersion,
	})

	db2 := newBakery("loc", locator, nil)
	key1, h1 := newHTTPDischarger(db2, httpbakery.ThirdPartyCaveatCheckerPFunc(func(ctx context.Context, p httpbakery.ThirdPartyCaveatCheckerParams) ([]checkers.Caveat, error) {
		called[1]++
		if _, err := db2.Checker.Auth(httpbakery.RequestMacaroons(p.Request)...).Allow(testContext, testOp); err != nil {
			c.Logf("returning discharge required error")
			return nil, newDischargeRequiredError(serverHandlerParams{
				bakery:       db2,
				authLocation: srv2.URL,
			}, err, p.Request)
		}
		if string(p.Caveat.Condition) != "is-ok" {
			return nil, fmt.Errorf("unrecognized caveat at srv1")
		}
		return nil, nil
	}))
	srv1 := httptest.NewServer(h1)
	defer srv1.Close()
	locator.AddInfo(srv1.URL, bakery.ThirdPartyInfo{
		PublicKey: key1,
		Version:   bakery.LatestVersion,
	})

	b0 := newBakery("loc", locator, nil)
	srv0 := httptest.NewServer(serverHandler(serverHandlerParams{
		bakery:       b0,
		authLocation: srv1.URL,
	}))
	defer srv0.Close()

	// Make a client request.
	client := httpbakery.NewClient()
	req, err := http.NewRequest("GET", srv0.URL, nil)
	c.Assert(err, gc.IsNil)
	resp, err := client.Do(req)
	c.Assert(err, gc.IsNil)
	defer resp.Body.Close()
	assertResponse(c, resp, "done")

	c.Assert(called, gc.DeepEquals, [3]int{0, 2, 1})
}

func (s *ClientSuite) TestTwoDischargesRequired(c *gc.C) {
	// Sometimes the first discharge won't be enough and we'll
	// need to discharge another one to get through another
	// layer of security.

	dischargeCount := 0
	checker := func(ctx context.Context, p httpbakery.ThirdPartyCaveatCheckerParams) ([]checkers.Caveat, error) {
		c.Check(string(p.Caveat.Condition), gc.Equals, "is-ok")
		dischargeCount++
		return nil, nil
	}
	discharger := bakerytest.NewDischarger(nil)
	discharger.CheckerP = httpbakery.ThirdPartyCaveatCheckerPFunc(checker)

	srv := s.serverRequiringMultipleDischarges(httpbakery.MaxDischargeRetries, discharger)
	defer srv.Close()

	// Create a client request.
	req, err := http.NewRequest("GET", srv.URL, nil)
	c.Assert(err, gc.IsNil)

	resp, err := httpbakery.NewClient().Do(req)
	c.Assert(err, gc.IsNil)
	defer resp.Body.Close()
	c.Assert(resp.StatusCode, gc.Equals, http.StatusOK)
	data, err := ioutil.ReadAll(resp.Body)
	c.Assert(err, gc.IsNil)
	c.Assert(string(data), gc.Equals, "ok")
	c.Assert(dischargeCount, gc.Equals, httpbakery.MaxDischargeRetries)
}

func (s *ClientSuite) TestTooManyDischargesRequired(c *gc.C) {
	checker := func(context.Context, httpbakery.ThirdPartyCaveatCheckerParams) ([]checkers.Caveat, error) {
		return nil, nil
	}
	discharger := bakerytest.NewDischarger(nil)
	discharger.CheckerP = httpbakery.ThirdPartyCaveatCheckerPFunc(checker)

	srv := s.serverRequiringMultipleDischarges(httpbakery.MaxDischargeRetries+1, discharger)
	defer srv.Close()

	// Create a client request.
	req, err := http.NewRequest("GET", srv.URL, nil)
	c.Assert(err, gc.IsNil)

	_, err = httpbakery.NewClient().Do(req)
	c.Assert(err, gc.ErrorMatches, `too many \(3\) discharge requests: foo`)
}

// multiDischargeServer returns a server that will require multiple
// discharges when accessing its endpoints. The parameter
// holds the total number of discharges that will be required.
func (s *ClientSuite) serverRequiringMultipleDischarges(n int, discharger *bakerytest.Discharger) *httptest.Server {
	b := newBakery("loc", discharger, nil)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if hasDuplicateCookies(req) {
			panic(errgo.Newf("duplicate cookie names in request; cookies %s", req.Header["Cookie"]))
		}
		if _, err := b.Checker.Auth(httpbakery.RequestMacaroons(req)...).Allow(context.TODO(), testOp); err == nil {
			w.Write([]byte("ok"))
			return
		}
		caveats := []checkers.Caveat{{
			Location:  discharger.Location(),
			Condition: "is-ok",
		}}
		if n--; n > 0 {
			// We've got more attempts to go, so add a first party caveat that
			// will cause the macaroon to fail verification and so trigger
			// another discharge-required error.
			caveats = append(caveats, checkers.Caveat{
				Condition: fmt.Sprintf("error %d attempts left", n),
			})
		}
		m, err := b.Oven.NewMacaroon(context.TODO(), bakery.LatestVersion, caveats, testOp)
		if err != nil {
			panic(fmt.Errorf("cannot make new macaroon: %v", err))
		}
		err = httpbakery.NewDischargeRequiredError(httpbakery.DischargeRequiredErrorParams{
			OriginalError:    errgo.New("foo"),
			Macaroon:         m,
			CookieNameSuffix: fmt.Sprintf("auth%d", n),
		})
		httpbakery.WriteError(testContext, w, err)
	}))
}

func hasDuplicateCookies(req *http.Request) bool {
	names := make(map[string]bool)
	for _, cookie := range req.Cookies() {
		if names[cookie.Name] {
			return true
		}
		names[cookie.Name] = true
	}
	return false
}

func (s *ClientSuite) TestVersion0Generates407Status(c *gc.C) {
	m, err := bakery.NewMacaroon([]byte("root key"), []byte("id"), "location", bakery.Version0, nil)
	c.Assert(err, gc.IsNil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		err := httpbakery.NewDischargeRequiredError(httpbakery.DischargeRequiredErrorParams{
			Macaroon: m,
		})
		httpbakery.WriteError(testContext, w, err)
	}))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	c.Assert(err, gc.IsNil)
	c.Assert(resp.StatusCode, gc.Equals, http.StatusProxyAuthRequired)
}

func (s *ClientSuite) TestVersion1Generates401Status(c *gc.C) {
	m, err := bakery.NewMacaroon([]byte("root key"), []byte("id"), "location", bakery.Version1, nil)
	c.Assert(err, gc.IsNil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		err := httpbakery.NewDischargeRequiredError(httpbakery.DischargeRequiredErrorParams{
			Macaroon: m,
		})
		httpbakery.WriteError(testContext, w, err)
	}))
	defer srv.Close()

	req, err := http.NewRequest("GET", srv.URL, nil)
	c.Assert(err, gc.IsNil)
	req.Header.Set(httpbakery.BakeryProtocolHeader, "1")
	resp, err := http.DefaultClient.Do(req)
	c.Assert(err, gc.IsNil)
	c.Assert(resp.StatusCode, gc.Equals, http.StatusUnauthorized)
	c.Assert(resp.Header.Get("WWW-Authenticate"), gc.Equals, "Macaroon")
}

func newHTTPDischarger(b *bakery.Bakery, checker httpbakery.ThirdPartyCaveatCheckerP) (bakery.PublicKey, http.Handler) {
	mux := http.NewServeMux()

	d := httpbakery.NewDischarger(httpbakery.DischargerParams{
		CheckerP: checker,
		Key:      b.Oven.Key(),
	})
	d.AddMuxHandlers(mux, "/")
	return b.Oven.Key().Public, mux
}

func (s *ClientSuite) TestMacaroonCookieName(c *gc.C) {
	d := bakerytest.NewDischarger(nil)
	defer d.Close()

	checked := make(map[string]bool)
	checker := checkers.New(nil)
	checker.Namespace().Register("testns", "")
	checker.Register("once", "testns", func(ctx context.Context, _, arg string) error {
		if checked[arg] {
			return errgo.Newf("caveat %q has already been checked once", arg)
		}
		checked[arg] = true
		return nil
	})

	b := newBakery("loc", nil, checker)

	// We arrange things so that although we use the same client
	// (with the same cookie jar), the macaroon verification only
	// succeeds once, so the client always fetches a new macaroon.

	caveatSeq := 0
	cookieName := ""
	ts := httptest.NewServer(serverHandler(serverHandlerParams{
		bakery: b,
		mutateError: func(e *httpbakery.Error) {
			e.Info.CookieNameSuffix = cookieName
			e.Info.MacaroonPath = "/"
		},
		caveats: func() []checkers.Caveat {
			caveatSeq++
			return []checkers.Caveat{{
				Condition: fmt.Sprintf("once %d", caveatSeq),
			}}
		},
	}))
	defer ts.Close()

	client := httpbakery.NewClient()
	doRequest := func() {
		req, err := http.NewRequest("GET", ts.URL+"/foo/bar/", nil)
		c.Assert(err, gc.IsNil)
		resp, err := client.Do(req)
		c.Assert(err, gc.IsNil)
		assertResponse(c, resp, "done")
	}
	assertCookieNames := func(names ...string) {
		u, err := url.Parse(ts.URL)
		c.Assert(err, gc.IsNil)
		sort.Strings(names)
		var gotNames []string
		for _, c := range client.Jar.Cookies(u) {
			gotNames = append(gotNames, c.Name)
		}
		sort.Strings(gotNames)
		c.Assert(gotNames, jc.DeepEquals, names)
	}
	cookieName = "foo"
	doRequest()
	assertCookieNames("macaroon-foo")

	// Another request with the same cookie name should
	// overwrite the old cookie.
	doRequest()
	assertCookieNames("macaroon-foo")

	// A subsequent request with a different cookie name
	// should create a new cookie, but the old one will still
	// be around.
	cookieName = "bar"
	doRequest()
	assertCookieNames("macaroon-foo", "macaroon-bar")
}

func (s *ClientSuite) TestMacaroonCookiePath(c *gc.C) {
	b := newBakery("loc", nil, nil)

	cookiePath := ""
	ts := httptest.NewServer(serverHandler(serverHandlerParams{
		bakery: b,
		mutateError: func(e *httpbakery.Error) {
			e.Info.MacaroonPath = cookiePath
		},
	}))
	defer ts.Close()

	var client *httpbakery.Client
	doRequest := func() {
		req, err := http.NewRequest("GET", ts.URL+"/foo/bar/", nil)
		c.Assert(err, gc.IsNil)
		client = httpbakery.NewClient()
		resp, err := client.Do(req)
		c.Assert(err, gc.IsNil)
		assertResponse(c, resp, "done")
	}
	assertCookieCount := func(path string, n int) {
		u, err := url.Parse(ts.URL + path)
		c.Assert(err, gc.IsNil)
		c.Assert(client.Jar.Cookies(u), gc.HasLen, n)
	}
	cookiePath = ""
	c.Logf("- cookie path %q", cookiePath)
	doRequest()
	assertCookieCount("", 0)
	assertCookieCount("/foo", 0)
	assertCookieCount("/foo", 0)
	assertCookieCount("/foo/", 0)
	assertCookieCount("/foo/bar/", 1)
	assertCookieCount("/foo/bar/baz", 1)

	cookiePath = "/foo/"
	c.Logf("- cookie path %q", cookiePath)
	doRequest()
	assertCookieCount("", 0)
	assertCookieCount("/foo", 0)
	assertCookieCount("/foo/", 1)
	assertCookieCount("/foo/bar/", 1)
	assertCookieCount("/foo/bar/baz", 1)

	cookiePath = "/foo"
	c.Logf("- cookie path %q", cookiePath)
	doRequest()
	assertCookieCount("", 0)
	assertCookieCount("/bar", 0)
	assertCookieCount("/foo", 1)
	assertCookieCount("/foo/", 1)
	assertCookieCount("/foo/bar/", 1)
	assertCookieCount("/foo/bar/baz", 1)

	cookiePath = "../"
	c.Logf("- cookie path %q", cookiePath)
	doRequest()
	assertCookieCount("", 0)
	assertCookieCount("/bar", 0)
	assertCookieCount("/foo", 0)
	assertCookieCount("/foo/", 1)
	assertCookieCount("/foo/bar/", 1)
	assertCookieCount("/foo/bar/baz", 1)

	cookiePath = "../bar"
	c.Logf("- cookie path %q", cookiePath)
	doRequest()
	assertCookieCount("", 0)
	assertCookieCount("/bar", 0)
	assertCookieCount("/foo", 0)
	assertCookieCount("/foo/", 0)
	assertCookieCount("/foo/bar/", 1)
	assertCookieCount("/foo/bar/baz", 1)
	assertCookieCount("/foo/baz", 0)
	assertCookieCount("/foo/baz/", 0)
	assertCookieCount("/foo/baz/bar", 0)

	cookiePath = "/"
	c.Logf("- cookie path %q", cookiePath)
	doRequest()
	assertCookieCount("", 1)
	assertCookieCount("/bar", 1)
	assertCookieCount("/foo", 1)
	assertCookieCount("/foo/", 1)
	assertCookieCount("/foo/bar/", 1)
	assertCookieCount("/foo/bar/baz", 1)
}

func (s *ClientSuite) TestThirdPartyDischargeRefused(c *gc.C) {
	d := bakerytest.NewDischarger(nil)
	d.CheckerP = bakerytest.ConditionParser(func(cond, arg string) ([]checkers.Caveat, error) {
		return nil, errgo.New("boo! cond " + cond)
	})
	defer d.Close()

	// Create a target service.
	b := newBakery("loc", d, nil)

	ts := httptest.NewServer(serverHandler(serverHandlerParams{
		bakery:       b,
		authLocation: d.Location(),
	}))
	defer ts.Close()

	// Create a client request.
	req, err := http.NewRequest("GET", ts.URL, nil)
	c.Assert(err, gc.IsNil)

	client := httpbakery.NewClient()

	// Make the request to the server.
	resp, err := client.Do(req)
	c.Assert(errgo.Cause(err), gc.FitsTypeOf, (*httpbakery.DischargeError)(nil))
	c.Assert(err, gc.ErrorMatches, `cannot get discharge from ".*": third party refused discharge: cannot discharge: boo! cond is-ok`)
	c.Assert(resp, gc.IsNil)
}

func (s *ClientSuite) TestDischargeWithInteractionRequiredError(c *gc.C) {
	d := bakerytest.NewDischarger(nil)
	defer d.Close()
	d.CheckerP = bakerytest.ConditionParser(func(cond, arg string) ([]checkers.Caveat, error) {
		return nil, &httpbakery.Error{
			Code:    httpbakery.ErrInteractionRequired,
			Message: "interaction required",
			Info: &httpbakery.ErrorInfo{
				LegacyVisitURL: "http://0.1.2.3/",
				LegacyWaitURL:  "http://0.1.2.3/",
			},
		}
	})

	// Create a target service.
	b := newBakery("loc", d, nil)

	ts := httptest.NewServer(serverHandler(serverHandlerParams{
		bakery:       b,
		authLocation: d.Location(),
	}))
	defer ts.Close()

	// Create a client request.
	req, err := http.NewRequest("GET", ts.URL, nil)
	c.Assert(err, gc.IsNil)

	errCannotVisit := errgo.New("cannot visit")
	client := httpbakery.NewClient()
	client.AddInteractor(legacyInteractor{
		kind: httpbakery.WebBrowserInteractionKind,
		legacyInteract: func(ctx context.Context, client *httpbakery.Client, location string, visitURL *url.URL) error {
			return errCannotVisit
		},
	})

	// Make the request to the server.
	resp, err := client.Do(req)
	c.Assert(err, gc.ErrorMatches, `cannot get discharge from "https://.*": cannot start interactive session: cannot visit`)
	c.Assert(httpbakery.IsInteractionError(errgo.Cause(err)), gc.Equals, true)
	ierr, ok := errgo.Cause(err).(*httpbakery.InteractionError)
	c.Assert(ok, gc.Equals, true)
	c.Assert(errgo.Cause(ierr.Reason), gc.Equals, errCannotVisit)
	c.Assert(resp, gc.IsNil)
}

var interactionRequiredMethodsTests = []struct {
	about               string
	methods             map[string]interface{}
	interactors         []httpbakery.Interactor
	expectInteractCalls int
	expectMethod        string
	expectError         string
}{{
	about: "single method",
	methods: map[string]interface{}{
		"test-interactor": "interaction-data",
	},
	interactors: []httpbakery.Interactor{
		testInteractor("test-interactor"),
	},
	expectInteractCalls: 1,
	expectMethod:        "test-interactor",
}, {
	about: "two methods, first one not used",
	methods: map[string]interface{}{
		"test-interactor": "interaction-data",
	},
	interactors: []httpbakery.Interactor{
		testInteractor("other-interactor"),
		testInteractor("test-interactor"),
	},
	expectInteractCalls: 1,
	expectMethod:        "test-interactor",
}, {
	about: "two methods, first one takes precedence",
	methods: map[string]interface{}{
		"test-interactor":  "interaction-data",
		"other-interactor": "other-data",
	},
	interactors: []httpbakery.Interactor{
		testInteractor("other-interactor"),
		testInteractor("test-interactor"),
	},
	expectInteractCalls: 1,
	expectMethod:        "other-interactor",
}, {
	about: "two methods, first one takes precedence",
	methods: map[string]interface{}{
		"test-interactor":  "interaction-data",
		"other-interactor": "other-data",
	},
	interactors: []httpbakery.Interactor{
		testInteractor("test-interactor"),
		testInteractor("other-interactor"),
	},
	expectInteractCalls: 1,
	expectMethod:        "test-interactor",
}, {
	about: "two methods, first one returns ErrInteractionMethodNotFound",
	methods: map[string]interface{}{
		"test-interactor":  "interaction-data",
		"other-interactor": "other-data",
	},
	interactors: []httpbakery.Interactor{
		interactor{
			kind: "test-interactor",
			interact: func(ctx context.Context, client *httpbakery.Client, location string, interactionRequiredErr *httpbakery.Error) (*httpbakery.DischargeToken, error) {
				return nil, errgo.WithCausef(nil, httpbakery.ErrInteractionMethodNotFound, "")
			},
		},
		testInteractor("other-interactor"),
	},
	expectInteractCalls: 2,
	expectMethod:        "other-interactor",
}, {
	about: "interactor returns error",
	methods: map[string]interface{}{
		"test-interactor":  "interaction-data",
		"other-interactor": "other-data",
	},
	interactors: []httpbakery.Interactor{
		interactor{
			kind: "test-interactor",
			interact: func(ctx context.Context, client *httpbakery.Client, location string, interactionRequiredErr *httpbakery.Error) (*httpbakery.DischargeToken, error) {
				return nil, errgo.New("an error")
			},
		},
		testInteractor("other-interactor"),
	},
	expectInteractCalls: 1,
	expectError:         `cannot get discharge from "https://.*": an error`,
}, {
	about: "no supported methods",
	methods: map[string]interface{}{
		"a-interactor": "interaction-data",
		"b-interactor": "other-data",
	},
	interactors: []httpbakery.Interactor{
		testInteractor("c-interactor"),
		testInteractor("d-interactor"),
	},
	expectError: `cannot get discharge from "https://.*": cannot start interactive session: no supported interaction method`,
}, {
	about: "interactor returns nil token",
	methods: map[string]interface{}{
		"test-interactor": "interaction-data",
	},
	interactors: []httpbakery.Interactor{
		interactor{
			kind: "test-interactor",
			interact: func(ctx context.Context, client *httpbakery.Client, location string, interactionRequiredErr *httpbakery.Error) (*httpbakery.DischargeToken, error) {
				return nil, nil
			},
		},
	},
	expectInteractCalls: 1,
	expectError:         `cannot get discharge from "https://.*": interaction method returned an empty token`,
}, {
	about: "no interaction methods",
	methods: map[string]interface{}{
		"test-interactor": "interaction-data",
	},
	expectError: `cannot get discharge from "https://.*": cannot start interactive session: interaction required but not possible`,
}}

func (s *ClientSuite) TestInteractionRequiredMethods(c *gc.C) {
	d := bakerytest.NewDischarger(nil)
	defer d.Close()
	checkedWithToken := 0
	checkedWithoutToken := 0
	interactionKind := ""
	var serverInteractionMethods map[string]interface{}
	d.CheckerP = httpbakery.ThirdPartyCaveatCheckerPFunc(func(ctx context.Context, p httpbakery.ThirdPartyCaveatCheckerParams) ([]checkers.Caveat, error) {
		if p.Token != nil {
			checkedWithToken++
			if p.Token.Kind != "test" {
				c.Errorf("invalid token value")
				return nil, errgo.Newf("unexpected token value")
			}
			interactionKind = string(p.Token.Value)
			return nil, nil
		}
		checkedWithoutToken++
		err := httpbakery.NewInteractionRequiredError(nil, p.Request)
		for key, val := range serverInteractionMethods {
			err.SetInteraction(key, val)
		}
		return nil, err
	})
	// Create a target service.
	b := newBakery("loc", d, nil)

	ts := httptest.NewServer(serverHandler(serverHandlerParams{
		bakery:       b,
		authLocation: d.Location(),
	}))
	defer ts.Close()

	for i, test := range interactionRequiredMethodsTests {
		c.Logf("\ntest %d: %s", i, test.about)
		interactCalls := 0
		checkedWithToken = 0
		checkedWithoutToken = 0
		interactionKind = ""
		client := httpbakery.NewClient()
		for _, in := range test.interactors {
			in := in
			client.AddInteractor(interactor{
				kind: in.Kind(),
				interact: func(ctx context.Context, client *httpbakery.Client, location string, interactionRequiredErr *httpbakery.Error) (*httpbakery.DischargeToken, error) {
					interactCalls++
					return in.Interact(ctx, client, location, interactionRequiredErr)
				},
			})
			c.Logf("added interactor %q", in.Kind())
		}
		serverInteractionMethods = test.methods
		// Make the request to the server.
		req, err := http.NewRequest("GET", ts.URL, nil)
		c.Assert(err, gc.IsNil)
		resp, err := client.Do(req)
		if test.expectError != "" {
			c.Assert(err, gc.ErrorMatches, test.expectError)
			c.Assert(resp, gc.IsNil)
			continue
		}
		c.Assert(err, gc.Equals, nil)
		assertResponse(c, resp, "done")
		c.Check(interactCalls, gc.Equals, test.expectInteractCalls)
		c.Check(checkedWithoutToken, gc.Equals, 1)
		c.Check(checkedWithToken, gc.Equals, 1)
		c.Check(interactionKind, gc.Equals, test.expectMethod)
	}
}

func testInteractor(kind string) httpbakery.Interactor {
	return interactor{
		kind: kind,
		interact: func(ctx context.Context, client *httpbakery.Client, location string, interactionRequiredErr *httpbakery.Error) (*httpbakery.DischargeToken, error) {
			return &httpbakery.DischargeToken{
				Kind:  "test",
				Value: []byte(kind),
			}, nil
		},
	}
}

var dischargeWithVisitURLErrorTests = []struct {
	about       string
	respond     func(http.ResponseWriter)
	expectError string
}{{
	about: "error message",
	respond: func(w http.ResponseWriter) {
		httpReqServer.WriteError(testContext, w, fmt.Errorf("an error"))
	},
	expectError: `cannot get discharge from ".*": failed to acquire macaroon after waiting: third party refused discharge: an error`,
}, {
	about: "non-JSON error",
	respond: func(w http.ResponseWriter) {
		w.Write([]byte("bad response"))
	},
	// TODO fix this unhelpful error message
	expectError: `cannot get discharge from ".*": cannot unmarshal wait response: unexpected content type text/plain; want application/json; content: bad response`,
}}

func (s *ClientSuite) TestDischargeWithVisitURLError(c *gc.C) {
	visitor := newVisitHandler(nil)
	visitSrv := httptest.NewServer(visitor)
	defer visitSrv.Close()

	d := bakerytest.NewDischarger(nil)
	d.CheckerP = bakerytest.ConditionParser(func(cond, arg string) ([]checkers.Caveat, error) {
		return nil, &httpbakery.Error{
			Code:    httpbakery.ErrInteractionRequired,
			Message: "interaction required",
			Info: &httpbakery.ErrorInfo{
				LegacyVisitURL: visitSrv.URL + "/visit",
				LegacyWaitURL:  visitSrv.URL + "/wait",
			},
		}
	})
	defer d.Close()

	// Create a target service.
	b := newBakery("loc", d, nil)
	ts := httptest.NewServer(serverHandler(serverHandlerParams{
		bakery:       b,
		authLocation: d.Location(),
	}))
	defer ts.Close()

	for i, test := range dischargeWithVisitURLErrorTests {
		c.Logf("test %d: %s", i, test.about)
		visitor.respond = test.respond

		client := httpbakery.NewClient()
		client.AddInteractor(legacyInteractor{
			kind: httpbakery.WebBrowserInteractionKind,
			legacyInteract: func(ctx context.Context, client *httpbakery.Client, location string, visitURL *url.URL) error {
				resp, err := http.Get(visitURL.String())
				if err != nil {
					return err
				}
				resp.Body.Close()
				return nil
			},
		})

		// Create a client request.
		req, err := http.NewRequest("GET", ts.URL, nil)
		c.Assert(err, gc.IsNil)

		// Make the request to the server.
		_, err = client.Do(req)
		c.Assert(err, gc.ErrorMatches, test.expectError)
	}
}

func (s *ClientSuite) TestMacaroonsForURL(c *gc.C) {
	// Create a target service.
	b := newBakery("loc", nil, nil)

	m1, err := b.Oven.NewMacaroon(testContext, bakery.LatestVersion, nil, testOp)
	c.Assert(err, gc.IsNil)
	m2, err := b.Oven.NewMacaroon(testContext, bakery.LatestVersion, nil, testOp)
	c.Assert(err, gc.IsNil)

	u1 := mustParseURL("http://0.1.2.3/")
	u2 := mustParseURL("http://0.1.2.3/x/")

	// Create some cookies with different cookie paths.
	jar, err := cookiejar.New(nil)
	c.Assert(err, gc.IsNil)
	httpbakery.SetCookie(jar, u1, nil, macaroon.Slice{m1.M()})
	httpbakery.SetCookie(jar, u2, nil, macaroon.Slice{m2.M()})
	jar.SetCookies(u1, []*http.Cookie{{
		Name:  "foo",
		Path:  "/",
		Value: "ignored",
	}, {
		Name:  "bar",
		Path:  "/x/",
		Value: "ignored",
	}})

	// Check that MacaroonsForURL behaves correctly
	// with both single and multiple cookies.

	mss := httpbakery.MacaroonsForURL(jar, u1)
	c.Assert(mss, gc.HasLen, 1)
	c.Assert(mss[0], gc.HasLen, 1)
	c.Assert(mss[0][0].Id(), jc.DeepEquals, m1.M().Id())

	mss = httpbakery.MacaroonsForURL(jar, u2)

	checked := make(map[string]int)
	for _, ms := range mss {
		checked[string(ms[0].Id())]++
		_, err := b.Checker.Auth(ms).Allow(testContext, testOp)
		c.Assert(err, gc.IsNil)
	}
	c.Assert(checked, jc.DeepEquals, map[string]int{
		string(m1.M().Id()): 1,
		string(m2.M().Id()): 1,
	})
}

func (s *ClientSuite) TestDoWithCustomError(c *gc.C) {
	d := bakerytest.NewDischarger(nil)
	defer d.Close()

	// Create a target service.
	b := newBakery("loc", d, nil)

	type customError struct {
		CustomError *httpbakery.Error
	}
	callCount := 0
	handler := func(w http.ResponseWriter, req *http.Request) {
		callCount++
		if _, err := b.Checker.Auth(httpbakery.RequestMacaroons(req)...).Allow(testContext, testOp); err != nil {
			httprequest.WriteJSON(w, http.StatusTeapot, customError{
				CustomError: newDischargeRequiredError(serverHandlerParams{
					bakery:       b,
					authLocation: d.Location(),
				}, err, req).(*httpbakery.Error),
			})
			return
		}
		fmt.Fprintf(w, "hello there")
	}
	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	req, err := http.NewRequest("GET", srv.URL, nil)
	c.Assert(err, gc.IsNil)

	// First check that a normal request fails.
	resp, err := httpbakery.NewClient().Do(req)
	c.Assert(err, gc.IsNil)
	defer resp.Body.Close()
	c.Assert(resp.StatusCode, gc.Equals, http.StatusTeapot)
	c.Assert(callCount, gc.Equals, 1)
	callCount = 0

	// Then check that a request with a custom error getter succeeds.
	errorGetter := func(resp *http.Response) error {
		if resp.StatusCode != http.StatusTeapot {
			return nil
		}
		data, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			panic(err)
		}
		var respErr customError
		if err := json.Unmarshal(data, &respErr); err != nil {
			panic(err)
		}
		return respErr.CustomError
	}

	resp, err = httpbakery.NewClient().DoWithCustomError(req, errorGetter)
	c.Assert(err, gc.IsNil)

	data, err := ioutil.ReadAll(resp.Body)
	c.Assert(err, gc.IsNil)
	c.Assert(string(data), gc.Equals, "hello there")
	c.Assert(callCount, gc.Equals, 2)
}

func (s *ClientSuite) TestHandleError(c *gc.C) {
	d := bakerytest.NewDischarger(nil)
	defer d.Close()

	// Create a target service.
	b := newBakery("loc", d, nil)

	srv := httptest.NewServer(serverHandler(serverHandlerParams{
		bakery:       b,
		authLocation: "unknown",
		mutateError:  nil,
	}))
	defer srv.Close()

	m, err := b.Oven.NewMacaroon(testContext, bakery.LatestVersion, []checkers.Caveat{{
		Location:  d.Location(),
		Condition: "something",
	}}, testOp)

	c.Assert(err, gc.IsNil)

	u, err := url.Parse(srv.URL + "/bar")
	c.Assert(err, gc.IsNil)

	respErr := &httpbakery.Error{
		Message: "an error",
		Code:    httpbakery.ErrDischargeRequired,
		Info: &httpbakery.ErrorInfo{
			Macaroon:     m,
			MacaroonPath: "/foo",
		},
	}
	client := httpbakery.NewClient()
	err = client.HandleError(testContext, u, respErr)
	c.Assert(err, gc.Equals, nil)
	// No cookies at the original location.
	c.Assert(client.Client.Jar.Cookies(u), gc.HasLen, 0)

	u.Path = "/foo"
	cookies := client.Client.Jar.Cookies(u)
	c.Assert(cookies, gc.HasLen, 1)

	// Check that we can actually make a request
	// with the newly acquired macaroon cookies.

	req, err := http.NewRequest("GET", srv.URL+"/foo", nil)
	c.Assert(err, gc.IsNil)

	resp, err := client.Do(req)
	c.Assert(err, gc.IsNil)
	resp.Body.Close()
	c.Assert(resp.StatusCode, gc.Equals, http.StatusOK)
}

func (s *ClientSuite) TestNewClientOldServer(c *gc.C) {
	d := bakerytest.NewDischarger(nil)
	defer d.Close()

	// Create a target service.
	b := newBakery("loc", d, nil)

	srv := httptest.NewServer(serverHandler(serverHandlerParams{
		bakery:       b,
		authLocation: d.Location(),
	}))
	defer srv.Close()

	// Make the request to the server.
	client := httpbakery.NewClient()
	req, err := http.NewRequest("GET", srv.URL, nil)
	c.Assert(err, gc.IsNil)
	resp, err := client.Do(req)
	c.Assert(err, gc.IsNil)
	defer resp.Body.Close()
	assertResponse(c, resp, "done")
}

func (s *ClientSuite) TestHandleErrorDifferentError(c *gc.C) {
	berr := &httpbakery.Error{
		Message: "an error",
		Code:    "another code",
	}
	client := httpbakery.NewClient()
	err := client.HandleError(testContext, &url.URL{}, berr)
	c.Assert(err, gc.Equals, berr)
}

func (s *ClientSuite) TestNewCookieExpiresLongExpiryTime(c *gc.C) {
	t := time.Now().Add(30 * time.Minute)
	b := newBakery("loc", nil, nil)
	m, err := b.Oven.NewMacaroon(testContext, bakery.LatestVersion, []checkers.Caveat{
		checkers.TimeBeforeCaveat(t),
	}, testOp)
	c.Assert(err, gc.IsNil)
	cookie, err := httpbakery.NewCookie(nil, macaroon.Slice{m.M()})
	c.Assert(err, gc.IsNil)
	c.Assert(cookie.Expires.Equal(t), gc.Equals, true, gc.Commentf("got %s want %s", cookie.Expires, t))
}

func (s *ClientSuite) TestNewCookieExpiresAlreadyExpired(c *gc.C) {
	t := time.Now().Add(-time.Minute)
	b := newBakery("loc", nil, nil)
	m, err := b.Oven.NewMacaroon(testContext, bakery.LatestVersion, []checkers.Caveat{
		checkers.TimeBeforeCaveat(t),
	}, testOp)
	c.Assert(err, gc.IsNil)
	cookie, err := httpbakery.NewCookie(nil, macaroon.Slice{m.M()})
	c.Assert(err, gc.IsNil)
	c.Assert(cookie.Expires, jc.Satisfies, time.Time.IsZero)
}

func (s *ClientSuite) TestNewCookieExpiresNoTimeBeforeCaveat(c *gc.C) {
	t0 := time.Now()
	b := newBakery("loc", nil, nil)
	m, err := b.Oven.NewMacaroon(testContext, bakery.LatestVersion, nil, testOp)
	c.Assert(err, gc.IsNil)
	cookie, err := httpbakery.NewCookie(nil, macaroon.Slice{m.M()})
	c.Assert(err, gc.IsNil)
	minExpires := t0.Add(httpbakery.PermanentExpiryDuration)
	maxExpires := time.Now().Add(httpbakery.PermanentExpiryDuration)
	if cookie.Expires.Before(minExpires) || cookie.Expires.After(maxExpires) {
		c.Fatalf("unexpected expiry time; got %v want %v", cookie.Expires, minExpires)
	}
}

func mustParseURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

type visitHandler struct {
	mux     *http.ServeMux
	rendez  chan struct{}
	respond func(w http.ResponseWriter)
}

func newVisitHandler(respond func(http.ResponseWriter)) *visitHandler {
	h := &visitHandler{
		rendez:  make(chan struct{}, 1),
		respond: respond,
		mux:     http.NewServeMux(),
	}
	h.mux.HandleFunc("/visit", h.serveVisit)
	h.mux.HandleFunc("/wait", h.serveWait)
	return h
}

func (h *visitHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	h.mux.ServeHTTP(w, req)
}

func (h *visitHandler) serveVisit(w http.ResponseWriter, req *http.Request) {
	h.rendez <- struct{}{}
}

func (h *visitHandler) serveWait(w http.ResponseWriter, req *http.Request) {
	<-h.rendez
	h.respond(w)
}

// assertResponse asserts that the given response is OK and contains
// the expected body text.
func assertResponse(c *gc.C, resp *http.Response, expectBody string) {
	body, err := ioutil.ReadAll(resp.Body)
	c.Assert(err, gc.IsNil)
	resp.Body.Close()
	c.Assert(resp.StatusCode, gc.Equals, http.StatusOK, gc.Commentf("body %q", body))
	c.Assert(string(body), gc.DeepEquals, expectBody)
	resp.Body = ioutil.NopCloser(bytes.NewReader(body))
}

type readCounter struct {
	io.ReadSeeker
	byteCount int
}

func (r *readCounter) Read(buf []byte) (int, error) {
	n, err := r.ReadSeeker.Read(buf)
	r.byteCount += n
	return n, err
}

func newBakery(location string, locator bakery.ThirdPartyLocator, checker bakery.FirstPartyCaveatChecker) *bakery.Bakery {
	if checker == nil {
		c := checkers.New(nil)
		c.Namespace().Register("testns", "")
		c.Register("is", "testns", checkIsSomething)
		checker = c
	}
	key, err := bakery.GenerateKey()
	if err != nil {
		panic(err)
	}
	return bakery.New(bakery.BakeryParams{
		Location: location,
		Locator:  locator,
		Key:      key,
		Checker:  checker,
	})
}

func clientRequestWithCookies(c *gc.C, u string, macaroons macaroon.Slice) *http.Client {
	client := httpbakery.NewHTTPClient()
	url, err := url.Parse(u)
	c.Assert(err, gc.IsNil)
	err = httpbakery.SetCookie(client.Jar, url, nil, macaroons)
	c.Assert(err, gc.IsNil)
	return client
}

var httpReqServer = &httprequest.Server{
	ErrorMapper: httpbakery.ErrorToResponse,
}

type serverHandlerParams struct {
	// bakery is used to check incoming requests
	// and macaroons for discharge-required errors.
	bakery *bakery.Bakery

	// authLocation holds the location of any 3rd party authorizer.
	// If this is non-empty, a 3rd party caveat will be added
	// addressed to this location.
	authLocation string

	// mutateError, if non-zero, will be called with any
	// discharge-required error before responding
	// to the client.
	mutateError func(*httpbakery.Error)

	// If caveats is non-nil, it is called to get caveats to
	// add to the returned macaroon.
	caveats func() []checkers.Caveat

	// alwaysReadBody specifies whether the handler should always read
	// the entire request body before returning.
	alwaysReadBody bool
}

// serverHandler returns an HTTP handler that checks macaroon authorization
// and, if that succeeds, writes the string "done" followed by all the
// data read from the request body.
// It recognises the single first party caveat "is something".
func serverHandler(hp serverHandlerParams) http.Handler {
	h := httpReqServer.HandleErrors(func(p httprequest.Params) error {
		if hp.alwaysReadBody {
			defer ioutil.ReadAll(p.Request.Body)
		}
		if _, err := hp.bakery.Checker.Auth(httpbakery.RequestMacaroons(p.Request)...).Allow(p.Context, testOp); err != nil {
			return newDischargeRequiredError(hp, err, p.Request)
		}
		fmt.Fprintf(p.Response, "done")
		// Special case: the no-body path doesn't return the body.
		if p.Request.URL.Path == "/no-body" {
			return nil
		}
		data, err := ioutil.ReadAll(p.Request.Body)
		if err != nil {
			panic(fmt.Errorf("cannot read body: %v", err))
		}
		if len(data) > 0 {
			fmt.Fprintf(p.Response, " %s", data)
		}
		return nil
	})
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		h(w, req, nil)
	})
}

// newDischargeRequiredError returns a discharge-required error holding
// a newly minted macaroon referencing the original check error
// checkErr. If hp.authLocation is non-empty, the issued macaroon will
// contain an "is-ok" third party caveat addressed to that location.
func newDischargeRequiredError(hp serverHandlerParams, checkErr error, req *http.Request) error {
	var caveats []checkers.Caveat
	if hp.authLocation != "" {
		caveats = []checkers.Caveat{{
			Location:  hp.authLocation,
			Condition: "is-ok",
		}}
	}
	if hp.caveats != nil {
		caveats = append(caveats, hp.caveats()...)
	}
	m, err := hp.bakery.Oven.NewMacaroon(testContext, bakery.LatestVersion, caveats, testOp)
	if err != nil {
		panic(fmt.Errorf("cannot make new macaroon: %v", err))
	}
	err = httpbakery.NewDischargeRequiredError(httpbakery.DischargeRequiredErrorParams{
		Macaroon:      m,
		OriginalError: checkErr,
		Request:       req,
	})
	if hp.mutateError != nil {
		hp.mutateError(err.(*httpbakery.Error))
	}
	return err
}

func isSomethingCaveat() checkers.Caveat {
	return checkers.Caveat{
		Condition: "is something",
		Namespace: "testns",
	}
}

func checkIsSomething(ctx context.Context, _, arg string) error {
	if arg != "something" {
		return fmt.Errorf(`%v doesn't match "something"`, arg)
	}
	return nil
}

type interactor struct {
	kind     string
	interact func(ctx context.Context, client *httpbakery.Client, location string, interactionRequiredErr *httpbakery.Error) (*httpbakery.DischargeToken, error)
}

func (i interactor) Kind() string {
	return i.kind
}

func (i interactor) Interact(ctx context.Context, client *httpbakery.Client, location string, interactionRequiredErr *httpbakery.Error) (*httpbakery.DischargeToken, error) {
	return i.interact(ctx, client, location, interactionRequiredErr)
}

var (
	_ httpbakery.Interactor       = interactor{}
	_ httpbakery.Interactor       = legacyInteractor{}
	_ httpbakery.LegacyInteractor = legacyInteractor{}
)

type legacyInteractor struct {
	kind           string
	interact       func(ctx context.Context, client *httpbakery.Client, location string, interactionRequiredErr *httpbakery.Error) (*httpbakery.DischargeToken, error)
	legacyInteract func(ctx context.Context, client *httpbakery.Client, location string, visitURL *url.URL) error
}

func (i legacyInteractor) Kind() string {
	return i.kind
}

func (i legacyInteractor) Interact(ctx context.Context, client *httpbakery.Client, location string, interactionRequiredErr *httpbakery.Error) (*httpbakery.DischargeToken, error) {
	if i.interact == nil {
		return nil, errgo.Newf("non-legacy interaction not supported")
	}
	return i.interact(ctx, client, location, interactionRequiredErr)
}

func (i legacyInteractor) LegacyInteract(ctx context.Context, client *httpbakery.Client, location string, visitURL *url.URL) error {
	if i.legacyInteract == nil {
		return errgo.Newf("legacy interaction not supported")
	}
	return i.legacyInteract(ctx, client, location, visitURL)
}
