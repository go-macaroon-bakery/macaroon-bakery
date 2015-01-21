package httpbakery_test

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	gc "gopkg.in/check.v1"
	"gopkg.in/macaroon.v1"

	"gopkg.in/macaroon-bakery.v0/bakery"
	"gopkg.in/macaroon-bakery.v0/bakery/checkers"
	"gopkg.in/macaroon-bakery.v0/bakerytest"
	"gopkg.in/macaroon-bakery.v0/httpbakery"
)

type ClientSuite struct{}

var _ = gc.Suite(&ClientSuite{})

// TestSingleServiceFirstParty creates a single service
// with a macaroon with one first party caveat.
// It creates a request with this macaroon and checks that the service
// can verify this macaroon as valid.
func (s *ClientSuite) TestSingleServiceFirstParty(c *gc.C) {
	// Create a target service.
	svc := newService(c, "loc", nil)
	// No discharge required, so pass "unknown" for the third party
	// caveat discharger location.
	ts := newServer(serverHandler(svc, "unknown"))
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
	body, err := ioutil.ReadAll(resp.Body)
	c.Assert(err, gc.IsNil)
	c.Assert(string(body), gc.DeepEquals, "done")
}

func (s *ClientSuite) TestRepeatedRequestWithBody(c *gc.C) {
	d := bakerytest.NewDischarger(nil, noCaveatChecker)
	defer d.Close()

	// Create a target service.
	svc := newService(c, "loc", d)

	ts := newServer(serverHandler(svc, d.Location()))
	defer ts.Close()

	// Create a client request.
	req, err := http.NewRequest("POST", ts.URL, nil)
	c.Assert(err, gc.IsNil)

	// Make the request to the server.

	// First try with a body in the request, which should be denied
	// because we must use DoWithBody.
	req.Body = ioutil.NopCloser(strings.NewReader("postbody"))
	resp, err := httpbakery.Do(httpbakery.NewHTTPClient(), req, noVisit)
	c.Assert(err, gc.ErrorMatches, "body unexpectedly provided in request - use DoWithBody")
	c.Assert(resp, gc.IsNil)

	// Then try with no authorization, so make sure that httpbakery.Do
	// really will retry the request.

	req.Body = nil

	bodyText := "postbody"
	bodyReader := &readCounter{ReadSeeker: strings.NewReader(bodyText)}

	resp, err = httpbakery.DoWithBody(httpbakery.NewHTTPClient(), req, httpbakery.SeekerBody(bodyReader), noVisit)
	c.Assert(err, gc.IsNil)
	c.Assert(resp.StatusCode, gc.Equals, http.StatusOK)
	data, err := ioutil.ReadAll(resp.Body)
	c.Assert(err, gc.IsNil)
	c.Assert(string(data), gc.Equals, "done postbody")

	// Sanity check that the body really was read twice and hence
	// that we are checking the logic we intend to check.
	c.Assert(bodyReader.byteCount, gc.Equals, len(bodyText)*2)
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

func newService(c *gc.C, location string, locator bakery.PublicKeyLocator) *bakery.Service {
	svc, err := bakery.NewService(bakery.NewServiceParams{
		Location: location,
		Locator:  locator,
	})
	c.Assert(err, gc.IsNil)
	return svc
}

func newServer(h func(http.ResponseWriter, *http.Request)) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/", h)
	return httptest.NewServer(mux)
}

func clientRequestWithCookies(c *gc.C, u string, macaroons macaroon.Slice) *http.Client {
	client := httpbakery.NewHTTPClient()
	url, err := url.Parse(u)
	c.Assert(err, gc.IsNil)
	err = httpbakery.SetCookie(client.Jar, url, macaroons)
	c.Assert(err, gc.IsNil)
	return client
}

func serverHandler(service *bakery.Service, authLocation string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, req *http.Request) {
		if _, checkErr := httpbakery.CheckRequest(service, req, nil, isChecker("something")); checkErr != nil {
			m, err := service.NewMacaroon("", nil, []checkers.Caveat{{
				Location:  authLocation,
				Condition: "is-ok",
			}})
			if err != nil {
				panic(fmt.Errorf("cannot make new macaroon: %v", err))
			}
			httpbakery.WriteDischargeRequiredError(w, m, checkErr)
			return
		}
		fmt.Fprintf(w, "done")
		data, err := ioutil.ReadAll(req.Body)
		if err != nil {
			panic(fmt.Errorf("cannot read body: %v", err))
		}
		if len(data) > 0 {
			fmt.Fprintf(w, " %s", data)
		}
	}
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

func noCaveatChecker(cond, arg string) ([]checkers.Caveat, error) {
	return nil, nil
}
