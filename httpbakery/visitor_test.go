package httpbakery_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"

	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"gopkg.in/macaroon-bakery.v1/httpbakery"
)

type VisitorSuite struct {
	jujutesting.LoggingSuite
}

var _ = gc.Suite(&VisitorSuite{})

func (*VisitorSuite) TestGetInteractionMethodsGetFailure(c *gc.C) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		w.Write([]byte("failure"))
	}))
	defer srv.Close()

	methods, err := httpbakery.GetInteractionMethods(http.DefaultClient, mustParseURL(srv.URL))
	c.Assert(methods, gc.IsNil)
	c.Assert(err, gc.ErrorMatches, `GET .*: cannot unmarshal error response \(status 418 I'm a teapot\): unexpected content type text/plain; want application/json; content: failure`)
}

func (*VisitorSuite) TestGetInteractionMethodsSuccess(c *gc.C) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"method": "http://somewhere/something"}`)
	}))
	defer srv.Close()

	methods, err := httpbakery.GetInteractionMethods(http.DefaultClient, mustParseURL(srv.URL))
	c.Assert(err, gc.IsNil)
	c.Assert(methods, jc.DeepEquals, map[string]*url.URL{
		"method": {
			Scheme: "http",
			Host:   "somewhere",
			Path:   "/something",
		},
	})
}

func (*VisitorSuite) TestGetInteractionMethodsInvalidURL(c *gc.C) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"method": ":::"}`)
	}))
	defer srv.Close()

	methods, err := httpbakery.GetInteractionMethods(http.DefaultClient, mustParseURL(srv.URL))
	c.Assert(methods, gc.IsNil)
	c.Assert(err, gc.ErrorMatches, `invalid URL for interaction method "method": parse :::: missing protocol scheme`)
}

func (*VisitorSuite) TestMultiVisitorNoUserInteractionMethod(c *gc.C) {
	v := httpbakery.NewMultiVisitor()
	err := v.VisitWebPage(httpbakery.NewClient(), nil)
	c.Assert(err, gc.ErrorMatches, `cannot get interaction methods because no "interactive" URL found`)
}

func (*VisitorSuite) TestMultiVisitorNoInteractionMethods(c *gc.C) {
	initialPage := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		initialPage++
		fmt.Fprint(w, `<html>oh yes</html>`)
	}))
	defer srv.Close()
	methods := map[string]*url.URL{
		httpbakery.UserInteractionMethod: mustParseURL(srv.URL),
	}
	visited := 0
	v := httpbakery.NewMultiVisitor(
		visitorFunc(func(_ *httpbakery.Client, m map[string]*url.URL) error {
			c.Check(m, jc.DeepEquals, methods)
			visited++
			return nil
		}),
	)
	err := v.VisitWebPage(httpbakery.NewClient(), methods)
	c.Assert(err, gc.IsNil)
	c.Assert(initialPage, gc.Equals, 1)
	c.Assert(visited, gc.Equals, 1)
}

func (*VisitorSuite) TestMultiVisitorSequence(c *gc.C) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"method": "http://somewhere/something"}`)
	}))
	defer srv.Close()

	firstCalled, secondCalled := 0, 0
	v := httpbakery.NewMultiVisitor(
		visitorFunc(func(_ *httpbakery.Client, m map[string]*url.URL) error {
			c.Check(m["method"], gc.NotNil)
			firstCalled++
			return httpbakery.ErrMethodNotSupported
		}),
		visitorFunc(func(_ *httpbakery.Client, m map[string]*url.URL) error {
			c.Check(m["method"], gc.NotNil)
			secondCalled++
			return nil
		}),
	)
	err := v.VisitWebPage(httpbakery.NewClient(), map[string]*url.URL{
		httpbakery.UserInteractionMethod: mustParseURL(srv.URL),
	})
	c.Assert(err, gc.IsNil)
	c.Assert(firstCalled, gc.Equals, 1)
	c.Assert(secondCalled, gc.Equals, 1)
}

func (*VisitorSuite) TestUserInteractionFallback(c *gc.C) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"method": "http://somewhere/something"}`)
	}))
	defer srv.Close()

	called := 0
	// Check that even though the methods didn't explicitly
	// include the "interactive" method, it is still supplied.
	v := httpbakery.NewMultiVisitor(
		visitorFunc(func(_ *httpbakery.Client, m map[string]*url.URL) error {
			c.Check(m, jc.DeepEquals, map[string]*url.URL{
				"method": mustParseURL("http://somewhere/something"),
				httpbakery.UserInteractionMethod: mustParseURL(srv.URL),
			})
			called++
			return nil
		}),
	)
	err := v.VisitWebPage(httpbakery.NewClient(), map[string]*url.URL{
		httpbakery.UserInteractionMethod: mustParseURL(srv.URL),
	})
	c.Assert(err, gc.IsNil)
	c.Assert(called, gc.Equals, 1)
}

type visitorFunc func(*httpbakery.Client, map[string]*url.URL) error

func (f visitorFunc) VisitWebPage(c *httpbakery.Client, m map[string]*url.URL) error {
	return f(c, m)
}
