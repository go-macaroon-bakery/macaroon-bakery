package httpbakery_test

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	gc "gopkg.in/check.v1"

	"gopkg.in/macaroon-bakery.v0/bakery"
	"gopkg.in/macaroon-bakery.v0/httpbakery"
	"gopkg.in/macaroon.v1"
)

type ServiceSuite struct{}

func Test(t *testing.T) {
	gc.TestingT(t)
}

var _ = gc.Suite(&ServiceSuite{})

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

// TestSingleServiceFirstParty creates a single service
// with a macaroon with one first party caveat.
// It creates a request with this macaroon and checks that the service
// can verify this macaroon as valid.
func (s *ServiceSuite) TestSingleServiceFirstParty(c *gc.C) {

	//create a target service
	svc := newService(c, "loc", nil)
	ts := newServer(serverHandler(svc))
	defer ts.Close()

	// mint a macaroon for the target service
	serverMacaroon, err := svc.NewMacaroon("", nil, nil)
	c.Assert(err, gc.IsNil)
	c.Assert(serverMacaroon.Location(), gc.Equals, "loc")
	cav := bakery.Caveat{
		Location:  "",
		Condition: "something",
	}
	err = svc.AddCaveat(serverMacaroon, cav)
	c.Assert(err, gc.IsNil)

	// create a client request
	req, err := http.NewRequest("GET", ts.URL, nil)
	c.Assert(err, gc.IsNil)
	client := clientRequestWithCookies(c, ts.URL, []*macaroon.Macaroon{serverMacaroon})
	// somehow the client has the macaroon add it to the cookiejar in our request.

	// make the request to the server.
	resp, err := httpbakery.Do(client, req, nil)
	c.Assert(err, gc.IsNil)
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	c.Assert(err, gc.IsNil)
	c.Assert(string(body), gc.DeepEquals, "done")
}

func clientRequestWithCookies(c *gc.C, u string, macaroons []*macaroon.Macaroon) *http.Client {
	client := httpbakery.DefaultHTTPClient
	url, err := url.Parse(u)
	c.Assert(err, gc.IsNil)
	cookies, err := httpbakery.CookiesForMacaroons(macaroons)
	client.Jar.SetCookies(url, cookies)
	return client
}

// TestMacaroonPaperFig6 implements an example flow as described in the macaroons paper:
// http://theory.stanford.edu/~ataly/Papers/macaroons.pdf
// There are three services, ts, fs, as:
// ts is a storage service which has deligated authority to a forum service fs.
// The forum service wants to require its users to be logged into to an authentication service as.
//
// The client obtains a macaroon from fs (minted by ts, with a third party caveat addressed to as).
// The client obtains a discharge macaroon from as to satisfy this caveat.
// The target service verifies the original macaroon it delegated to fs
// No direct contact between as and ts is required
func (s *ServiceSuite) TestMacaroonPaperFig6(c *gc.C) {
	locator := make(bakery.PublicKeyLocatorMap)
	as := newService(c, "as-loc", locator)
	ts := newService(c, "ts-loc", locator)
	fs := newService(c, "fs-loc", locator)

	targetServer := newServer(serverHandler(ts))
	defer targetServer.Close()

	// ts creates a macaroon.
	// ts somehow sends the macaroon to fs which adds a third party caveat to be discharged by as.
	tsMacaroon := createMacaroonWithThirdPartyCaveat(c, ts, fs, bakery.Caveat{Location: "as-loc", Condition: "user==bob"})

	// client asks for a discharge macaroon for each third party caveat
	d, err := bakery.DischargeAll(tsMacaroon, func(firstPartyLocation string, cav macaroon.Caveat) (*macaroon.Macaroon, error) {
		c.Assert(firstPartyLocation, gc.Equals, "ts-loc")
		c.Assert(cav.Location, gc.Equals, "as-loc")
		mac, err := as.Discharge(strCompThirdPartyChecker("user==bob"), cav.Id)
		c.Assert(err, gc.IsNil)
		return mac, nil
	})
	c.Assert(err, gc.IsNil)

	// client has all the discharge macaroons. For each discharge macaroon bind it to our tsMacaroon.
	for _, dm := range d {
		dm.Bind(tsMacaroon.Signature())
	}
	req, err := http.NewRequest("GET", targetServer.URL, nil)
	c.Assert(err, gc.IsNil)
	macaroons := append(d, tsMacaroon)
	client := clientRequestWithCookies(c, targetServer.URL, macaroons)
	// somehow the client has the macaroon add it to the cookiejar in our request.

	// make the request to the server.
	resp, err := httpbakery.Do(client, req, nil)
	c.Assert(err, gc.IsNil)
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	c.Assert(err, gc.IsNil)
	c.Assert(string(body), gc.DeepEquals, "done")
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

func createMacaroonWithThirdPartyCaveat(c *gc.C, minter, caveater *httpbakery.Service, cav bakery.Caveat) *macaroon.Macaroon {
	mac, err := minter.NewMacaroon("", nil, nil)
	c.Assert(err, gc.IsNil)

	err = caveater.AddCaveat(mac, cav)
	c.Assert(err, gc.IsNil)
	return mac
}

type strCompFirstPartyChecker string

func (c strCompFirstPartyChecker) CheckFirstPartyCaveat(caveat string) error {
	if caveat != string(c) {
		return fmt.Errorf("%v doesn't match %s", caveat, c)
	}
	return nil
}

type strCompThirdPartyChecker string

func (c strCompThirdPartyChecker) CheckThirdPartyCaveat(caveatId string, caveat string) ([]bakery.Caveat, error) {
	if caveat != string(c) {
		return nil, fmt.Errorf("%v doesn't match %s", caveat, c)
	}
	return nil, nil
}
