package httpbakery

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"path"

	"github.com/juju/httprequest"
	"github.com/julienschmidt/httprouter"
	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v2-unstable"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
)

// ThirdPartyChecker is used to check third party caveats.
type ThirdPartyChecker interface {
	// CheckThirdPartyCaveat is used to check whether a client
	// making the given request should be allowed a discharge for
	// the given caveat. On success, the caveat will be discharged,
	// with any returned caveats also added to the discharge
	// macaroon.
	//
	// Note than when used in the context of a discharge handler
	// created by Discharger, any returned errors will be marshaled
	// as documented in DischargeHandler.ErrorMapper.
	CheckThirdPartyCaveat(req *http.Request, info *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error)
}

// ThirdPartyCheckerFunc implements ThirdPartyChecker
// by calling a function.
type ThirdPartyCheckerFunc func(req *http.Request, info *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error)

func (f ThirdPartyCheckerFunc) CheckThirdPartyCaveat(req *http.Request, info *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
	return f(req, info)
}

// newDischargeClient returns a discharge client that addresses the
// third party discharger at the given location URL and uses
// the given client to make HTTP requests.
//
// If client is nil, http.DefaultClient is used.
func newDischargeClient(location string, client httprequest.Doer) *dischargeClient {
	if client == nil {
		client = http.DefaultClient
	}
	return &dischargeClient{
		Client: httprequest.Client{
			BaseURL:        location,
			Doer:           client,
			UnmarshalError: unmarshalError,
		},
	}
}

// Discharger holds parameters for creating a new Discharger.
type DischargerParams struct {
	// Checker is used to actually check the caveats.
	Checker ThirdPartyChecker

	// Key holds the key pair of the discharger.
	Key *bakery.KeyPair

	// Locator is used to find public keys when adding
	// third-party caveats on discharge macaroons.
	// If this is nil, no third party caveats may be added.
	Locator bakery.ThirdPartyLocator

	// ErrorToResponse is used to convert errors returned by the third
	// party caveat checker to the form that will be JSON-marshaled
	// on the wire. If zero, this defaults to ErrorToResponse.
	// If set, it should handle errors that it does not understand
	// by falling back to calling ErrorToResponse to ensure
	// that the standard bakery errors are marshaled in the expected way.
	ErrorToResponse func(err error) (int, interface{})
}

// Discharger represents a third-party caveat discharger.
// can discharge caveats in an HTTP server.
//
// The name space served by dischargers is as follows.
// All parameters can be provided either as URL attributes
// or form attributes. The result is always formatted as a JSON
// object.
//
// On failure, all endpoints return an error described by
// the Error type.
//
// POST /discharge
//	params:
//		id: all-UTF-8 third party caveat id
//		id64: non-padded URL-base64 encoded caveat id
//		macaroon-id: (optional) id to give to discharge macaroon (defaults to id)
//	result on success (http.StatusOK):
//		{
//			Macaroon *macaroon.Macaroon
//		}
//
// GET /publickey
//	result:
//		public key of service
//		expiry time of key
type Discharger struct {
	p DischargerParams
}

// NewDischargerFromService returns a new third-party caveat
// discharger using the key and locator from the given service.
func NewDischargerFromService(svc *bakery.Service, checker ThirdPartyChecker) *Discharger {
	return NewDischarger(DischargerParams{
		Checker: checker,
		Key:     svc.Key(),
		Locator: svc.Locator(),
	})
}

// NewDischarger returns a new third-party caveat discharger
// using the given parameters.
func NewDischarger(p DischargerParams) *Discharger {
	if p.ErrorToResponse == nil {
		p.ErrorToResponse = ErrorToResponse
	}
	if p.Locator == nil {
		p.Locator = emptyLocator{}
	}
	return &Discharger{
		p: p,
	}
}

type emptyLocator struct{}

func (emptyLocator) ThirdPartyInfo(loc string) (bakery.ThirdPartyInfo, error) {
	return bakery.ThirdPartyInfo{}, bakery.ErrNotFound
}

// AddMuxHandlers adds handlers to the given ServeMux to provide
// a third-party caveat discharge service.
func (d *Discharger) AddMuxHandlers(mux *http.ServeMux, rootPath string) {
	for _, h := range d.Handlers() {
		// Note: this only works because we don't have any wildcard
		// patterns in the discharger paths.
		mux.Handle(path.Join(rootPath, h.Path), mkHTTPHandler(h.Handle))
	}
}

