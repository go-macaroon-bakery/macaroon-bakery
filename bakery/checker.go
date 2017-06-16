package bakery

import (
	"sort"
	"sync"
	"time"

	"golang.org/x/net/context"
	errgo "gopkg.in/errgo.v1"
	macaroon "gopkg.in/macaroon.v2-unstable"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
)

// LoginOp represents a login (authentication) operation.
// A macaroon that is associated with this operation generally
// carries authentication information with it.
var LoginOp = Op{
	Entity: "login",
	Action: "login",
}

// Op holds an entity and action to be authorized on that entity.
type Op struct {
	// Entity holds the name of the entity to be authorized.
	// Entity names should not contain spaces and should
	// not start with the prefix "login" or "multi-" (conventionally,
	// entity names will be prefixed with the entity type followed
	// by a hyphen.
	Entity string

	// Action holds the action to perform on the entity, such as "read"
	// or "delete". It is up to the service using a checker to define
	// a set of operations and keep them consistent over time.
	Action string
}

// CheckerParams holds parameters for NewChecker.
type CheckerParams struct {
	// CaveatChecker is used to check first party caveats when authorizing.
	// If this is nil NewChecker will use checkers.New(nil).
	Checker FirstPartyCaveatChecker

	// Authorizer is used to check whether an authenticated user is
	// allowed to perform operations. If it is nil, NewChecker will
	// use ClosedAuthorizer.
	//
	// The identity parameter passed to Authorizer.Allow will
	// always have been obtained from a call to
	// IdentityClient.DeclaredIdentity.
	Authorizer Authorizer

	// IdentityClient is used for interactions with the external
	// identity service used for authentication.
	//
	// If this is nil, no authentication will be possible.
	IdentityClient IdentityClient

	// MacaroonOps is used to retrieve macaroon root keys
	// and other associated information.
	MacaroonOpStore MacaroonOpStore
}

// AuthInfo information about an authorization decision.
type AuthInfo struct {
	// Identity holds information on the authenticated user as returned
	// from IdentityClient. It may be nil after a
	// successful authorization if LoginOp access was not required.
	Identity Identity

	// Macaroons holds all the macaroons that were used for the
	// authorization. Macaroons that were invalid or unnecessary are
	// not included.
	Macaroons []macaroon.Slice

	// TODO add information on user ids that have contributed
	// to the authorization:
	// After a successful call to Authorize or Capability,
	// AuthorizingUserIds returns the user ids that were used to
	// create the capability macaroons used to authorize the call.
	// Note that this is distinct from UserId, as there can only be
	// one authenticated user associated with the checker.
	// AuthorizingUserIds []string
}

// Checker wraps a FirstPartyCaveatChecker and adds authentication and authorization checks.
//
// It uses macaroons as authorization tokens but it is not itself responsible for
// creating the macaroons - see the Oven type (TODO) for one way of doing that.
type Checker struct {
	FirstPartyCaveatChecker
	p CheckerParams
}

// NewChecker returns a new Checker using the given parameters.
func NewChecker(p CheckerParams) *Checker {
	if p.Checker == nil {
		p.Checker = checkers.New(nil)
	}
	if p.Authorizer == nil {
		p.Authorizer = ClosedAuthorizer
	}
	if p.IdentityClient == nil {
		p.IdentityClient = noIdentities{}
	}
	return &Checker{
		FirstPartyCaveatChecker: p.Checker,
		p: p,
	}
}

// Auth makes a new AuthChecker instance using the
// given macaroons to inform authorization decisions.
func (c *Checker) Auth(mss ...macaroon.Slice) *AuthChecker {
	return &AuthChecker{
		Checker:   c,
		macaroons: mss,
	}
}

// AuthChecker authorizes operations with respect to a user's request.
// The identity is authenticated only once, the first time any method
// of the AuthChecker is called, using the context passed in then.
//
// To find out any declared identity without requiring a login,
// use Allow(ctx); to require authentication but no additional operations,
// use Allow(ctx, LoginOp).
type AuthChecker struct {
	// Checker is used to check first party caveats.
	*Checker
	macaroons []macaroon.Slice
	// conditions holds the first party caveat conditions
	// that apply to each of the above macaroons.
	conditions      [][]string
	initOnce        sync.Once
	initError       error
	initErrors      []error
	identity        Identity
	identityCaveats []checkers.Caveat
	// authIndexes holds for each potentially authorized operation
	// the indexes of the macaroons that authorize it.
	authIndexes map[Op][]int
}

