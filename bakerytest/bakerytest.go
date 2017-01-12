// Package bakerytest provides test helper functions for
// the bakery.
package bakerytest

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/juju/httprequest"
	"golang.org/x/net/context"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery"
)

// NoCaveatChecker is a third party caveat checker that
// always allows any caveat and adds no third party caveats.
var NoCaveatChecker = httpbakery.ThirdPartyCaveatCheckerFunc(func(ctx context.Context, req *http.Request, info *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
	return nil, nil
})

// Discharger is a third-party caveat discharger suitable
// for testing. It listens on a local network port for
// discharge requests. It should be shut down by calling
// Close when done with.
type Discharger struct {
	Key     *bakery.KeyPair
	Locator bakery.ThirdPartyLocator

	server *httptest.Server
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

// NewDischarger returns a new third party caveat discharger
// which uses the given function to check caveats.
//
// If locator is non-nil, it will be used to find public keys
// for any third party caveats returned by the checker.
//
// Calling this function has the side-effect of setting
// InsecureSkipVerify in http.DefaultTransport.TLSClientConfig
// until all the dischargers are closed.
//
// If checker is nil, NoCaveatChecker will be used.
func NewDischarger(
	locator bakery.ThirdPartyLocator,
	checker httpbakery.ThirdPartyCaveatChecker,
) *Discharger {
	mux := http.NewServeMux()
	server := httptest.NewTLSServer(mux)
	key, err := bakery.GenerateKey()
	if err != nil {
		panic(err)
	}
	if checker == nil {
		checker = NoCaveatChecker
	}
	d := httpbakery.NewDischarger(httpbakery.DischargerParams{
		Key:     key,
		Locator: locator,
		Checker: checker,
	})
	d.AddMuxHandlers(mux, "/")
	startSkipVerify()
	return &Discharger{
		Key:     key,
		Locator: locator,
		server:  server,
	}
}

// ConditionParser adapts the given function into a httpbakery.ThirdPartyCaveatChecker.
// It parses the caveat's condition and calls the function with the result.
func ConditionParser(check func(cond, arg string) ([]checkers.Caveat, error)) httpbakery.ThirdPartyCaveatChecker {
	f := func(ctx context.Context, req *http.Request, cav *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
		cond, arg, err := checkers.ParseCaveat(string(cav.Condition))
		if err != nil {
			return nil, err
		}
		return check(cond, arg)
	}
	return httpbakery.ThirdPartyCaveatCheckerFunc(f)
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

// PublicKeyForLocation implements bakery.PublicKeyLocator.
func (d *Discharger) ThirdPartyInfo(ctxt context.Context, loc string) (bakery.ThirdPartyInfo, error) {
	if loc == d.Location() {
		return bakery.ThirdPartyInfo{
			PublicKey: d.Key.Public,
			Version:   bakery.LatestVersion,
		}, nil
	}
	return bakery.ThirdPartyInfo{}, bakery.ErrNotFound
}

type dischargeResult struct {
	err  error
	cavs []checkers.Caveat
}

type discharge struct {
	caveatInfo *bakery.ThirdPartyCaveatInfo
	c          chan dischargeResult
}

// InteractiveDischarger is a Discharger that requires interaction to
// complete the discharge. The SetChecker method can be used
// to avoid interaction sometimes.
type InteractiveDischarger struct {
	Discharger
	Mux *http.ServeMux

	// mu protects the following fields.
	mu      sync.Mutex
	waiting map[string]discharge
	id      int
	checker httpbakery.ThirdPartyCaveatChecker
}

// NewInteractiveDischarger returns a new InteractiveDischarger. The
// InteractiveDischarger will serve the following endpoints by default:
//
//     /discharge - always causes interaction to be required.
//     /publickey - gets the bakery public key.
//     /visit - delegates to visitHandler.
//     /wait - blocks waiting for the interaction to complete.
//
// Additional endpoints may be added to Mux as necessary.
//
// The /discharge endpoint generates a error with the code
// httpbakery.ErrInteractionRequired. The visitURL and waitURL will
// point to the /visit and /wait endpoints of the InteractiveDischarger
// respectively. These URLs will also carry context information in query
// parameters, any handlers should be careful to preserve this context
// information between calls. The easiest way to do this is to always use
// the URL method when generating new URLs.
//
// The /visit endpoint is handled by the provided visitHandler. This
// handler performs the required interactions and should result in the
// FinishInteraction method being called. This handler may process the
// interaction in a number of steps, possibly using additional handlers,
// so long as FinishInteraction is called when no further interaction is
// required.
//
// The /wait endpoint blocks until FinishInteraction has been called.
//
// If locator is non-nil, it will be used to find public keys
// for any third party caveats returned by the checker.
//
// Calling this function has the side-effect of setting
// InsecureSkipVerify in http.DefaultTransport.TLSClientConfig
// until all the dischargers are closed.
//
// The returned InteractiveDischarger must be closed when finished with.
func NewInteractiveDischarger(locator bakery.ThirdPartyLocator, visitHandler http.Handler) *InteractiveDischarger {
	d := &InteractiveDischarger{
		Mux:     http.NewServeMux(),
		waiting: map[string]discharge{},
	}
	d.Mux.Handle("/visit", visitHandler)
	d.Mux.Handle("/wait", http.HandlerFunc(d.wait))
	server := httptest.NewTLSServer(d.Mux)

	key, err := bakery.GenerateKey()
	if err != nil {
		panic(err)
	}
	bd := httpbakery.NewDischarger(httpbakery.DischargerParams{
		Key:     key,
		Locator: locator,
		Checker: httpbakery.ThirdPartyCaveatCheckerFunc(d.checkThirdPartyCaveat),
	})
	bd.AddMuxHandlers(d.Mux, "/")
	startSkipVerify()
	d.Discharger = Discharger{
		Key:     key,
		Locator: locator,
		server:  server,
	}
	return d
}

func (d *InteractiveDischarger) checkThirdPartyCaveat(ctx context.Context, req *http.Request, cav *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
	d.mu.Lock()
	checker := d.checker
	d.mu.Unlock()
	if checker == nil {
		checker = d
	}
	return checker.CheckThirdPartyCaveat(ctx, req, cav)
}

// SetChecker sets a checker that will be used to check third party caveats.
// The checker may call d.CheckThirdPartyCaveat if it decides to discharge interactively.
func (d *InteractiveDischarger) SetChecker(c httpbakery.ThirdPartyCaveatChecker) {
	d.mu.Lock()
	d.checker = c
	d.mu.Unlock()
}

// CheckThirdPartyCaveat implements httpbakery.ThirdPartyCaveatDischarger
// by always returning an interaction-required error.
func (d *InteractiveDischarger) CheckThirdPartyCaveat(ctxt context.Context, req *http.Request, cav *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
	d.mu.Lock()
	id := fmt.Sprintf("%d", d.id)
	d.id++
	d.waiting[id] = discharge{
		caveatInfo: cav,
		c:          make(chan dischargeResult, 1),
	}
	d.mu.Unlock()
	visitURL := "/visit?waitid=" + id
	waitURL := "/wait?waitid=" + id
	return nil, httpbakery.NewInteractionRequiredError(visitURL, waitURL, nil, req)
}

var dischargeNamespace = httpbakery.NewChecker().Namespace()

func (d *InteractiveDischarger) wait(w http.ResponseWriter, r *http.Request) {
	ctx := context.TODO()
	r.ParseForm()
	d.mu.Lock()
	discharge, ok := d.waiting[r.Form.Get("waitid")]
	d.mu.Unlock()
	if !ok {
		code, body := httpbakery.ErrorToResponse(ctx, errgo.Newf("invalid waitid %q", r.Form.Get("waitid")))
		httprequest.WriteJSON(w, code, body)
		return
	}
	defer func() {
		d.mu.Lock()
		delete(d.waiting, r.Form.Get("waitid"))
		d.mu.Unlock()
	}()
	var err error
	var cavs []checkers.Caveat
	select {
	case res := <-discharge.c:
		err = res.err
		cavs = res.cavs
	case <-time.After(5 * time.Minute):
		code, body := httpbakery.ErrorToResponse(ctx, errgo.New("timeout waiting for interaction to complete"))
		httprequest.WriteJSON(w, code, body)
		return
	}
	if err != nil {
		code, body := httpbakery.ErrorToResponse(ctx, err)
		httprequest.WriteJSON(w, code, body)
		return
	}
	check := bakery.ThirdPartyCaveatCheckerFunc(func(_ context.Context, cav *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
		return cavs, nil
	})
	m, err := bakery.Discharge(context.Background(), bakery.DischargeParams{
		Id:      discharge.caveatInfo.Id,
		Caveat:  discharge.caveatInfo.Caveat,
		Key:     d.Key,
		Checker: check,
		Locator: d.Locator,
	})
	if err != nil {
		code, body := httpbakery.ErrorToResponse(ctx, err)
		httprequest.WriteJSON(w, code, body)
		return
	}

	httprequest.WriteJSON(
		w,
		http.StatusOK,
		httpbakery.WaitResponse{
			Macaroon: m,
		},
	)
}

// FinishInteraction signals to the InteractiveDischarger that a
// particular interaction is complete. It causes any waiting requests to
// return. If err is not nil then it will be returned by the
// corresponding /wait request.
func (d *InteractiveDischarger) FinishInteraction(ctx context.Context, w http.ResponseWriter, r *http.Request, cavs []checkers.Caveat, err error) {
	r.ParseForm()
	d.mu.Lock()
	discharge, ok := d.waiting[r.Form.Get("waitid")]
	d.mu.Unlock()
	if !ok {
		code, body := httpbakery.ErrorToResponse(ctx, errgo.Newf("invalid waitid %q", r.Form.Get("waitid")))
		httprequest.WriteJSON(w, code, body)
		return
	}
	select {
	case discharge.c <- dischargeResult{err: err, cavs: cavs}:
	default:
		panic("cannot finish interaction " + r.Form.Get("waitid"))
	}
	return
}

// HostRelativeURL is like URL but includes only the
// URL path and query parameters. Use this when returning
// a URL for use in GetInteractionMethods.
func (d *InteractiveDischarger) HostRelativeURL(path string, r *http.Request) string {
	r.ParseForm()
	return path + "?waitid=" + r.Form.Get("waitid")
}

// URL returns a URL addressed to the given path in the discharger that
// contains any discharger context information found in the given
// request. Use this to generate intermediate URLs before calling
// FinishInteraction.
func (d *InteractiveDischarger) URL(path string, r *http.Request) string {
	r.ParseForm()
	return d.Location() + d.HostRelativeURL(path, r)
}
