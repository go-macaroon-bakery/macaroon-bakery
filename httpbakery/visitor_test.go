package httpbakery_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"

	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	"golang.org/x/net/context"
	gc "gopkg.in/check.v1"

	"gopkg.in/macaroon-bakery.v2/httpbakery"
)

type InteractorSuite struct {
	jujutesting.LoggingSuite
}

var _ = gc.Suite(&InteractorSuite{})

func (*InteractorSuite) TestLegacyGetInteractionMethodsGetFailure(c *gc.C) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		w.Write([]byte("failure"))
	}))
	defer srv.Close()

	methods := httpbakery.LegacyGetInteractionMethods(testContext, nopLogger{}, http.DefaultClient, mustParseURL(srv.URL))
	// On error, it falls back to just the single default interactive method.
	c.Assert(methods, jc.DeepEquals, map[string]*url.URL{
		"interactive": mustParseURL(srv.URL),
	})
}

func (*InteractorSuite) TestLegacyGetInteractionMethodsSuccess(c *gc.C) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"method": "http://somewhere/something"}`)
	}))
	defer srv.Close()

	methods := httpbakery.LegacyGetInteractionMethods(testContext, nopLogger{}, http.DefaultClient, mustParseURL(srv.URL))
	c.Assert(methods, jc.DeepEquals, map[string]*url.URL{
		"interactive": mustParseURL(srv.URL),
		"method":      mustParseURL("http://somewhere/something"),
	})
}

func (*InteractorSuite) TestLegacyGetInteractionMethodsInvalidURL(c *gc.C) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"method": ":::"}`)
	}))
	defer srv.Close()

	methods := httpbakery.LegacyGetInteractionMethods(testContext, nopLogger{}, http.DefaultClient, mustParseURL(srv.URL))

	// On error, it falls back to just the single default interactive method.
	c.Assert(methods, jc.DeepEquals, map[string]*url.URL{
		"interactive": mustParseURL(srv.URL),
	})
}

type nopLogger struct{}

func (nopLogger) Debugf(context.Context, string, ...interface{}) {}
func (nopLogger) Infof(context.Context, string, ...interface{})  {}