func (a *AuthChecker) init(ctx context.Context) error {
	a.initOnce.Do(func() {
		a.initError = a.initOnceFunc(ctx)
	})
	return a.initError
}

func (a *AuthChecker) initOnceFunc(ctx context.Context) error {
	a.authIndexes = make(map[Op][]int)
	a.conditions = make([][]string, len(a.macaroons))
	for i, ms := range a.macaroons {
		ops, conditions, err := a.p.MacaroonOpStore.MacaroonOps(ctx, ms)
		if err != nil {
			if !isVerificationError(err) {
				return errgo.Notef(err, "cannot retrieve macaroon")
			}
			a.initErrors = append(a.initErrors, errgo.Mask(err))
			continue
		}
		logger.Debugf("checking macaroon %d; ops %q, conditions %q", i, ops, conditions)
		// It's a valid macaroon (in principle - we haven't checked first party caveats).
		a.conditions[i] = conditions
		isLogin := false
		for _, op := range ops {
			if op == LoginOp {
				// Don't associate the macaroon with the login operation
				// until we've verified that it is valid below
				isLogin = true
			} else {
				a.authIndexes[op] = append(a.authIndexes[op], i)
			}
		}
		if !isLogin {
			continue
		}
		// It's a login macaroon. Check the conditions now -
		// all calls want to see the same authentication
		// information so that callers have a consistent idea of
		// the client's identity.
		//
		// If the conditions fail, we won't use the macaroon for
		// identity, but we can still potentially use it for its
		// other operations if the conditions succeed for those.
		declared, err := a.checkConditions(ctx, LoginOp, conditions)
		if err != nil {
			a.initErrors = append(a.initErrors, errgo.Notef(err, "cannot authorize login macaroon"))
			continue
		}
		if a.identity != nil {
			// We've already found a login macaroon so ignore this one
			// for the purposes of identity.
			continue
		}
		identity, err := a.p.IdentityClient.DeclaredIdentity(ctx, declared)
		if err != nil {
			a.initErrors = append(a.initErrors, errgo.Notef(err, "cannot decode declared identity: %v", err))
			continue
		}
		a.authIndexes[LoginOp] = append(a.authIndexes[LoginOp], i)
		a.identity = identity
	}
	if a.identity == nil {
		// No identity yet, so try to get one based on the context.
		identity, caveats, err := a.p.IdentityClient.IdentityFromContext(ctx)
		if err != nil {
			a.initErrors = append(a.initErrors, errgo.Notef(err, "could not determine identity"))
		}
		a.identity, a.identityCaveats = identity, caveats
	}
	logger.Infof("after init, identity: %#v, authIndexes %v; errors %q", a.identity, a.authIndexes, a.initErrors)
	return nil
}

// Allow checks that the authorizer's request is authorized to
// perform all the given operations. Note that Allow does not check
// first party caveats - if there is more than one macaroon that may
// authorize the request, it will choose the first one that does regardless
//
// If all the operations are allowed, an AuthInfo is returned holding
// details of the decision and any first party caveats that must be
// checked before actually executing any operation.
//
// If operations include LoginOp, the request should contain an
// authentication macaroon proving the client's identity. Once an
// authentication macaroon is chosen, it will be used for all other
// authorization requests.
//
// If an operation was not allowed, an error will be returned which may
// be *DischargeRequiredError holding the operations that remain to
// be authorized in order to allow authorization to
// proceed.
func (a *AuthChecker) Allow(ctx context.Context, ops ...Op) (*AuthInfo, error) {
	authInfo, _, err := a.AllowAny(ctx, ops...)
	if err != nil {
		return nil, err
	}
	return authInfo, nil
}

