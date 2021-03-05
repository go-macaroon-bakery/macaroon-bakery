package httpbakery_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	qt "github.com/frankban/quicktest"

	"gopkg.in/macaroon-bakery.v3/httpbakery"
)

func TestLegacyGetInteractionMethodsGetFailure(t *testing.T) {
	c := qt.New(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		w.Write([]byte("failure"))
	}))
	defer srv.Close()

	methods := httpbakery.LegacyGetInteractionMethods(testContext, nopLogger{}, http.DefaultClient, mustParseURL(srv.URL))
	// On error, it falls back to just the single default interactive method.
	c.Assert(methods, qt.DeepEquals, map[string]*url.URL{
		"interactive": mustParseURL(srv.URL),
	})
}

func TestLegacyGetInteractionMethodsSuccess(t *testing.T) {
	c := qt.New(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"method": "http://somewhere/something"}`)
	}))
	defer srv.Close()

	methods := httpbakery.LegacyGetInteractionMethods(testContext, nopLogger{}, http.DefaultClient, mustParseURL(srv.URL))
	c.Assert(methods, qt.DeepEquals, map[string]*url.URL{
		"interactive": mustParseURL(srv.URL),
		"method":      mustParseURL("http://somewhere/something"),
	})
}

func TestLegacyGetInteractionMethodsInvalidURL(t *testing.T) {
	c := qt.New(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"method": ":::"}`)
	}))
	defer srv.Close()

	methods := httpbakery.LegacyGetInteractionMethods(testContext, nopLogger{}, http.DefaultClient, mustParseURL(srv.URL))

	// On error, it falls back to just the single default interactive method.
	c.Assert(methods, qt.DeepEquals, map[string]*url.URL{
		"interactive": mustParseURL(srv.URL),
	})
}

type nopLogger struct{}

func (nopLogger) Debugf(context.Context, string, ...interface{}) {}
func (nopLogger) Infof(context.Context, string, ...interface{})  {}
