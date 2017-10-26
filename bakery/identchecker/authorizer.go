package identchecker

import (
	"golang.org/x/net/context"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
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
	Authorize(ctx context.Context, id Identity, ops []bakery.Op) (allowed []bool, caveats []checkers.Caveat, err error)
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
func (openAuthorizer) Authorize(ctx context.Context, id Identity, ops []bakery.Op) (allowed []bool, caveats []checkers.Caveat, err error) {
	allowed = make([]bool, len(ops))
	for i := range allowed {
		allowed[i] = true
	}
	return allowed, nil, nil
}

type closedAuthorizer struct{}

// Authorize implements Authorizer.Authorize.
func (closedAuthorizer) Authorize(ctx context.Context, id Identity, ops []bakery.Op) (allowed []bool, caveats []checkers.Caveat, err error) {
	return make([]bool, len(ops)), nil, nil
}

// AuthorizerFunc implements a simplified version of Authorizer
// that operates on a single operation at a time.
type AuthorizerFunc func(ctx context.Context, id Identity, op bakery.Op) (bool, []checkers.Caveat, error)

// Authorize implements Authorizer.Authorize by calling f
// with the given identity for each operation.
func (f AuthorizerFunc) Authorize(ctx context.Context, id Identity, ops []bakery.Op) (allowed []bool, caveats []checkers.Caveat, err error) {
	allowed = make([]bool, len(ops))
	for i, op := range ops {
		ok, fcaveats, err := f(ctx, id, op)
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
	Allow(ctx context.Context, acl []string) (bool, error)
}

// ACLAuthorizer is an Authorizer implementation that will check access
// control list (ACL) membership of users. It uses GetACL to find out
// the ACLs that apply to the requested operations and will authorize an
// operation if an ACL contains the group "everyone" or if the context
// contains an AuthInfo (see ContextWithAuthInfo) that holds an Identity
// that implements ACLIdentity and its Allow method returns true for the
// ACL.
type ACLAuthorizer struct {
	// GetACL returns the ACL that applies to the given operation,
	// and reports whether non-authenticated users should
	// be allowed access when the ACL contains "everyone".
	//
	// If an entity cannot be found or the action is not recognised,
	// GetACLs should return an empty ACL but no error.
	GetACL func(ctx context.Context, op bakery.Op) (acl []string, allowPublic bool, err error)
}

// Authorize implements Authorizer.Authorize by calling ident.Allow to determine
// whether the identity is a member of the ACLs associated with the given
// operations.
func (a ACLAuthorizer) Authorize(ctx context.Context, ident Identity, ops []bakery.Op) (allowed []bool, caveats []checkers.Caveat, err error) {
	if len(ops) == 0 {
		// Anyone is allowed to do nothing.
		return nil, nil, nil
	}
	ident1, _ := ident.(ACLIdentity)
	allowed = make([]bool, len(ops))
	for i, op := range ops {
		acl, allowPublic, err := a.GetACL(ctx, op)
		if err != nil {
			return nil, nil, errgo.Mask(err)
		}
		if ident1 != nil {
			allowed[i], err = ident1.Allow(ctx, acl)
			if err != nil {
				return nil, nil, errgo.Notef(err, "cannot check permissions")
			}
		} else {
			// TODO should we allow "everyone" when the identity is
			// non-nil but isn't an ACLIdentity?
			allowed[i] = allowPublic && isPublicACL(acl)
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
