package httpbakery_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"github.com/juju/httprequest"
	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v1"

	"gopkg.in/macaroon-bakery.v1/bakery"
	"gopkg.in/macaroon-bakery.v1/bakery/checkers"
	"gopkg.in/macaroon-bakery.v1/bakerytest"
	"gopkg.in/macaroon-bakery.v1/httpbakery"
)

type ClientSuite struct {
	jujutesting.LoggingSuite
}

var _ = gc.Suite(&ClientSuite{})

// TestSingleServiceFirstParty creates a single service
// with a macaroon with one first party caveat.
// It creates a request with this macaroon and checks that the service
// can verify this macaroon as valid.
func (s *ClientSuite) TestSingleServiceFirstParty(c *gc.C) {
	// Create a target service.
	svc := newService("loc", nil)
	// No discharge required, so pass "unknown" for the third party
	// caveat discharger location so we know that we don't try
	// to discharge the location.
	ts := httptest.NewServer(serverHandler(svc, "unknown", nil))
	defer ts.Close()

	// Mint a macaroon for the target service.
	serverMacaroon, err := svc.NewMacaroon("", nil, nil)
	c.Assert(err, gc.IsNil)
	c.Assert(serverMacaroon.Location(), gc.Equals, "loc")
	err = svc.AddCaveat(serverMacaroon, checkers.Caveat{
		Condition: "is something",
	})
	c.Assert(err, gc.IsNil)

	// Create a client request.
	req, err := http.NewRequest("GET", ts.URL, nil)
	c.Assert(err, gc.IsNil)
	client := clientRequestWithCookies(c, ts.URL, macaroon.Slice{serverMacaroon})
	// Somehow the client has accquired the macaroon. Add it to the cookiejar in our request.

	// Make the request to the server.
	resp, err := client.Do(req)
	c.Assert(err, gc.IsNil)
	defer resp.Body.Close()
	assertResponse(c, resp, "done")
}

