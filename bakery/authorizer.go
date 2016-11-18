package bakery

import (
	"golang.org/x/net/context"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
)

// Authorizer is used to check whether a given user is allowed
// to perform a set of operations.
type Authorizer interface {
	// Authorize checks whether the given identity (which will be nil
	// when there is no authenticated user) is allowed to perform
	// the given operations. It should return an error only when
	// the authorization cannot be determined, not when the
	// user has been denied access.
	//
	// On success, each element of allowed holds whether the respective
	// element of ops has been allowed, and caveats holds any additional
	// third party caveats that apply.
	//
	// If allowed is shorter then ops, the additional elements are assumed to
	// be false.
	Authorize(ctxt context.Context, id Identity, ops []Op) (allowed []bool, caveats []checkers.Caveat, err error)
}

var (
	// OpenAuthorizer is an Authorizer implementation that will authorize all operations without question.
	OpenAuthorizer openAuthorizer

	// ClosedAuthorizer is an Authorizer implementation that will return ErrPermissionDenied
	// on all authorization requests.
	ClosedAuthorizer closedAuthorizer
)

var (
	_ Authorizer = OpenAuthorizer
	_ Authorizer = ClosedAuthorizer
)

type openAuthorizer struct{}

// Authorize implements Authorizer.Authorize.
func (openAuthorizer) Authorize(ctxt context.Context, id Identity, ops []Op) (allowed []bool, caveats []checkers.Caveat, err error) {
	allowed = make([]bool, len(ops))
	for i := range allowed {
		allowed[i] = true
	}
	return allowed, nil, nil
}

type closedAuthorizer struct{}

// Authorize implements Authorizer.Authorize.
func (closedAuthorizer) Authorize(ctxt context.Context, id Identity, ops []Op) (allowed []bool, caveats []checkers.Caveat, err error) {
	return make([]bool, len(ops)), nil, nil
}
