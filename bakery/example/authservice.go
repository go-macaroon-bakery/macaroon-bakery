package main

import (
	"context"
	"net/http"

	"github.com/go-macaroon-bakery/macaroon-bakery/v3/bakery"
	"github.com/go-macaroon-bakery/macaroon-bakery/v3/bakery/checkers"
	"github.com/go-macaroon-bakery/macaroon-bakery/v3/httpbakery"
)

// authService implements an authorization service,
// that can discharge third-party caveats added
// to other macaroons.
func authService(endpoint string, key *bakery.KeyPair) (http.Handler, error) {
	d := httpbakery.NewDischarger(httpbakery.DischargerParams{
		Checker: httpbakery.ThirdPartyCaveatCheckerFunc(thirdPartyChecker),
		Key:     bakery.MustGenerateKey(),
	})

	mux := http.NewServeMux()
	d.AddMuxHandlers(mux, "/")
	return mux, nil
}

// thirdPartyChecker is used to check third party caveats added by other
// services. The HTTP request is that of the client - it is attempting
// to gather a discharge macaroon.
//
// Note how this function can return additional first- and third-party
// caveats which will be added to the original macaroon's caveats.
func thirdPartyChecker(ctx context.Context, req *http.Request, info *bakery.ThirdPartyCaveatInfo, token *httpbakery.DischargeToken) ([]checkers.Caveat, error) {
	if string(info.Condition) != "access-allowed" {
		return nil, checkers.ErrCaveatNotRecognized
	}
	// TODO check that the HTTP request has cookies that prove
	// something about the client.
	return []checkers.Caveat{
		httpbakery.SameClientIPAddrCaveat(req),
	}, nil
}