func (s *ClientSuite) TestSingleServiceFirstPartyWithHeader(c *gc.C) {
	// Create a target service.
	svc := newService("loc", nil)
	// No discharge required, so pass "unknown" for the third party
	// caveat discharger location so we know that we don't try
	// to discharge the location.
	ts := httptest.NewServer(serverHandler(svc, "unknown", nil))
	defer ts.Close()

	// Mint a macaroon for the target service.
	serverMacaroon, err := svc.NewMacaroon("", nil, nil)
	c.Assert(err, gc.IsNil)
	c.Assert(serverMacaroon.Location(), gc.Equals, "loc")
	err = svc.AddCaveat(serverMacaroon, checkers.Caveat{
		Condition: "is something",
	})
	c.Assert(err, gc.IsNil)

	// Serialize the macaroon slice.
	data, err := json.Marshal(macaroon.Slice{serverMacaroon})
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
	d := bakerytest.NewDischarger(nil, noCaveatChecker)
	defer d.Close()

	// Create a target service.
	svc := newService("loc", d)

	ts := httptest.NewServer(serverHandler(svc, d.Location(), nil))
	defer ts.Close()

	// Create a client request.
	req, err := http.NewRequest("POST", ts.URL, nil)
	c.Assert(err, gc.IsNil)

	// Make the request to the server.

	// First try with a body in the request, which should be denied
	// because we must use DoWithBody.
	req.Body = ioutil.NopCloser(strings.NewReader("postbody"))
	resp, err := httpbakery.NewClient().Do(req)
	c.Assert(err, gc.ErrorMatches, "body unexpectedly provided in request - use DoWithBody")
	c.Assert(resp, gc.IsNil)

	// Then try with no authorization, so make sure that httpbakery.Do
	// really will retry the request.

	req.Body = nil

	bodyText := "postbody"
	bodyReader := &readCounter{ReadSeeker: strings.NewReader(bodyText)}

	resp, err = httpbakery.NewClient().DoWithBody(req, bodyReader)
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

	d := bakerytest.NewDischarger(nil, noCaveatChecker)
	defer d.Close()

	// Create a target service.
	svc := newService("loc", d)

	ts := httptest.NewServer(serverHandler(svc, d.Location(), nil))
	defer ts.Close()

	// Create a client request.
	req, err := http.NewRequest("POST", ts.URL+"/no-body", nil)
	c.Assert(err, gc.IsNil)

	resp, err := httpbakery.NewClient().DoWithBody(req, &largeReader{total: 3 * 1024 * 1024})
	c.Assert(err, gc.IsNil)
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

func (s *ClientSuite) TestDoWithBodyFailsWithBodyInRequest(c *gc.C) {
	body := strings.NewReader("foo")
	// Create a client request.
	req, err := http.NewRequest("POST", "http://0.1.2.3/", body)
	c.Assert(err, gc.IsNil)
	_, err = httpbakery.NewClient().DoWithBody(req, body)
	c.Assert(err, gc.ErrorMatches, "body unexpectedly supplied in Request struct")
}

func (s *ClientSuite) TestDischargeServerWithMacaraqOnDischarge(c *gc.C) {
	locator := bakery.NewPublicKeyRing()

	var called [3]int

	// create the services from leaf discharger to primary
	// service so that each one can know the location
	// to discharge at.
	key2, h2 := newHTTPDischarger(locator, func(svc *bakery.Service, req *http.Request, cavId, cav string) ([]checkers.Caveat, error) {
		called[2]++
		if cav != "is-ok" {
			return nil, fmt.Errorf("unrecognized caveat at srv2")
		}
		return nil, nil
	})
	srv2 := httptest.NewServer(h2)
	locator.AddPublicKeyForLocation(srv2.URL, true, key2)

	key1, h1 := newHTTPDischarger(locator, func(svc *bakery.Service, req *http.Request, cavId, cav string) ([]checkers.Caveat, error) {
		called[1]++
		if _, err := httpbakery.CheckRequest(svc, req, nil, checkers.New()); err != nil {
			return nil, newDischargeRequiredError(svc, srv2.URL, nil, err, req)
		}
		if cav != "is-ok" {
			return nil, fmt.Errorf("unrecognized caveat at srv1")
		}
		return nil, nil
	})
	srv1 := httptest.NewServer(h1)
	locator.AddPublicKeyForLocation(srv1.URL, true, key1)

	svc0 := newService("loc", locator)
	srv0 := httptest.NewServer(serverHandler(svc0, srv1.URL, nil))

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

func (s *ClientSuite) TestVersion0Generates407Status(c *gc.C) {
	m, err := macaroon.New([]byte("root key"), "id", "location")
	c.Assert(err, gc.IsNil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		httpbakery.WriteDischargeRequiredErrorForRequest(w, m, "", errgo.New("foo"), req)
	}))
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	c.Assert(err, gc.IsNil)
	c.Assert(resp.StatusCode, gc.Equals, http.StatusProxyAuthRequired)
}

