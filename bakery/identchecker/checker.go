// Package identchecker wraps the functionality in the bakery
// package to add support for authentication via third party
// caveats.
package identchecker

import (
	"sync"

	"golang.org/x/net/context"
	errgo "gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v2"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
)

// CheckerParams holds parameters for NewChecker.
// The only mandatory parameter is MacaroonVerifier.
type CheckerParams struct {
	// MacaroonVerifier is used to retrieve macaroon root keys
	// and other associated information.
	MacaroonVerifier bakery.MacaroonVerifier

	// Checker is used to check first party caveats when authorizing.
	// If this is nil NewChecker will use checkers.New(nil).
	Checker bakery.FirstPartyCaveatChecker

	// OpsAuthorizer is used to check whether operations are authorized
	// by some other already-authorized operation. If it is nil,
	// NewChecker will assume no operation is authorized by any
	// operation except itself.
	OpsAuthorizer bakery.OpsAuthorizer

	// IdentityClient is used for interactions with the external
	// identity service used for authentication.
	//
	// If this is nil, no authentication will be possible.
	IdentityClient IdentityClient

	// Authorizer is used to check whether an authenticated user is
	// allowed to perform operations. If it is nil, NewChecker will
	// use ClosedAuthorizer.
	//
	// The identity parameter passed to Authorizer.Allow will
	// always have been obtained from a call to
	// IdentityClient.DeclaredIdentity.
	Authorizer Authorizer

	// Logger is used to log checker operations. If it is nil,
	// DefaultLogger("bakery.identchecker") will be used.
	Logger bakery.Logger
}

// NewChecker returns a new Checker using the given parameters.
func NewChecker(p CheckerParams) *Checker {
	if p.IdentityClient == nil {
		p.IdentityClient = noIdentities{}
	}
	if p.Authorizer == nil {
		p.Authorizer = ClosedAuthorizer
	}
	if p.Logger == nil {
		p.Logger = bakery.DefaultLogger("bakery.identchecker")
	}
	c := &Checker{
		p: p,
	}
	bp := bakery.CheckerParams{
		Checker:          p.Checker,
		OpsAuthorizer:    identityOpsAuthorizer{c},
		MacaroonVerifier: p.MacaroonVerifier,
	}
	return &Checker{
		checker: bakery.NewChecker(bp),
		p:       p,
	}
}

// Checker is similar to bakery.Checker but also knows
// about authentication, and can use an authenticated identity
// to authorize operations.
type Checker struct {
	checker *bakery.Checker
	p       CheckerParams
}

// Namespace returns the first-party caveat namespace
// used by the checker.
func (c *Checker) Namespace() *checkers.Namespace {
	return c.checker.Namespace()
}

// Auth makes a new AuthChecker instance using the
// given macaroons to inform authorization decisions.
// The identity is authenticated only once, the first time any method
// of the AuthChecker is called, using the context passed in then.
//
// To find out any declared identity without requiring a login,
// use Allow(ctx); to require authentication but no additional operations,
// use Allow(ctx, LoginOp).
func (c *Checker) Auth(mss ...macaroon.Slice) *AuthChecker {
	return &AuthChecker{
		checker:     c,
		authChecker: c.checker.Auth(mss...),
	}
}

// AuthInfo information about an authorization decision.
type AuthInfo struct {
	*bakery.AuthInfo

	// Identity holds information on the authenticated user as returned
	// from IdentityClient. It may be nil after a
	// successful authorization if LoginOp access was not required.
	Identity Identity
}

// LoginOp represents a login (authentication) operation.
// A macaroon that is associated with this operation generally
// carries authentication information with it.
var LoginOp = bakery.Op{
	Entity: "login",
	Action: "login",
}

// AuthChecker authorizes operations with respect to a user's request.
type AuthChecker struct {
	checker     *Checker
	authChecker *bakery.AuthChecker

	// mu guards the identity_ field.
	mu sync.Mutex

	// identity_ holds the first identity discovered by AuthorizeOps
	// so that all authorizations use a consistent identity.
	identity_ Identity
}

