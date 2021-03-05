package httpbakery_test

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	qt "github.com/frankban/quicktest"
	"gopkg.in/errgo.v1"
	"gopkg.in/httprequest.v1"

	"gopkg.in/macaroon-bakery.v3/bakery"
	"gopkg.in/macaroon-bakery.v3/bakery/checkers"
	"gopkg.in/macaroon-bakery.v3/bakery/identchecker"
	"gopkg.in/macaroon-bakery.v3/bakerytest"
	"gopkg.in/macaroon-bakery.v3/httpbakery"
)

func TestOvenWithAuthnMacaroon(t *testing.T) {
	c := qt.New(t)
	discharger := newTestIdentityServer()
	defer discharger.Close()

	key, err := bakery.GenerateKey()
	if err != nil {
		panic(err)
	}
	b := identchecker.NewBakery(identchecker.BakeryParams{
		Location:       "here",
		Locator:        discharger,
		Key:            key,
		Checker:        httpbakery.NewChecker(),
		IdentityClient: discharger,
	})
	expectedExpiry := time.Hour
	oven := &httpbakery.Oven{
		Oven:        b.Oven,
		AuthnExpiry: expectedExpiry,
		AuthzExpiry: 5 * time.Minute,
	}
	errorCalled := 0
	handler := httpReqServer.HandleErrors(func(p httprequest.Params) error {
		if _, err := b.Checker.Auth(httpbakery.RequestMacaroons(p.Request)...).Allow(p.Context, identchecker.LoginOp); err != nil {
			errorCalled++
			return oven.Error(testContext, p.Request, err)
		}
		fmt.Fprintf(p.Response, "done")
		return nil
	})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		handler(w, req, nil)
	}))
	defer ts.Close()
	req, err := http.NewRequest("GET", ts.URL, nil)
	c.Assert(err, qt.Equals, nil)
	client := httpbakery.NewClient()
	t0 := time.Now()
	resp, err := client.Do(req)
	c.Assert(err, qt.Equals, nil)
	c.Check(errorCalled, qt.Equals, 1)
	body, _ := ioutil.ReadAll(resp.Body)
	c.Assert(resp.StatusCode, qt.Equals, http.StatusOK, qt.Commentf("body: %q", body))
	mss := httpbakery.MacaroonsForURL(client.Jar, mustParseURL(discharger.Location()))
	c.Assert(mss, qt.HasLen, 1)
	t1, ok := checkers.MacaroonsExpiryTime(b.Checker.Namespace(), mss[0])
	c.Assert(ok, qt.Equals, true)
	want := t0.Add(expectedExpiry)
	if t1.Before(want) || t1.After(want.Add(time.Second)) {
		c.Fatalf("time out of range; got %v want %v", t1, want)
	}
}

func TestOvenWithAuthzMacaroon(t *testing.T) {
	c := qt.New(t)
	discharger := newTestIdentityServer()
	defer discharger.Close()
	discharger2 := bakerytest.NewDischarger(nil)
	defer discharger2.Close()

	locator := httpbakery.NewThirdPartyLocator(nil, nil)
	locator.AllowInsecure()

	key, err := bakery.GenerateKey()
	if err != nil {
		panic(err)
	}
	b := identchecker.NewBakery(identchecker.BakeryParams{
		Location:       "here",
		Locator:        locator,
		Key:            key,
		Checker:        httpbakery.NewChecker(),
		IdentityClient: discharger,
		Authorizer: identchecker.AuthorizerFunc(func(ctx context.Context, id identchecker.Identity, op bakery.Op) (bool, []checkers.Caveat, error) {
			if id == nil {
				return false, nil, nil
			}
			return true, []checkers.Caveat{{
				Location:  discharger2.Location(),
				Condition: "something",
			}}, nil
		}),
	})
	expectedAuthnExpiry := 5 * time.Minute
	expectedAuthzExpiry := time.Hour
	oven := &httpbakery.Oven{
		Oven:        b.Oven,
		AuthnExpiry: expectedAuthnExpiry,
		AuthzExpiry: expectedAuthzExpiry,
	}
	errorCalled := 0
	handler := httpReqServer.HandleErrors(func(p httprequest.Params) error {
		if _, err := b.Checker.Auth(httpbakery.RequestMacaroons(p.Request)...).Allow(p.Context, bakery.Op{"something", "read"}); err != nil {
			errorCalled++
			return oven.Error(testContext, p.Request, err)
		}
		fmt.Fprintf(p.Response, "done")
		return nil
	})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		handler(w, req, nil)
	}))
	defer ts.Close()
	req, err := http.NewRequest("GET", ts.URL, nil)
	c.Assert(err, qt.Equals, nil)
	client := httpbakery.NewClient()
	t0 := time.Now()
	resp, err := client.Do(req)
	c.Assert(err, qt.Equals, nil)
	c.Check(errorCalled, qt.Equals, 2)
	body, _ := ioutil.ReadAll(resp.Body)
	c.Assert(resp.StatusCode, qt.Equals, http.StatusOK, qt.Commentf("body: %q", body))

	cookies := client.Jar.Cookies(mustParseURL(discharger.Location()))
	for i, cookie := range cookies {
		c.Logf("cookie %d: %s %q", i, cookie.Name, cookie.Value)
	}
	mss := httpbakery.MacaroonsForURL(client.Jar, mustParseURL(discharger.Location()))
	c.Assert(mss, qt.HasLen, 2)

	// The cookie jar returns otherwise-similar cookies in the order
	// they were added, so the authn macaroon will be first.
	t1, ok := checkers.MacaroonsExpiryTime(b.Checker.Namespace(), mss[0])
	c.Assert(ok, qt.Equals, true)
	want := t0.Add(expectedAuthnExpiry)
	if t1.Before(want) || t1.After(want.Add(time.Second)) {
		c.Fatalf("time out of range; got %v want %v", t1, want)
	}

	t1, ok = checkers.MacaroonsExpiryTime(b.Checker.Namespace(), mss[1])
	c.Assert(ok, qt.Equals, true)
	want = t0.Add(expectedAuthzExpiry)
	if t1.Before(want) || t1.After(want.Add(time.Second)) {
		c.Fatalf("time out of range; got %v want %v", t1, want)
	}
}

type testIdentityServer struct {
	*bakerytest.Discharger
}

func newTestIdentityServer() *testIdentityServer {
	checker := func(ctx context.Context, p httpbakery.ThirdPartyCaveatCheckerParams) ([]checkers.Caveat, error) {
		if string(p.Caveat.Condition) != "is-authenticated-user" {
			return nil, errgo.New("unexpected caveat")
		}
		return []checkers.Caveat{
			checkers.DeclaredCaveat("username", "bob"),
		}, nil
	}
	discharger := bakerytest.NewDischarger(nil)
	discharger.CheckerP = httpbakery.ThirdPartyCaveatCheckerPFunc(checker)
	return &testIdentityServer{
		Discharger: discharger,
	}
}

func (s *testIdentityServer) IdentityFromContext(ctx context.Context) (identchecker.Identity, []checkers.Caveat, error) {
	return nil, []checkers.Caveat{{
		Location:  s.Location(),
		Condition: "is-authenticated-user",
	}}, nil
}

func (s *testIdentityServer) DeclaredIdentity(ctx context.Context, declared map[string]string) (identchecker.Identity, error) {
	username, ok := declared["username"]
	if !ok {
		return nil, errgo.New("no username declared")
	}
	return identchecker.SimpleIdentity(username), nil
}