func (s *ClientSuite) TestVersion1Generates401Status(c *gc.C) {
	m, err := macaroon.New([]byte("root key"), "id", "location")
	c.Assert(err, gc.IsNil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		httpbakery.WriteDischargeRequiredErrorForRequest(w, m, "", errgo.New("foo"), req)
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

func newHTTPDischarger(locator bakery.PublicKeyLocator, checker func(svc *bakery.Service, req *http.Request, cavId, cav string) ([]checkers.Caveat, error)) (*bakery.PublicKey, http.Handler) {
	svc := newService("loc", locator)
	mux := http.NewServeMux()
	httpbakery.AddDischargeHandler(mux, "/", svc, func(req *http.Request, cavId, cav string) ([]checkers.Caveat, error) {
		return checker(svc, req, cavId, cav)
	})
	return svc.PublicKey(), mux
}

func (s *ClientSuite) TestDischargeAcquirer(c *gc.C) {
	rootKey := []byte("secret")
	m, err := macaroon.New(rootKey, "", "here")
	c.Assert(err, gc.IsNil)

	dischargeRootKey := []byte("shared root key")
	thirdPartyCaveatId := "3rd party caveat"
	err = m.AddThirdPartyCaveat(dischargeRootKey, thirdPartyCaveatId, "there")
	c.Assert(err, gc.IsNil)

	dm, err := macaroon.New(dischargeRootKey, thirdPartyCaveatId, "there")
	c.Assert(err, gc.IsNil)

	ta := &testAcquirer{dischargeMacaroon: dm}
	cl := httpbakery.NewClient()
	cl.DischargeAcquirer = ta

	ms, err := cl.DischargeAll(m)
	c.Assert(err, gc.IsNil)
	c.Assert(ms, gc.HasLen, 2)

	c.Assert(ta.acquireLocation, gc.Equals, "here") // should be first-party location
	c.Assert(ta.acquireCaveat.Id, gc.Equals, thirdPartyCaveatId)
	expectCaveat := "must foo"
	var lastCaveat string
	err = ms[0].Verify(rootKey, func(s string) error {
		if s != expectCaveat {
			return errgo.Newf(`expected %q, got %q`, expectCaveat, s)
		}
		lastCaveat = s
		return nil
	}, ms[1:])
	c.Assert(err, gc.IsNil)
	c.Assert(lastCaveat, gc.Equals, expectCaveat)
}

type testAcquirer struct {
	dischargeMacaroon *macaroon.Macaroon

	acquireLocation string
	acquireCaveat   macaroon.Caveat
}

// AcquireDischarge implements httpbakery.DischargeAcquirer.
func (ta *testAcquirer) AcquireDischarge(loc string, cav macaroon.Caveat) (*macaroon.Macaroon, error) {
	ta.acquireLocation = loc
	ta.acquireCaveat = cav
	err := ta.dischargeMacaroon.AddFirstPartyCaveat("must foo")
	if err != nil {
		return nil, err
	}
	return ta.dischargeMacaroon, nil
}

func (s *ClientSuite) TestMacaroonCookiePath(c *gc.C) {
	svc := newService("loc", nil)

	cookiePath := ""
	ts := httptest.NewServer(serverHandler(svc, "", func() string {
		return cookiePath
	}))
	defer ts.Close()

	var client *httpbakery.Client
	doRequest := func() {
		req, err := http.NewRequest("GET", ts.URL+"/foo/bar/", nil)
		c.Assert(err, gc.IsNil)
		client = httpbakery.NewClient()
		resp, err := client.Do(req)
		c.Assert(err, gc.IsNil)
		defer resp.Body.Close()
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
	d := bakerytest.NewDischarger(nil, func(_ *http.Request, cond, arg string) ([]checkers.Caveat, error) {
		return nil, errgo.New("boo! cond " + cond)
	})
	defer d.Close()

	// Create a target service.
	svc := newService("loc", d)

	ts := httptest.NewServer(serverHandler(svc, d.Location(), nil))
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
	d := bakerytest.NewDischarger(nil, func(_ *http.Request, cond, arg string) ([]checkers.Caveat, error) {
		return nil, &httpbakery.Error{
			Code:    httpbakery.ErrInteractionRequired,
			Message: "interaction required",
			Info: &httpbakery.ErrorInfo{
				VisitURL: "http://0.1.2.3/",
				WaitURL:  "http://0.1.2.3/",
			},
		}
	})
	defer d.Close()

	// Create a target service.
	svc := newService("loc", d)

	ts := httptest.NewServer(serverHandler(svc, d.Location(), nil))
	defer ts.Close()

	// Create a client request.
	req, err := http.NewRequest("GET", ts.URL, nil)
	c.Assert(err, gc.IsNil)

	errCannotVisit := errgo.New("cannot visit")
	client := httpbakery.NewClient()
	client.VisitWebPage = func(*url.URL) error {
		return errCannotVisit
	}

	// Make the request to the server.
	resp, err := client.Do(req)
	c.Assert(err, gc.ErrorMatches, `cannot get discharge from "https://.*": cannot start interactive session: cannot visit`)
	c.Assert(httpbakery.IsInteractionError(errgo.Cause(err)), gc.Equals, true)
	ierr, ok := errgo.Cause(err).(*httpbakery.InteractionError)
	c.Assert(ok, gc.Equals, true)
	c.Assert(ierr.Reason, gc.Equals, errCannotVisit)
	c.Assert(resp, gc.IsNil)
}

var dischargeWithVisitURLErrorTests = []struct {
	about       string
	respond     func(http.ResponseWriter)
	expectError string
}{{
	about: "error message",
	respond: func(w http.ResponseWriter) {
		httprequest.ErrorMapper(httpbakery.ErrorToResponse).WriteError(w, fmt.Errorf("an error"))
	},
	expectError: `cannot get discharge from ".*": failed to acquire macaroon after waiting: third party refused discharge: an error`,
}, {
	about: "non-JSON error",
	respond: func(w http.ResponseWriter) {
		w.Write([]byte("bad response"))
	},
	// TODO fix this unhelpful error message
	expectError: `cannot get discharge from ".*": cannot unmarshal wait response: invalid character 'b' looking for beginning of value`,
}}

func (s *ClientSuite) TestDischargeWithVisitURLError(c *gc.C) {
	visitor := newVisitHandler(nil)
	visitSrv := httptest.NewServer(visitor)
	defer visitSrv.Close()

	d := bakerytest.NewDischarger(nil, func(_ *http.Request, cond, arg string) ([]checkers.Caveat, error) {
		return nil, &httpbakery.Error{
			Code:    httpbakery.ErrInteractionRequired,
			Message: "interaction required",
			Info: &httpbakery.ErrorInfo{
				VisitURL: visitSrv.URL + "/visit",
				WaitURL:  visitSrv.URL + "/wait",
			},
		}
	})
	defer d.Close()

	// Create a target service.
	svc := newService("loc", d)
	ts := httptest.NewServer(serverHandler(svc, d.Location(), nil))
	defer ts.Close()

	for i, test := range dischargeWithVisitURLErrorTests {
		c.Logf("test %d: %s", i, test.about)
		visitor.respond = test.respond

		client := httpbakery.NewClient()
		client.VisitWebPage = func(u *url.URL) error {
			resp, err := http.Get(u.String())
			if err != nil {
				return err
			}
			resp.Body.Close()
			return nil
		}

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
	svc := newService("loc", nil)

	m1, err := svc.NewMacaroon("id1", []byte("key1"), nil)
	c.Assert(err, gc.IsNil)
	m2, err := svc.NewMacaroon("id2", []byte("key2"), nil)
	c.Assert(err, gc.IsNil)

	u1 := mustParseURL("http://0.1.2.3/")
	u2 := mustParseURL("http://0.1.2.3/x/")

	// Create some cookies with different cookie paths.
	jar, err := cookiejar.New(nil)
	c.Assert(err, gc.IsNil)
	httpbakery.SetCookie(jar, u1, macaroon.Slice{m1})
	httpbakery.SetCookie(jar, u2, macaroon.Slice{m2})
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
	c.Assert(mss[0][0].Id(), gc.Equals, "id1")

	mss = httpbakery.MacaroonsForURL(jar, u2)

	checked := make(map[string]int)
	for _, ms := range mss {
		checked[ms[0].Id()]++
		err := svc.Check(ms, checkers.New())
		c.Assert(err, gc.IsNil)
	}
	c.Assert(checked, jc.DeepEquals, map[string]int{
		"id1": 1,
		"id2": 1,
	})
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
	c.Assert(resp.StatusCode, gc.Equals, http.StatusOK)
	body, err := ioutil.ReadAll(resp.Body)
	c.Assert(err, gc.IsNil)
	c.Assert(string(body), gc.DeepEquals, expectBody)
}

func noVisit(*url.URL) error {
	return fmt.Errorf("should not be visiting")
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

func newService(location string, locator bakery.PublicKeyLocator) *bakery.Service {
	svc, err := bakery.NewService(bakery.NewServiceParams{
		Location: location,
		Locator:  locator,
	})
	if err != nil {
		panic(err)
	}
	return svc
}

func clientRequestWithCookies(c *gc.C, u string, macaroons macaroon.Slice) *http.Client {
	client := httpbakery.NewHTTPClient()
	url, err := url.Parse(u)
	c.Assert(err, gc.IsNil)
	err = httpbakery.SetCookie(client.Jar, url, macaroons)
	c.Assert(err, gc.IsNil)
	return client
}

var handleErrors = httprequest.ErrorMapper(httpbakery.ErrorToResponse).HandleErrors

// serverHandler returns an HTTP handler that checks macaroon authorization
// and, if that succeeds, writes the string "done" and echos anything in the
// request body.
// It recognises the single first party caveat "is something".
func serverHandler(service *bakery.Service, authLocation string, cookiePath func() string) http.Handler {
	h := handleErrors(func(p httprequest.Params) error {
		if _, checkErr := httpbakery.CheckRequest(service, p.Request, nil, isChecker("something")); checkErr != nil {
			return newDischargeRequiredError(service, authLocation, cookiePath, checkErr, p.Request)
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
// checkErr. If authLocation is non-empty, the issued macaroon will
// contain an "is-ok" third party caveat addressed to that location.
//
// If cookiePath is not nil, it will be called to find the cookie path
// to put in the response.
//
// If req is non-nil, it will be used to pass to NewDischargeRequiredErrorForRequest,
// otherwise the old protocol (triggered by NewDischargeRequiredError) will be used.
func newDischargeRequiredError(svc *bakery.Service, authLocation string, cookiePath func() string, checkErr error, req *http.Request) error {
	var caveats []checkers.Caveat
	if authLocation != "" {
		caveats = []checkers.Caveat{{
			Location:  authLocation,
			Condition: "is-ok",
		}}
	}
	m, err := svc.NewMacaroon("", nil, caveats)
	if err != nil {
		panic(fmt.Errorf("cannot make new macaroon: %v", err))
	}
	path := ""
	if cookiePath != nil {
		path = cookiePath()
	}
	if req != nil {
		return httpbakery.NewDischargeRequiredErrorForRequest(m, path, checkErr, req)
	}
	return httpbakery.NewDischargeRequiredError(m, path, checkErr)
}

type isChecker string

func (isChecker) Condition() string {
	return "is"
}

func (c isChecker) Check(_, arg string) error {
	if arg != string(c) {
		return fmt.Errorf("%v doesn't match %s", arg, c)
	}
	return nil
}

func noCaveatChecker(_ *http.Request, cond, arg string) ([]checkers.Caveat, error) {
	return nil, nil
}

func (s *ClientSuite) TestNewCookieExpires(c *gc.C) {
	t := time.Now().Add(24 * time.Hour)
	svc := newService("loc", nil)
	m, err := svc.NewMacaroon("", nil, []checkers.Caveat{
		checkers.TimeBeforeCaveat(t),
	})
	c.Assert(err, gc.IsNil)
	cookie, err := httpbakery.NewCookie(macaroon.Slice{m})
	c.Assert(err, gc.IsNil)
	c.Assert(cookie.Expires.Equal(t), gc.Equals, true, gc.Commentf("obtained: %s, expected: %s", cookie.Expires, t))
}

func (s *ClientSuite) TestNewCookieDefaultExpiry(c *gc.C) {
	t := time.Now()
	jujutesting.PatchValue(httpbakery.TimeNow, func() time.Time {
		return t
	})

	svc := newService("loc", nil)
	m, err := svc.NewMacaroon("", nil, []checkers.Caveat{})
	c.Assert(err, gc.IsNil)
	cookie, err := httpbakery.NewCookie(macaroon.Slice{m})
	c.Assert(err, gc.IsNil)
	c.Assert(cookie.Expires.Equal(t.Add(24*time.Hour)), gc.Equals, true, gc.Commentf("obtained: %s, expected: %s", cookie.Expires, t))
}

func (s *ClientSuite) TestDoWithBodyAndCustomError(c *gc.C) {
	d := bakerytest.NewDischarger(nil, noCaveatChecker)
	defer d.Close()

	// Create a target service.
	svc := newService("loc", d)

	type customError struct {
		CustomError *httpbakery.Error
	}
	callCount := 0
	handler := func(w http.ResponseWriter, req *http.Request) {
		callCount++
		if _, checkErr := httpbakery.CheckRequest(svc, req, nil, checkers.New()); checkErr != nil {
			httprequest.WriteJSON(w, http.StatusTeapot, customError{
				CustomError: newDischargeRequiredError(svc, d.Location(), nil, checkErr, req).(*httpbakery.Error),
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

	resp, err = httpbakery.NewClient().DoWithBodyAndCustomError(req, nil, errorGetter)
	c.Assert(err, gc.IsNil)

	data, err := ioutil.ReadAll(resp.Body)
	c.Assert(err, gc.IsNil)
	c.Assert(string(data), gc.Equals, "hello there")
	c.Assert(callCount, gc.Equals, 2)
}

func (s *ClientSuite) TestHandleError(c *gc.C) {
	d := bakerytest.NewDischarger(nil, noCaveatChecker)
	defer d.Close()

	// Create a target service.
	svc := newService("loc", d)

	srv := httptest.NewServer(serverHandler(svc, "unknown", nil))
	defer srv.Close()

	m, err := svc.NewMacaroon("", nil, []checkers.Caveat{{
		Location:  d.Location(),
		Condition: "something",
	}})
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
	err = client.HandleError(u, respErr)
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

func (s *ClientSuite) TestHandleErrorDifferentError(c *gc.C) {
	berr := &httpbakery.Error{
		Message: "an error",
		Code:    "another code",
	}
	client := httpbakery.NewClient()
	err := client.HandleError(&url.URL{}, berr)
	c.Assert(err, gc.Equals, berr)
}
