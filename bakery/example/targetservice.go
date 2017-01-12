// +build ignore

package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"golang.org/x/net/context"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery"
)

type targetServiceHandler struct {
	svc          *bakery.Service
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
	svc, err := bakery.NewService(bakery.NewServiceParams{
		Key:      key,
		Location: endpoint,
		Locator:  pkLocator,
		Checker:  httpbakery.NewChecker(),
	})
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	srv := &targetServiceHandler{
		svc:          svc,
		authEndpoint: authEndpoint,
	}
	mux.HandleFunc("/gold/", srv.serveGold)
	mux.HandleFunc("/silver/", srv.serveSilver)
	return mux, nil
}

func (srv *targetServiceHandler) serveGold(w http.ResponseWriter, req *http.Request) {
	ctx := checkers.ContextWithOperations(context.TODO(), "gold")
	if _, _, err := httpbakery.CheckRequest(ctx, srv.svc, req, nil); err != nil {
		srv.writeError(w, req, "gold", err)
		return
	}
	fmt.Fprintf(w, "all is golden")
}

func (srv *targetServiceHandler) serveSilver(w http.ResponseWriter, req *http.Request) {
	ctx := checkers.ContextWithOperations(context.TODO(), "silver")
	if _, _, err := httpbakery.CheckRequest(ctx, srv.svc, req, nil); err != nil {
		srv.writeError(w, req, "silver", err)
		return
	}
	fmt.Fprintf(w, "every cloud has a silver lining")
}

// writeError writes an error to w in response to req. If the error was
// generated because of a required macaroon that the client does not
// have, we mint a macaroon that, when discharged, will grant the client
// the right to execute the given operation.
//
// The logic in this function is crucial to the security of the service
// - it must determine for a given operation what caveats to attach.
func (srv *targetServiceHandler) writeError(w http.ResponseWriter, req *http.Request, operation string, verr error) {
	log.Printf("writing error with operation %q", operation)
	fail := func(code int, msg string, args ...interface{}) {
		if code == http.StatusInternalServerError {
			msg = "internal error: " + msg
		}
		http.Error(w, fmt.Sprintf(msg, args...), code)
	}

	if _, ok := errgo.Cause(verr).(*bakery.VerificationError); !ok {
		fail(http.StatusForbidden, "%v", verr)
		return
	}

	// Work out what caveats we need to apply for the given operation.
	// Could special-case the operation here if desired.
	caveats := []checkers.Caveat{
		checkers.TimeBeforeCaveat(time.Now().Add(5 * time.Minute)),
		checkers.AllowCaveat(operation),
		{
			Location:  srv.authEndpoint,
			Condition: "access-allowed",
		},
	}
	// Mint an appropriate macaroon and send it back to the client.
	m, err := srv.svc.NewMacaroon(httpbakery.RequestVersion(req), caveats)
	if err != nil {
		fail(http.StatusInternalServerError, "cannot mint macaroon: %v", err)
		return
	}
	httpbakery.WriteDischargeRequiredErrorForRequest(w, m, "", verr, req)
}
