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

	// OpsAuthorizer is used to check whether operations are authorized
	// by some other already-authorized operation. If it is nil,
	// NewChecker will assume no operation is authorized by any
	// operation except itself.
	OpsAuthorizer OpsAuthorizer

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

// Allowed returns all the operations allowed by the provided macaroons
// as keys in the returned map (all the associated values will be true).
// Note that this does not include operations that would be indirectly
// allowed via the OpAuthorizer.
//
// It also returns the AuthInfo (always non-nil) similarly to AllowAny.
//
// Allowed returns an error only when there is an underlying storage failure,
// not when operations are not authorized.
func (a *AuthChecker) Allowed(ctx context.Context) (*AuthInfo, map[Op]bool, error) {
	used := make([]bool, len(a.macaroons))
	if err := a.init(ctx); err != nil {
		return a.newAuthInfo(used), nil, errgo.Mask(err)
	}
	ops := make(map[Op]bool)
	// TODO this is non-deterministic; perhaps we should sort the
	// operations before ranging over them?
	for op, indexes := range a.authIndexes {
		for _, mindex := range indexes {
			_, err := a.checkConditions(ctx, op, a.conditions[mindex])
			if err == nil {
				used[mindex] = true
				ops[op] = true
				break
			}
		}
	}
	return a.newAuthInfo(used), ops, nil
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

// allowContext holds temporary state used by AuthChecker.allowAny.
type allowContext struct {
	checker *AuthChecker

	// used holds which elements of the request macaroons
	// have been used by the authorization logic.
	used []bool

	// authed holds which of the requested operations have
	// been authorized so far.
	authed []bool

	// need holds all of the requested operations that
	// are remaining to be authorized. needIndex holds the
	// index of each of these operations in the original operations slice
	need      []Op
	needIndex []int

	// errors holds any errors encountered during authorization.
	errors []error
}

// allowAny is the internal version of AllowAny. Instead of returning an
// authInfo struct, it returns a slice describing which operations have
// been successfully authorized and a slice describing which macaroons
// have been used in the authorization.
// If all operations were authorized, authed and err will be nil.
func (a *AuthChecker) allowAny(ctx context.Context, ops []Op) (authed, used []bool, err error) {
	if err := a.init(ctx); err != nil {
		return nil, nil, errgo.Mask(err)
	}
	actx := &allowContext{
		checker:   a,
		used:      make([]bool, len(a.macaroons)),
		authed:    make([]bool, len(ops)),
		need:      append([]Op(nil), ops...),
		needIndex: make([]int, len(ops)),
	}
	for i := range actx.needIndex {
		actx.needIndex[i] = i
	}
	actx.checkDirect(ctx)
	if len(actx.need) == 0 {
		return nil, actx.used, nil
	}
	actx.checkIndirect(ctx)
	if len(actx.need) == 0 {
		return nil, actx.used, nil
	}
	caveats, err := actx.checkWithAuthorizer(ctx)
	if err != nil {
		return actx.authed, actx.used, errgo.Mask(err)
	}
	if len(actx.need) == 0 && len(caveats) == 0 {
		// No more ops need to be authenticated and no caveats to be discharged.
		return actx.authed, actx.used, nil
	}
	logger.Debugf("operations still needed after auth check: %#v", actx.need)
	if a.identity == nil && len(a.identityCaveats) > 0 {
		return actx.authed, actx.used, &DischargeRequiredError{
			Message: "authentication required",
			Ops:     []Op{LoginOp},
			Caveats: a.identityCaveats,
		}
	}
	if len(caveats) == 0 {
		allErrors := make([]error, 0, len(a.initErrors)+len(actx.errors))
		allErrors = append(allErrors, a.initErrors...)
		allErrors = append(allErrors, actx.errors...)
		var err error
		if len(allErrors) > 0 {
			// TODO return all errors?
			logger.Infof("all auth errors: %q", allErrors)
			err = allErrors[0]
		}
		return actx.authed, actx.used, errgo.WithCausef(err, ErrPermissionDenied, "")
	}
	return actx.authed, actx.used, &DischargeRequiredError{
		Message: "some operations have extra caveats",
		Ops:     ops,
		Caveats: caveats,
	}
}

// checkDirect checks which operations are directly authorized by
// the macaroon operations.
func (a *allowContext) checkDirect(ctx context.Context) {
	defer a.updateNeed()
	for i, op := range a.need {
		authed := false
		for _, mindex := range a.checker.authIndexes[op] {
			_, err := a.checker.checkConditions(ctx, op, a.checker.conditions[mindex])
			if err == nil {
				// Use the first authorized macaroon only.
				a.used[mindex] = true
				authed = true
				break
			}
			logger.Infof("condition check %q failed: %v", a.checker.conditions[mindex], err)
			a.addError(err)
		}
		// Allow LoginOp when there's an authenticated user even
		// when there's no macaroon that specifically authorizes it.
		authed = authed || (op == LoginOp && a.checker.identity != nil)
		if authed {
			a.authed[a.needIndex[i]] = true
		}
	}
	if a.checker.identity == nil {
		return
	}
	// We've authenticated as a user, so even if the operations didn't
	// specifically require it, we add the login macaroon
	// to the macaroons used.
	// Note that the LoginOp conditions have already been checked
	// successfully in initOnceFunc so no need to check again.
	// Note also that there may not be any macaroons if the
	// identity client decided on an identity even with no
	// macaroons.
	for _, i := range a.checker.authIndexes[LoginOp] {
		a.used[i] = true
	}
}

// checkIndirect checks to see if any of the remaining operations are authorized
// indirectly with the already-authorized operations.
func (a *allowContext) checkIndirect(ctx context.Context) {
	if a.checker.p.OpsAuthorizer == nil {
		return
	}
	for op, mindexes := range a.checker.authIndexes {
		if len(a.need) == 0 {
			return
		}
		authedOK, err := a.checker.p.OpsAuthorizer.AuthorizeOps(ctx, op, a.need)
		if err != nil {
			// TODO this probably means "can't check" rather than "authorization denied";
			// perhaps we should return rather than carrying on?
			a.addError(err)
			continue
		}
		for i, ok := range authedOK {
			if !ok {
				continue
			}
			// This operation is potentially authorized. See whether we have a macaroon
			// that actually allows this operation.
			for _, mindex := range mindexes {
				if _, err := a.checker.checkConditions(ctx, a.need[i], a.checker.conditions[mindex]); err != nil {
					a.addError(err)
					continue
				}
				// Operation is authorized. Mark the appropriate macaroon as used,
				// and remove the operation from the needed list so that we don't
				// bother AuthorizeOps with it again.
				a.used[mindex] = true
				a.authed[a.needIndex[i]] = true
			}
		}
		a.updateNeed()
	}
}

// checkWithAuthorizer checks which operations are authorized by the
// Authorizer instance. We call Authorize even when we haven't got an
// authenticated identity.
func (a *allowContext) checkWithAuthorizer(ctx context.Context) ([]checkers.Caveat, error) {
	oks, caveats, err := a.checker.p.Authorizer.Authorize(ctx, a.checker.identity, a.need)
	if err != nil {
		// TODO if there are macaroons supplied that have failed, perhaps we shouldn't
		// do this but return those errors instead? Doing things the current
		// way means that we lose the previous errors.
		return nil, errgo.Notef(err, "cannot check permissions")
	}
	for i, ok := range oks {
		if ok {
			a.authed[a.needIndex[i]] = true
		}
	}
	a.updateNeed()
	return caveats, nil
}

// updateNeed removes all authorized operations from a.need
// and updates a.needIndex appropriately too.
func (a *allowContext) updateNeed() {
	j := 0
	for i, opIndex := range a.needIndex {
		if a.authed[opIndex] {
			continue
		}
		if i != j {
			a.need[j], a.needIndex[j] = a.need[i], a.needIndex[i]
		}
		j++
	}
	a.need, a.needIndex = a.need[0:j], a.needIndex[0:j]
}

func (a *allowContext) addError(err error) {
	a.errors = append(a.errors, err)
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
