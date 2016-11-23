package bakery

import (
	"golang.org/x/net/context"
	"gopkg.in/errgo.v1"

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
	_ Authorizer = AuthorizerFunc(nil)
	_ Authorizer = ACLAuthorizer{}
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

// AuthorizerFunc implements a simplified version of Authorizer
// that operates on a single operation at a time.
type AuthorizerFunc func(ctxt context.Context, id Identity, op Op) (bool, []checkers.Caveat, error)

// Authorize implements Authorizer.Authorize by calling f
// with the given identity for each operation.
func (f AuthorizerFunc) Authorize(ctxt context.Context, id Identity, ops []Op) (allowed []bool, caveats []checkers.Caveat, err error) {
	allowed = make([]bool, len(ops))
	for i, op := range ops {
		ok, fcaveats, err := f(ctxt, id, op)
		if err != nil {
			return nil, nil, errgo.Mask(err)
		}
		allowed[i] = ok
		// TODO merge identical caveats?
		caveats = append(caveats, fcaveats...)
	}
	return allowed, caveats, nil
}

// Everyone is recognized by ACLAuthorizer as the name of a
// group that has everyone in it.
const Everyone = "everyone"

// ACLIdentity may be implemented by Identity implementions
// to report group membership information.
// See ACLAuthorizer for details.
type ACLIdentity interface {
	Identity

	// Allow reports whether the user should be allowed to access
	// any of the users or groups in the given ACL slice.
	Allow(ctxt context.Context, acl []string) (bool, error)
}

// ACLAuthorizer is an Authorizer implementation that will check access
// control list (ACL) membership of users. It uses GetACL to find out
// the ACLs that apply to the requested operations and will authorize an
// operation if an ACL contains the group "everyone" or if the context
// contains an AuthInfo (see ContextWithAuthInfo) that holds an Identity
// that implements ACLIdentity and its Allow method returns true for the
// ACL.
type ACLAuthorizer struct {
	// If AllowPublic is true and an ACL contains "everyone",
	// then authorization will be granted even if there is
	// no logged in user.
	AllowPublic bool

	// GetACL returns the ACL that applies to the given operation.
	// If an entity cannot be found or the action is not recognised,
	// GetACLs should return an empty ACL but no error.
	GetACL func(ctxt context.Context, op Op) ([]string, error)
}

// Authorize implements Authorizer.Authorize by calling ident.Allow to determine
// whether the identity is a member of the ACLs associated with the given
// operations.
func (a ACLAuthorizer) Authorize(ctxt context.Context, ident Identity, ops []Op) (allowed []bool, caveats []checkers.Caveat, err error) {
	if len(ops) == 0 {
		// Anyone is allowed to do nothing.
		return nil, nil, nil
	}
	ident1, _ := ident.(ACLIdentity)
	allowed = make([]bool, len(ops))
	for i, op := range ops {
		acl, err := a.GetACL(ctxt, op)
		if err != nil {
			return nil, nil, errgo.Mask(err)
		}
		if ident1 != nil {
			allowed[i], err = ident1.Allow(ctxt, acl)
			if err != nil {
				return nil, nil, errgo.Notef(err, "cannot check permissions")
			}
		} else {
			allowed[i] = a.AllowPublic && isPublicACL(acl)
		}
	}
	return allowed, nil, nil
}

func isPublicACL(acl []string) bool {
	for _, g := range acl {
		if g == Everyone {
			return true
		}
	}
	return false
}