// Allow checks that the authorizer's request is authorized to
// perform all the given operations.
//
// If all the operations are allowed, an AuthInfo is returned holding
// details of the decision.
//
// If an operation was not allowed, an error will be returned which may
// be *bakery.DischargeRequiredError holding the operations that remain to
// be authorized in order to allow authorization to
// proceed.
func (c *AuthChecker) Allow(ctx context.Context, ops ...bakery.Op) (*AuthInfo, error) {
	loginInfo, loginErr := c.authChecker.Allow(ctx, LoginOp)
	c.checker.p.Logger.Infof(ctx, "allow loginop: %#v; err %#v", loginInfo, loginErr)
	var identity Identity
	var identityCaveats []checkers.Caveat
	if loginErr == nil {
		// We've got a login macaroon. Extract the identity from it.
		identity1, err := c.inferIdentityFromMacaroon(ctx, c.authChecker.Namespace(), loginInfo.Macaroons[loginInfo.OpIndexes[LoginOp]])
		if err != nil {
			return nil, errgo.Mask(err)
		}
		identity = identity1
	} else {
		// No login macaroon found. Try to infer an identity from the context.
		identity1, caveats1, err := c.inferIdentityFromContext(ctx)
		if err != nil {
			return nil, errgo.WithCausef(err, bakery.ErrPermissionDenied, "")
		}
		identity, identityCaveats = identity1, caveats1

		mss := c.authChecker.Macaroons()
		loginInfo = &bakery.AuthInfo{
			Macaroons: mss,
			Used:      make([]bool, len(mss)),
			OpIndexes: make(map[bakery.Op]int),
		}
	}
	// Form a slice holding all the non-login operations that are required.
	need := make([]bakery.Op, 0, len(ops))
	for _, op := range ops {
		if op != LoginOp {
			need = append(need, op)
		}
	}
	if len(need) == 0 && identity != nil {
		// No operations other than LoginOp required, and we've
		// got an identity, so nothing more to do.
		return &AuthInfo{
			Identity: identity,
			AuthInfo: loginInfo,
		}, nil
	}
	// Check the remaining operations only there are more to
	// authorize, and if we have an identity or we don't need one.
	needLogin := len(need) != len(ops)
	if len(need) > 0 && (!needLogin || identity != nil) {
		// Make the AuthChecker available to the OpsAuthorizer
		// so that it can use any identity we inferred above in
		// the authorization decision.
		ctx := contextWithIdentity(ctx, identity)
		opInfo, err := c.authChecker.Allow(ctx, need...)
		if err == nil {
			// All operations allowed.
			if loginErr == nil {
				for i, used := range loginInfo.Used {
					if used {
						opInfo.Used[i] = used
					}
				}
				opInfo.OpIndexes[LoginOp] = loginInfo.OpIndexes[LoginOp]
			}
			return &AuthInfo{
				AuthInfo: opInfo,
				Identity: identity,
			}, nil
		}
		if bakery.IsDischargeRequiredError(err) {
			return nil, errgo.Mask(err, bakery.IsDischargeRequiredError)
		}
	}
	if identity != nil || len(identityCaveats) == 0 {
		return nil, errgo.WithCausef(loginErr, bakery.ErrPermissionDenied, "")
	}
	return nil, &bakery.DischargeRequiredError{
		Message:           "authentication required",
		Ops:               []bakery.Op{LoginOp},
		Caveats:           identityCaveats,
		ForAuthentication: true,
	}
}

type identityOpsAuthorizer struct {
	checker *Checker
}

// AuthorizeOps implements bakery.OpsAuthorizer. It allows LoginOp
// to authorize operations by using the Authorizer passed to
// NewChecker, and falls back to OpsAuthorizer for other operations.
//
// Once an identity has been determined, the same identity will always
// be used.
func (a identityOpsAuthorizer) AuthorizeOps(ctx context.Context, authorizedOp bakery.Op, queryOps []bakery.Op) ([]bool, []checkers.Caveat, error) {
	identity, ok := identityFromContext(ctx)
	if !ok {
		// There's no identity associated with the context, which means we're
		// authorizing before we've inferred the identity, so we
		// can't authorize any indirect operations.
		return nil, nil, nil
	}
	// There might or might not be an actual LoginOp macaroon, so
	// we authorize from NoOp instead, which is always used last.
	if authorizedOp != bakery.NoOp {
		if a.checker.p.OpsAuthorizer != nil {
			return a.checker.p.OpsAuthorizer.AuthorizeOps(ctx, authorizedOp, queryOps)
		}
		return nil, nil, nil
	}
	allowed, caveats, err := a.checker.p.Authorizer.Authorize(ctx, identity, queryOps)
	return allowed, caveats, errgo.Mask(err)
}

// inferIdentityFromMacaroon ensures sure that we always use the same identity for authorization.
// The op argument holds an authorized operation
func (c *AuthChecker) inferIdentityFromMacaroon(ctx context.Context, ns *checkers.Namespace, ms macaroon.Slice) (Identity, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.identity_ != nil {
		return c.identity_, nil
	}
	declared := checkers.InferDeclared(ns, ms)
	identity, err := c.checker.p.IdentityClient.DeclaredIdentity(ctx, declared)
	if err != nil {
		return nil, errgo.Notef(err, "could not determine identity")
	}
	if identity == nil {
		// Shouldn't happen if DeclaredIdentity behaves itself, but
		// be defensive just in case.
		return nil, errgo.Newf("no declared identity found in LoginOp macaroon")
	}
	c.identity_ = identity
	return identity, nil
}

// inferIdentity extracts an Identity from ctx, if there is one, and stores it
// in c.identity_ to ensure that we always use the same identity for authorization.
// It returns either the identity or a set of third party caveats that, when
// discharged, will allow us to determine an identity.
func (c *AuthChecker) inferIdentityFromContext(ctx context.Context) (Identity, []checkers.Caveat, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.identity_ != nil {
		return c.identity_, nil, nil
	}
	// NoOp is used after all the other operations have been tried, so we've
	// already tried any LoginOp macaroons if present.
	identity, caveats, err := c.checker.p.IdentityClient.IdentityFromContext(ctx)
	if err != nil {
		return nil, nil, errgo.Notef(err, "could not determine identity")
	}
	if len(caveats) != 0 {
		return nil, caveats, nil
	}
	c.identity_ = identity
	return identity, nil, nil
}

type identityKey struct{}

// identityVal holds an Identity in a context. We use a struct
// rather than storing the identity directly so that we can know
// when a context is associated with a nil identity,
// so that AuthorizeOps can tell that it's being called before
// any identity has been determined.
type identityVal struct {
	Identity
}

func contextWithIdentity(ctx context.Context, identity Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, identityVal{identity})
}

func identityFromContext(ctx context.Context) (Identity, bool) {
	idVal, ok := ctx.Value(identityKey{}).(identityVal)
	return idVal.Identity, ok
}