// Handlers returns a slice of handlers that can handle a third-party
// caveat discharge service when added to an httprouter.Router.
func (d *Discharger) Handlers() []httprequest.Handler {
	f := func(p httprequest.Params) (dischargeHandler, error) {
		return dischargeHandler{
			discharger: d,
		}, nil
	}
	return httprequest.ErrorMapper(d.p.ErrorToResponse).Handlers(f)
}

//go:generate httprequest-generate-client gopkg.in/macaroon-bakery.v2-unstable/httpbakery dischargeHandler dischargeClient

// dischargeHandler is the type used to define the httprequest handler
// methods for a discharger.
type dischargeHandler struct {
	discharger *Discharger
}

// dischargeRequest is a request to create a macaroon that discharges the
// supplied third-party caveat. Discharging caveats will normally be
// handled by the bakery it would be unusual to use this type directly in
// client software.
type dischargeRequest struct {
	httprequest.Route `httprequest:"POST /discharge"`
	Id                string `httprequest:"id,form"`
	Id64              string `httprequest:"id64,form"`
	// TODO(rog) If/when caveat ids can be passed
	// around independently of the macaroons themselves,
	// then this field could be used by a client to specify
	// the id to give to the discharge macaroon.
	// MacaroonId string `httprequest:"macaroon-id,form"`
}

// dischargeResponse contains the response from a /discharge POST request.
type dischargeResponse struct {
	Macaroon *macaroon.Macaroon `json:",omitempty"`
}

// Discharge discharges a third party caveat.
func (h dischargeHandler) Discharge(p httprequest.Params, r *dischargeRequest) (*dischargeResponse, error) {
	var id []byte
	if r.Id64 != "" {
		var err error
		id, err = base64.RawURLEncoding.DecodeString(r.Id64)
		if err != nil {
			return nil, errgo.Notef(err, "bad base64-encoded caveat id: %v", err)
		}
	} else {
		id = []byte(r.Id)
	}
	m, caveats, err := bakery.Discharge(h.discharger.p.Key, bakery.ThirdPartyCheckerFunc(
		func(cav *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
			return h.discharger.p.Checker.CheckThirdPartyCaveat(p.Request, cav)
		},
	), id)
	for _, cav := range caveats {
		if err := bakery.AddCaveat(h.discharger.p.Key, h.discharger.p.Locator, m, cav); err != nil {
			return nil, errgo.Mask(err)
		}
	}
	if err != nil {
		return nil, errgo.NoteMask(err, "cannot discharge", errgo.Any)
	}
	return &dischargeResponse{m}, nil
}

// publicKeyRequest specifies the /publickey endpoint.
type publicKeyRequest struct {
	httprequest.Route `httprequest:"GET /publickey"`
}

// publicKeyResponse is the response to a /publickey GET request.
type publicKeyResponse struct {
	PublicKey *bakery.PublicKey
}

// publicKeyRequest specifies the /discharge/info endpoint.
type dischargeInfoRequest struct {
	httprequest.Route `httprequest:"GET /discharge/info"`
}

// dischargeInfoResponse is the response to a /discharge/info GET
// request.
type dischargeInfoResponse struct {
	PublicKey *bakery.PublicKey
	Version   bakery.Version
}

// PublicKey returns the public key of the discharge service.
func (h dischargeHandler) PublicKey(*publicKeyRequest) (publicKeyResponse, error) {
	return publicKeyResponse{
		PublicKey: &h.discharger.p.Key.Public,
	}, nil
}

// DischargeInfo returns information on the discharger.
func (h dischargeHandler) DischargeInfo(*dischargeInfoRequest) (dischargeInfoResponse, error) {
	return dischargeInfoResponse{
		PublicKey: &h.discharger.p.Key.Public,
		Version:   bakery.LatestVersion,
	}, nil
}

// mkHTTPHandler converts an httprouter handler to an http.Handler,
// assuming that the httprouter handler has no wildcard path
// parameters.
func mkHTTPHandler(h httprouter.Handle) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		h(w, req, nil)
	})
}

// randomBytes returns n random bytes.
func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		return nil, fmt.Errorf("cannot generate %d random bytes: %v", n, err)
	}
	return b, nil
}