// AllowAny is like Allow except that it will authorize as many of the
// operations as possible without requiring any to be authorized. If all
// the operations succeeded, the returned error and slice will be nil.
//
// If any the operations failed, the returned error will be the same
// that Allow would return and each element in the returned slice will
// hold whether its respective operation was allowed.
//
// If all the operations succeeded, the returned slice will be nil.
//
// The returned *AuthInfo will always be non-nil.
//
// The LoginOp operation is treated specially - it is always required if
// present in ops.
func (a *AuthChecker) AllowAny(ctx context.Context, ops ...Op) (*AuthInfo, []bool, error) {
	authed, used, err := a.allowAny(ctx, ops)
	return a.newAuthInfo(used), authed, err
}

func (a *AuthChecker) newAuthInfo(used []bool) *AuthInfo {
	info := &AuthInfo{
		Identity:  a.identity,
		Macaroons: make([]macaroon.Slice, 0, len(a.macaroons)),
	}
	for i, isUsed := range used {
		if isUsed {
			info.Macaroons = append(info.Macaroons, a.macaroons[i])
		}
	}
	return info
}

// allowAny is the internal version of AllowAny. Instead of returning an
// authInfo struct, it returns a slice describing which operations have
// been successfully authorized and a slice describing which macaroons
// have been used in the authorization.
func (a *AuthChecker) allowAny(ctx context.Context, ops []Op) (authed, used []bool, err error) {
	if err := a.init(ctx); err != nil {
		return nil, nil, errgo.Mask(err)
	}
	used = make([]bool, len(a.macaroons))
	authed = make([]bool, len(ops))
	numAuthed := 0
	var errors []error
	for i, op := range ops {
		for _, mindex := range a.authIndexes[op] {
			_, err := a.checkConditions(ctx, op, a.conditions[mindex])
			if err != nil {
				logger.Infof("condition check %q failed: %v", a.conditions[mindex], err)
				errors = append(errors, err)
				continue
			}
			authed[i] = true
			numAuthed++
			used[mindex] = true
			// Use the first authorized macaroon only.
			break
		}
		if op == LoginOp && !authed[i] && a.identity != nil {
			// Allow LoginOp when there's an authenticated user even
			// when there's no macaroon that specifically authorizes it.
			authed[i] = true
		}
	}
	if a.identity != nil {
		// We've authenticated as a user, so even if the operations didn't
		// specifically require it, we add the login macaroon
		// to the macaroons used.
		// Note that the LoginOp conditions have already been checked
		// successfully in initOnceFunc so no need to check again.
		// Note also that there may not be any macaroons if the
		// identity client decided on an identity even with no
		// macaroons.
		for _, i := range a.authIndexes[LoginOp] {
			used[i] = true
		}
	}
	if numAuthed == len(ops) {
		// All operations allowed.
		return nil, used, nil
	}
	// There are some unauthorized operations.
	need := make([]Op, 0, len(ops)-numAuthed)
	needIndex := make([]int, cap(need))
	for i, ok := range authed {
		if !ok {
			needIndex[len(need)] = i
			need = append(need, ops[i])
		}
	}

	// Try to authorize the operations even if we haven't got an authenticated user.
	oks, caveats, err := a.p.Authorizer.Authorize(ctx, a.identity, need)
	if err != nil {
		// TODO if there are macaroons supplied that have failed, perhaps we shouldn't
		// do this but return those errors instead? Doing things the current
		// way means that we lose the previous errors.
		return authed, used, errgo.Notef(err, "cannot check permissions")
	}

	stillNeed := make([]Op, 0, len(need))
	for i := range need {
		if i < len(oks) && oks[i] {
			authed[needIndex[i]] = true
		} else {
			stillNeed = append(stillNeed, ops[needIndex[i]])
		}
	}
	if len(stillNeed) == 0 && len(caveats) == 0 {
		// No more ops need to be authenticated and no caveats to be discharged.
		return authed, used, nil
	}
	logger.Debugf("operations still needed after auth check: %#v", stillNeed)
	if a.identity == nil && len(a.identityCaveats) > 0 {
		return authed, used, &DischargeRequiredError{
			Message: "authentication required",
			Ops:     []Op{LoginOp},
			Caveats: a.identityCaveats,
		}
	}
	if len(caveats) == 0 {
		allErrors := make([]error, 0, len(a.initErrors)+len(errors))
		allErrors = append(allErrors, a.initErrors...)
		allErrors = append(allErrors, errors...)
		var err error
		if len(allErrors) > 0 {
			// TODO return all errors?
			logger.Infof("all auth errors: %q", allErrors)
			err = allErrors[0]
		}
		return authed, used, errgo.WithCausef(err, ErrPermissionDenied, "")
	}
	return authed, used, &DischargeRequiredError{
		Message: "some operations have extra caveats",
		Ops:     ops,
		Caveats: caveats,
	}
}

