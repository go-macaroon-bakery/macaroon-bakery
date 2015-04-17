// Package bakerytest provides test helper functions for
// the bakery.
package bakerytest

import (
	"net/http"
	"net/http/httptest"

	"gopkg.in/macaroon-bakery.v1/bakery"
	"gopkg.in/macaroon-bakery.v1/bakery/checkers"
	"gopkg.in/macaroon-bakery.v1/httpbakery"
)

// Discharger is a third-party caveat discharger suitable
// for testing. It listens on a local network port for
// discharge requests. It should be shut down by calling
// Close when done with.
type Discharger struct {
	Service *bakery.Service

	server *httptest.Server
}

// NewDischarger returns a new third party caveat discharger
// which uses the given function to check caveats.
// The cond and arg arguments to the function are as returned
// by checkers.ParseCaveat.
//
// If locator is non-nil, it will be used to find public keys
// for any third party caveats returned by the checker.
func NewDischarger(
	locator bakery.PublicKeyLocator,
	checker func(req *http.Request, cond, arg string) ([]checkers.Caveat, error),
) *Discharger {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	svc, err := bakery.NewService(bakery.NewServiceParams{
		Location: server.URL,
		Locator:  locator,
	})
	if err != nil {
		panic(err)
	}
	checker1 := func(req *http.Request, cavId, cav string) ([]checkers.Caveat, error) {
		cond, arg, err := checkers.ParseCaveat(cav)
		if err != nil {
			return nil, err
		}
		return checker(req, cond, arg)
	}
	httpbakery.AddDischargeHandler(mux, "/", svc, checker1)
	return &Discharger{
		Service: svc,
		server:  server,
	}
}

// Close shuts down the server.
func (d *Discharger) Close() {
	d.server.Close()
}

// Location returns the location of the discharger, suitable
// for setting as the location in a third party caveat.
// This will be the URL of the server.
func (d *Discharger) Location() string {
	return d.Service.Location()
}

// PublicKeyForLocation implements bakery.PublicKeyLocator.
func (d *Discharger) PublicKeyForLocation(loc string) (*bakery.PublicKey, error) {
	if loc == d.Location() {
		return d.Service.PublicKey(), nil
	}
	return nil, bakery.ErrNotFound
}
