package httpbakery_test

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"

	gc "gopkg.in/check.v1"
	"gopkg.in/macaroon.v1"

	"gopkg.in/macaroon-bakery.v0/bakery"
	"gopkg.in/macaroon-bakery.v0/httpbakery"
)

type ServiceSuite struct{}

var _ = gc.Suite(&ServiceSuite{})

// TestSingleServiceFirstParty creates a single service
// with a macaroon with one first party caveat.
// It creates a request with this macaroon and checks that the service
// can verify this macaroon as valid.
func (s *ServiceSuite) TestSingleServiceFirstParty(c *gc.C) {

	// Create a target service.
	svc := newService(c, "loc", nil)
	ts := newServer(serverHandler(svc))
	defer ts.Close()

	// Mint a macaroon for the target service.
	serverMacaroon, err := svc.NewMacaroon("", nil, nil)
	c.Assert(err, gc.IsNil)
	c.Assert(serverMacaroon.Location(), gc.Equals, "loc")
	cav := bakery.Caveat{
		Condition: "something",
	}
	err = svc.AddCaveat(serverMacaroon, cav)
	c.Assert(err, gc.IsNil)

	// Create a client request.
	req, err := http.NewRequest("GET", ts.URL, nil)
	c.Assert(err, gc.IsNil)
	client := clientRequestWithCookies(c, ts.URL, []*macaroon.Macaroon{serverMacaroon})
	// Somehow the client has accquired the macaroon. Add it to the cookiejar in our request.

	// Make the request to the server.
	resp, err := client.Do(req)
	c.Assert(err, gc.IsNil)
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	c.Assert(err, gc.IsNil)
	c.Assert(string(body), gc.DeepEquals, "done")
}

func newService(c *gc.C, location string, locator bakery.PublicKeyLocatorMap) *httpbakery.Service {
	keyPair, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)

	svc, err := httpbakery.NewService(bakery.NewServiceParams{
		Location: location,
		Store:    nil,
		Key:      keyPair,
		Locator:  locator,
	})
	c.Assert(err, gc.IsNil)
	if locator != nil {
		locator[location] = &keyPair.Public
	}
	return svc
}

func newServer(h func(http.ResponseWriter, *http.Request)) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/", h)
	return httptest.NewServer(mux)
}

func clientRequestWithCookies(c *gc.C, u string, macaroons []*macaroon.Macaroon) *http.Client {
	client := httpbakery.DefaultHTTPClient
	url, err := url.Parse(u)
	c.Assert(err, gc.IsNil)
	cookies, err := httpbakery.CookiesFromMacaroons(macaroons)
	c.Assert(err, gc.IsNil)
	client.Jar.SetCookies(url, cookies)
	return client
}

func serverHandler(service *httpbakery.Service) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, req *http.Request) {
		breq := service.NewRequest(req, strCompFirstPartyChecker("something"))
		if err := breq.Check(); err != nil {
			http.Error(w, "no macaroon", http.StatusUnauthorized)
			return
		}
		fmt.Fprintf(w, "done")
	}
}

type strCompFirstPartyChecker string

func (c strCompFirstPartyChecker) CheckFirstPartyCaveat(caveat string) error {
	if caveat != string(c) {
		return fmt.Errorf("%v doesn't match %s", caveat, c)
	}
	return nil
}
