package main

import (
	"fmt"
	"net/http"
	"strings"

	"golang.org/x/net/context"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2/bakery/identchecker"
	"gopkg.in/macaroon-bakery.v2/httpbakery"
)

type targetServiceHandler struct {
	checker      *identchecker.Checker
	oven         *httpbakery.Oven
	authEndpoint string
	endpoint     string
	mux          *http.ServeMux
}

// targetService implements a "target service", representing
// an arbitrary web service that wants to delegate authorization
// to third parties.
//
func targetService(endpoint, authEndpoint string, authPK *bakery.PublicKey) (http.Handler, error) {
	key, err := bakery.GenerateKey()
	if err != nil {
		return nil, err
	}
	pkLocator := httpbakery.NewThirdPartyLocator(nil, nil)
	pkLocator.AllowInsecure()
	b := identchecker.NewBakery(identchecker.BakeryParams{
		Key:      key,
		Location: endpoint,
		Locator:  pkLocator,
		Checker:  httpbakery.NewChecker(),
		Authorizer: authorizer{
			thirdPartyLocation: authEndpoint,
		},
	})
	mux := http.NewServeMux()
	srv := &targetServiceHandler{
		checker:      b.Checker,
		oven:         &httpbakery.Oven{Oven: b.Oven},
		authEndpoint: authEndpoint,
	}
	mux.Handle("/gold/", srv.auth(http.HandlerFunc(srv.serveGold)))
	mux.Handle("/silver/", srv.auth(http.HandlerFunc(srv.serveSilver)))
	return mux, nil
}

func (srv *targetServiceHandler) serveGold(w http.ResponseWriter, req *http.Request) {
	fmt.Fprintf(w, "all is golden")
}

func (srv *targetServiceHandler) serveSilver(w http.ResponseWriter, req *http.Request) {
	fmt.Fprintf(w, "every cloud has a silver lining")
}

// auth wraps the given handler with a handler that provides
// authorization by inspecting the HTTP request
// to decide what authorization is required.
func (srv *targetServiceHandler) auth(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := httpbakery.ContextWithRequest(context.TODO(), req)
		ops, err := opsForRequest(req)
		if err != nil {
			fail(w, http.StatusInternalServerError, "%v", err)
			return
		}
		authChecker := srv.checker.Auth(httpbakery.RequestMacaroons(req)...)
		if _, err = authChecker.Allow(ctx, ops...); err != nil {
			httpbakery.WriteError(ctx, w, srv.oven.Error(ctx, req, err))
			return
		}
		h.ServeHTTP(w, req)
	})
}

// opsForRequest returns the required operations
// implied by the given HTTP request.
func opsForRequest(req *http.Request) ([]bakery.Op, error) {
	if !strings.HasPrefix(req.URL.Path, "/") {
		return nil, errgo.Newf("bad path")
	}
	elems := strings.Split(req.URL.Path, "/")
	if len(elems) < 2 {
		return nil, errgo.Newf("bad path")
	}
	return []bakery.Op{{
		Entity: elems[1],
		Action: req.Method,
	}}, nil
}

func fail(w http.ResponseWriter, code int, msg string, args ...interface{}) {
	http.Error(w, fmt.Sprintf(msg, args...), code)
}

type authorizer struct {
	thirdPartyLocation string
}

// Authorize implements bakery.Authorizer.Authorize by
// allowing anyone to do anything if a third party
// approves it.
func (a authorizer) Authorize(ctx context.Context, id identchecker.Identity, ops []bakery.Op) (allowed []bool, caveats []checkers.Caveat, err error) {
	allowed = make([]bool, len(ops))
	for i := range allowed {
		allowed[i] = true
	}
	caveats = []checkers.Caveat{{
		Location:  a.thirdPartyLocation,
		Condition: "access-allowed",
	}}
	return
}
