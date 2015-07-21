package httpbakery_test

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	jujutesting "github.com/juju/testing"
	"github.com/juju/utils/jsonhttp"
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
			return nil, newDischargeRequiredError(svc, srv2.URL, nil, err)
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

func newHTTPDischarger(locator bakery.PublicKeyLocator, checker func(svc *bakery.Service, req *http.Request, cavId, cav string) ([]checkers.Caveat, error)) (*bakery.PublicKey, http.Handler) {
	svc := newService("loc", locator)
	mux := http.NewServeMux()
	httpbakery.AddDischargeHandler(mux, "/", svc, func(req *http.Request, cavId, cav string) ([]checkers.Caveat, error) {
		return checker(svc, req, cavId, cav)
	})
	return svc.PublicKey(), mux
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
		c.Logf("client jar %p", client.Jar)
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
	assertCookieCount("/foo", 1)
	assertCookieCount("/foo/", 1)
	assertCookieCount("/foo/bar/", 1)
	assertCookieCount("/foo/bar/baz", 1)

	cookiePath = "../"
	c.Logf("- cookie path %q", cookiePath)
	doRequest()
	assertCookieCount("", 0)
	assertCookieCount("/bar", 0)
	assertCookieCount("/foo", 1)
	assertCookieCount("/foo/", 1)
	assertCookieCount("/foo/bar/", 1)
	assertCookieCount("/foo/bar/baz", 1)
}

func (s *ClientSuite) TestPublicKey(c *gc.C) {
	d := bakerytest.NewDischarger(nil, noCaveatChecker)
	defer d.Close()
	client := httpbakery.NewHTTPClient()
	publicKey, err := httpbakery.PublicKeyForLocation(client, d.Location())
	c.Assert(err, gc.IsNil)
	expectedKey := d.Service.PublicKey()
	c.Assert(publicKey, gc.DeepEquals, expectedKey)
}

func (s *ClientSuite) TestPublicKeyWrongURL(c *gc.C) {
	client := httpbakery.NewHTTPClient()
	_, err := httpbakery.PublicKeyForLocation(client, "http://localhost:0")
	c.Assert(err, gc.ErrorMatches,
		`cannot get public key from "http://localhost:0/publickey": Get http://localhost:0/publickey: dial tcp 127.0.0.1:0: .*connection refused`)
}

func (s *ClientSuite) TestPublicKeyReturnsInvalidJson(c *gc.C) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "BADJSON")
	}))
	defer ts.Close()
	client := httpbakery.NewHTTPClient()
	_, err := httpbakery.PublicKeyForLocation(client, ts.URL)
	c.Assert(err, gc.ErrorMatches,
		fmt.Sprintf(`failed to decode response from "%s/publickey": invalid character 'B' looking for beginning of value`, ts.URL))
}

func (s *ClientSuite) TestPublicKeyReturnsStatusInternalServerError(c *gc.C) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	client := httpbakery.NewHTTPClient()
	_, err := httpbakery.PublicKeyForLocation(client, ts.URL)
	c.Assert(err, gc.ErrorMatches,
		fmt.Sprintf(`cannot get public key from "%s/publickey": got status 500 Internal Server Error`, ts.URL))
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
	c.Assert(err, gc.ErrorMatches, `cannot get discharge from ".*": cannot start interactive session: cannot visit`)
	c.Assert(httpbakery.IsInteractionError(errgo.Cause(err)), gc.Equals, true)
	ierr, ok := errgo.Cause(err).(*httpbakery.InteractionError)
	c.Assert(ok, gc.Equals, true)
	c.Assert(ierr.Reason, gc.Equals, errCannotVisit)
	c.Assert(resp, gc.IsNil)
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

// serverHandler returns an HTTP handler that checks macaroon authorization
// and, if that succeeds, writes the string "done" and echos anything in the
// request body.
// It recognises the single first party caveat "is something".
func serverHandler(service *bakery.Service, authLocation string, cookiePath func() string) http.Handler {
	handleErrors := jsonhttp.HandleErrors(httpbakery.ErrorToResponse)
	return handleErrors(func(w http.ResponseWriter, req *http.Request) error {
		if _, checkErr := httpbakery.CheckRequest(service, req, nil, isChecker("something")); checkErr != nil {
			return newDischargeRequiredError(service, authLocation, cookiePath, checkErr)
		}
		fmt.Fprintf(w, "done")
		data, err := ioutil.ReadAll(req.Body)
		if err != nil {
			panic(fmt.Errorf("cannot read body: %v", err))
		}
		if len(data) > 0 {
			fmt.Fprintf(w, " %s", data)
		}
		return nil
	})
}

// newDischargeRequiredError returns a discharge-required error
// holding a newly minted macaroon
// referencing the original check error checkErr.
// If authLocation is non-empty, the issued macaroon
// will contain an "is-ok" third party caveat addressed to that location.
//
// If cookiePath is not nil, it will be called to find the cookie path to
// put in the response.
func newDischargeRequiredError(svc *bakery.Service, authLocation string, cookiePath func() string, checkErr error) error {
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
