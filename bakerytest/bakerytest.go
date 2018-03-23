// Package bakerytest provides test helper functions for
// the bakery.
package bakerytest

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/julienschmidt/httprouter"
	"golang.org/x/net/context"
	"gopkg.in/errgo.v1"
	"gopkg.in/httprequest.v1"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2/httpbakery"
)

// Discharger represents a third party caveat discharger server.
type Discharger struct {
	server *httptest.Server

	// Mux holds the HTTP multiplexor used by
	// the discharger server.
	Mux *httprouter.Router

	// Key holds the discharger's private key.
	Key *bakery.KeyPair

	// Locator holds the third party locator
	// used when adding a third party caveat
	// returned by a third party caveat checker.
	Locator bakery.ThirdPartyLocator

	// CheckerP is called to check third party caveats when they're
	// discharged. It defaults to NopThirdPartyCaveatCheckerP.
	CheckerP httpbakery.ThirdPartyCaveatCheckerP

	// Checker is the deprecated version of CheckerP, and will be
	// ignored if CheckerP is non-nil.
	Checker httpbakery.ThirdPartyCaveatChecker
}

// NewDischarger returns a new discharger server that can be used to
// discharge third party caveats. It uses the given locator to add third
// party caveats returned by the Checker. The discharger also acts as a
// locator, returning locator information for itself only.
//
// The returned discharger should be closed after use.
//
// This should not be used concurrently unless httpbakery.AllowInsecureThirdPartyLocator
// is set, because otherwise it needs to run a TLS server and modify http.DefaultTransport
// to allow insecure connections.
func NewDischarger(locator bakery.ThirdPartyLocator) *Discharger {
	key, err := bakery.GenerateKey()
	if err != nil {
		panic(err)
	}
	d := &Discharger{
		Mux:     httprouter.New(),
		Key:     key,
		Locator: locator,
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		d.Mux.ServeHTTP(w, req)
	})
	if httpbakery.AllowInsecureThirdPartyLocator {
		d.server = httptest.NewServer(handler)
	} else {
		d.server = httptest.NewTLSServer(handler)
		startSkipVerify()
	}
	bd := httpbakery.NewDischarger(httpbakery.DischargerParams{
		Key:      key,
		Locator:  locator,
		CheckerP: d,
	})
	d.AddHTTPHandlers(bd.Handlers())
	return d
}

// AddHTTPHandlers adds the given HTTP handlers to the
// set of endpoints handled by the discharger.
func (d *Discharger) AddHTTPHandlers(hs []httprequest.Handler) {
	for _, h := range hs {
		d.Mux.Handle(h.Method, h.Path, h.Handle)
	}
}

// Close shuts down the server. It may be called more than
// once on the same discharger.
func (d *Discharger) Close() {
	if d.server == nil {
		return
	}
	d.server.Close()
	stopSkipVerify()
	d.server = nil
}

// Location returns the location of the discharger, suitable
// for setting as the location in a third party caveat.
// This will be the URL of the server.
func (d *Discharger) Location() string {
	return d.server.URL
}

// PublicKeyForLocation implements bakery.PublicKeyLocator
// by returning information on the discharger's server location
// only.
func (d *Discharger) ThirdPartyInfo(ctx context.Context, loc string) (bakery.ThirdPartyInfo, error) {
	if loc == d.Location() {
		return bakery.ThirdPartyInfo{
			PublicKey: d.Key.Public,
			Version:   bakery.LatestVersion,
		}, nil
	}
	return bakery.ThirdPartyInfo{}, bakery.ErrNotFound
}

// DischargeMacaroon returns a discharge macaroon
// for the given caveat information with the given
// caveats added. It assumed the actual third party
// caveat has already been checked.
func (d *Discharger) DischargeMacaroon(
	ctx context.Context,
	cav *bakery.ThirdPartyCaveatInfo,
	caveats []checkers.Caveat,
) (*bakery.Macaroon, error) {
	return bakery.Discharge(ctx, bakery.DischargeParams{
		Id:     cav.Id,
		Caveat: cav.Caveat,
		Key:    d.Key,
		Checker: bakery.ThirdPartyCaveatCheckerFunc(func(ctx context.Context, cav *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
			return caveats, nil
		}),
		Locator: d.Locator,
	})
}

var ErrTokenNotRecognized = errgo.New("discharge token not recognized")

// CheckThirdPartyCaveat implements httpbakery.ThirdPartyCaveatCheckerP
// by calling d.CheckerP, or d.Checker if that's nil.
func (d *Discharger) CheckThirdPartyCaveat(ctx context.Context, p httpbakery.ThirdPartyCaveatCheckerParams) ([]checkers.Caveat, error) {
	if d.CheckerP != nil {
		return d.CheckerP.CheckThirdPartyCaveat(ctx, p)
	}
	if d.Checker == nil {
		return nil, nil
	}
	return d.Checker.CheckThirdPartyCaveat(ctx, p.Caveat, p.Request, p.Token)
}

// ConditionParser adapts the given function into an httpbakery.ThirdPartyCaveatCheckerP.
// It parses the caveat's condition and calls the function with the result.
func ConditionParser(check func(cond, arg string) ([]checkers.Caveat, error)) httpbakery.ThirdPartyCaveatCheckerP {
	f := func(ctx context.Context, p httpbakery.ThirdPartyCaveatCheckerParams) ([]checkers.Caveat, error) {
		cond, arg, err := checkers.ParseCaveat(string(p.Caveat.Condition))
		if err != nil {
			return nil, err
		}
		return check(cond, arg)
	}
	return httpbakery.ThirdPartyCaveatCheckerPFunc(f)
}

// ConditionParserP adapts the given function into an httpbakery.ThirdPartyCaveatChecker.
// It parses the caveat's condition and calls the function with the result.
func ConditionParserP(check func(cond, arg string) ([]checkers.Caveat, error)) httpbakery.ThirdPartyCaveatChecker {
	f := func(ctx context.Context, req *http.Request, cav *bakery.ThirdPartyCaveatInfo, token *httpbakery.DischargeToken) ([]checkers.Caveat, error) {
		cond, arg, err := checkers.ParseCaveat(string(cav.Condition))
		if err != nil {
			return nil, err
		}
		return check(cond, arg)
	}
	return httpbakery.ThirdPartyCaveatCheckerFunc(f)
}

var skipVerify struct {
	mu            sync.Mutex
	refCount      int
	oldSkipVerify bool
}

func startSkipVerify() {
	v := &skipVerify
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.refCount++; v.refCount > 1 {
		return
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return
	}
	if transport.TLSClientConfig != nil {
		v.oldSkipVerify = transport.TLSClientConfig.InsecureSkipVerify
		transport.TLSClientConfig.InsecureSkipVerify = true
	} else {
		v.oldSkipVerify = false
		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
	}
}

func stopSkipVerify() {
	v := &skipVerify
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.refCount--; v.refCount > 0 {
		return
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return
	}
	// technically this doesn't return us to the original state,
	// as TLSClientConfig may have been nil before but won't
	// be now, but that should be equivalent.
	transport.TLSClientConfig.InsecureSkipVerify = v.oldSkipVerify
}