// AllowCapability checks that the user is allowed to perform all the
// given operations. If not, the error will be as returned from Allow.
//
// If AllowCapability succeeds, it returns a list of first party caveat
// conditions that must be applied to any macaroon granting capability
// to execute the operations. Those caveat conditions will not
// include any declarations contained in login macaroons - the
// caller must be careful not to mint a macaroon associated
// with the LoginOp operation unless they add the expected
// declaration caveat too - in general, clients should not create capabilities
// that grant LoginOp rights.
//
// The operations must include at least one non-LoginOp operation.
func (a *AuthChecker) AllowCapability(ctx context.Context, ops ...Op) ([]string, error) {
	nops := 0
	for _, op := range ops {
		if op != LoginOp {
			nops++
		}
	}
	if nops == 0 {
		return nil, errgo.Newf("no non-login operations required in capability")
	}
	_, used, err := a.allowAny(ctx, ops)
	if err != nil {
		return nil, errgo.Mask(err, isDischargeRequiredError)
	}
	var squasher caveatSquasher
	for i, isUsed := range used {
		if !isUsed {
			continue
		}
		for _, cond := range a.conditions[i] {
			squasher.add(cond)
		}
	}
	return squasher.final(), nil
}

// caveatSquasher rationalizes first party caveats created for a capability
// by:
//	- including only the earliest time-before caveat.
//	- excluding allow and deny caveats (operations are checked by
//	virtue of the operations associated with the macaroon).
//	- removing declared caveats.
//	- removing duplicates.
type caveatSquasher struct {
	expiry time.Time
	conds  []string
}

func (c *caveatSquasher) add(cond string) {
	if c.add0(cond) {
		c.conds = append(c.conds, cond)
	}
}

func (c *caveatSquasher) add0(cond string) bool {
	cond, args, err := checkers.ParseCaveat(cond)
	if err != nil {
		// Be safe - if we can't parse the caveat, just leave it there.
		return true
	}
	switch cond {
	case checkers.CondTimeBefore:
		et, err := time.Parse(time.RFC3339Nano, args)
		if err != nil || et.IsZero() {
			// Again, if it doesn't seem valid, leave it alone.
			return true
		}
		if c.expiry.IsZero() || et.Before(c.expiry) {
			c.expiry = et
		}
		return false
	case checkers.CondAllow,
		checkers.CondDeny,
		checkers.CondDeclared:
		return false
	}
	return true
}

func (c *caveatSquasher) final() []string {
	if !c.expiry.IsZero() {
		c.conds = append(c.conds, checkers.TimeBeforeCaveat(c.expiry).Condition)
	}
	if len(c.conds) == 0 {
		return nil
	}
	// Make deterministic and eliminate duplicates.
	sort.Strings(c.conds)
	prev := c.conds[0]
	j := 1
	for _, cond := range c.conds[1:] {
		if cond != prev {
			c.conds[j] = cond
			prev = cond
			j++
		}
	}
	c.conds = c.conds[:j]
	return c.conds
}

func (a *AuthChecker) checkConditions(ctx context.Context, op Op, conds []string) (map[string]string, error) {
	declared := checkers.InferDeclaredFromConditions(a.Namespace(), conds)
	ctx = checkers.ContextWithOperations(ctx, op.Action)
	ctx = checkers.ContextWithDeclared(ctx, declared)
	for _, cond := range conds {
		if err := a.CheckFirstPartyCaveat(ctx, cond); err != nil {
			return nil, errgo.Mask(err)
		}
	}
	return declared, nil
}
