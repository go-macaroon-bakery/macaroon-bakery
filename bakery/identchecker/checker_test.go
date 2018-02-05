package identchecker_test

import (
	"sort"
	"strings"

	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	"golang.org/x/net/context"
	gc "gopkg.in/check.v1"
	errgo "gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v2"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2/bakery/identchecker"
)

type dischargeRecord struct {
	location string
	user     string
}

type checkerSuite struct {
	jujutesting.LoggingSuite
	discharges []dischargeRecord
}

var _ = gc.Suite(&checkerSuite{})

func (s *checkerSuite) SetUpTest(c *gc.C) {
	s.discharges = nil
	s.LoggingSuite.SetUpTest(c)
}

func (s *checkerSuite) TestAuthorizeWithOpenAccessAndNoMacaroons(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := opACL{readOp("something"): {identchecker.Everyone}}
	ts := newService(auth, ids, locator)
	client := newClient(locator)

	authInfo, err := client.do(testContext, ts, readOp("something"))
	c.Assert(err, gc.IsNil)
	c.Assert(s.discharges, gc.HasLen, 0)
	c.Assert(authInfo, gc.NotNil)
	c.Assert(authInfo.Identity, gc.Equals, nil)
	c.Assert(authInfo.Macaroons, gc.HasLen, 0)
}

func (s *checkerSuite) TestAuthorizationDenied(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := identchecker.ClosedAuthorizer
	ts := newService(auth, ids, locator)
	client := newClient(locator)

	authInfo, err := client.do(asUser("bob"), ts, readOp("something"))
	c.Assert(err, gc.ErrorMatches, `permission denied`)
	c.Assert(authInfo, gc.IsNil)
}

func (s *checkerSuite) TestAuthorizeWithAuthenticationRequired(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := opACL{readOp("something"): {"bob"}}
	ts := newService(auth, ids, locator)
	client := newClient(locator)

	authInfo, err := client.do(asUser("bob"), ts, readOp("something"))
	c.Assert(err, gc.IsNil)

	c.Assert(s.discharges, jc.DeepEquals, []dischargeRecord{{
		location: "ids",
		user:     "bob",
	}})
	c.Assert(authInfo, gc.NotNil)
	c.Assert(authInfo.Identity, gc.Equals, identchecker.SimpleIdentity("bob"))
	c.Assert(authInfo.Macaroons, gc.HasLen, 1)
}

func asUser(username string) context.Context {
	return contextWithDischargeUser(testContext, username)
}

func (s *checkerSuite) TestAuthorizeMultipleOps(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := opACL{readOp("something"): {"bob"}, readOp("otherthing"): {"bob"}}
	ts := newService(auth, ids, locator)
	client := newClient(locator)

	_, err := client.do(asUser("bob"), ts, readOp("something"), readOp("otherthing"))
	c.Assert(err, gc.IsNil)

	c.Assert(s.discharges, jc.DeepEquals, []dischargeRecord{{
		location: "ids",
		user:     "bob",
	}})
}

func (s *checkerSuite) TestCapability(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := opACL{readOp("something"): {"bob"}}
	ts := newService(auth, ids, locator)
	client := newClient(locator)

	m, err := client.dischargedCapability(asUser("bob"), ts, readOp("something"))
	c.Assert(err, gc.IsNil)

	// Check that we can exercise the capability directly on the service
	// with no discharging required.
	authInfo, err := ts.do(testContext, []macaroon.Slice{m}, readOp("something"))
	c.Assert(err, gc.IsNil)
	c.Assert(authInfo, gc.NotNil)
	c.Assert(authInfo.Identity, gc.Equals, nil)
	c.Assert(authInfo.Macaroons, gc.HasLen, 1)
	c.Assert(authInfo.Macaroons[0][0].Id(), jc.DeepEquals, m[0].Id())
}

func (s *checkerSuite) TestCapabilityMultipleEntities(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := opACL{readOp("e1"): {"bob"}, readOp("e2"): {"bob"}, readOp("e3"): {"bob"}}
	ts := newService(auth, ids, locator)
	client := newClient(locator)

	m, err := client.dischargedCapability(asUser("bob"), ts, readOp("e1"), readOp("e2"), readOp("e3"))
	c.Assert(err, gc.IsNil)

	c.Assert(s.discharges, jc.DeepEquals, []dischargeRecord{{
		location: "ids",
		user:     "bob",
	}})

	// Check that we can exercise the capability directly on the service
	// with no discharging required.
	_, err = ts.do(testContext, []macaroon.Slice{m}, readOp("e1"), readOp("e2"), readOp("e3"))
	c.Assert(err, gc.IsNil)

	// Check that we can exercise the capability to act on a subset of the operations.
	_, err = ts.do(testContext, []macaroon.Slice{m}, readOp("e2"), readOp("e3"))
	c.Assert(err, gc.IsNil)
	_, err = ts.do(testContext, []macaroon.Slice{m}, readOp("e3"))
	c.Assert(err, gc.IsNil)
}

func (s *checkerSuite) TestMultipleCapabilities(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := opACL{readOp("e1"): {"alice"}, readOp("e2"): {"bob"}}
	ts := newService(auth, ids, locator)

	// Acquire two capabilities as different users and check
	// that we can combine them together to do both operations
	// at once.
	m1, err := newClient(locator).dischargedCapability(asUser("alice"), ts, readOp("e1"))
	c.Assert(err, gc.IsNil)
	m2, err := newClient(locator).dischargedCapability(asUser("bob"), ts, readOp("e2"))
	c.Assert(err, gc.IsNil)

	c.Assert(s.discharges, jc.DeepEquals, []dischargeRecord{{
		location: "ids",
		user:     "alice",
	}, {
		location: "ids",
		user:     "bob",
	}})

	authInfo, err := ts.do(testContext, []macaroon.Slice{m1, m2}, readOp("e1"), readOp("e2"))
	c.Assert(err, gc.IsNil)

	c.Assert(authInfo, gc.NotNil)
	c.Assert(authInfo.Identity, gc.Equals, nil)
	c.Assert(authInfo.Macaroons, gc.HasLen, 2)
	c.Assert(authInfo.Macaroons[0][0].Id(), jc.DeepEquals, m1[0].Id())
	c.Assert(authInfo.Macaroons[1][0].Id(), jc.DeepEquals, m2[0].Id())
}

func (s *checkerSuite) TestCombineCapabilities(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := opACL{readOp("e1"): {"alice"}, readOp("e2"): {"bob"}, readOp("e3"): {"bob", "alice"}}
	ts := newService(auth, ids, locator)

	// Acquire two capabilities as different users and check
	// that we can combine them together into a single capability
	// capable of both operations.
	m1, err := newClient(locator).dischargedCapability(asUser("alice"), ts, readOp("e1"), readOp("e3"))
	c.Assert(err, gc.IsNil)
	m2, err := newClient(locator).dischargedCapability(asUser("bob"), ts, readOp("e2"))
	c.Assert(err, gc.IsNil)

	m, err := ts.capability(testContext, []macaroon.Slice{m1, m2}, readOp("e1"), readOp("e2"), readOp("e3"))
	c.Assert(err, gc.IsNil)

	_, err = ts.do(testContext, []macaroon.Slice{{m.M()}}, readOp("e1"), readOp("e2"), readOp("e3"))
	c.Assert(err, gc.IsNil)
}

func (s *checkerSuite) TestPartiallyAuthorizedRequest(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := opACL{readOp("e1"): {"alice"}, readOp("e2"): {"bob"}}
	ts := newService(auth, ids, locator)

	// Acquire a capability for e1 but rely on authentication to
	// authorize e2.
	m, err := newClient(locator).dischargedCapability(asUser("alice"), ts, readOp("e1"))
	c.Assert(err, gc.IsNil)

	client := newClient(locator)
	client.addMacaroon(ts, "authz", m)

	_, err = client.do(asUser("bob"), ts, readOp("e1"), readOp("e2"))
	c.Assert(err, gc.IsNil)
}

func (s *checkerSuite) TestAuthWithThirdPartyCaveats(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)

	// We make an authorizer that requires a third party discharge
	// when authorizing.
	auth := identchecker.AuthorizerFunc(func(_ context.Context, id identchecker.Identity, op bakery.Op) (bool, []checkers.Caveat, error) {
		if id == identchecker.SimpleIdentity("bob") && op == readOp("something") {
			return true, []checkers.Caveat{{
				Condition: "question",
				Location:  "other third party",
			}}, nil
		}
		return false, nil, nil
	})
	ts := newService(auth, ids, locator)

	locator["other third party"] = &discharger{
		key: mustGenerateKey(),
		checker: bakery.ThirdPartyCaveatCheckerFunc(func(ctx context.Context, info *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
			if string(info.Condition) != "question" {
				return nil, errgo.Newf("third party condition not recognized")
			}
			s.discharges = append(s.discharges, dischargeRecord{
				location: "other third party",
				user:     dischargeUserFromContext(ctx),
			})
			return nil, nil
		}),
		locator: locator,
	}

	client := newClient(locator)
	_, err := client.do(asUser("bob"), ts, readOp("something"))
	c.Assert(err, gc.IsNil)

	c.Assert(s.discharges, jc.DeepEquals, []dischargeRecord{{
		location: "ids",
		user:     "bob",
	}, {
		location: "other third party",
		user:     "bob",
	}})
}

func (s *checkerSuite) TestCapabilityCombinesFirstPartyCaveats(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := opACL{readOp("e1"): {"alice"}, readOp("e2"): {"bob"}}
	ts := newService(auth, ids, locator)

	// Acquire two capabilities as different users, add some first party caveats
	//
	// that we can combine them together into a single capability
	// capable of both operations.
	m1, err := newClient(locator).capability(asUser("alice"), ts, readOp("e1"))
	c.Assert(err, gc.IsNil)
	m1.M().AddFirstPartyCaveat([]byte("true 1"))
	m1.M().AddFirstPartyCaveat([]byte("true 2"))
	m2, err := newClient(locator).capability(asUser("bob"), ts, readOp("e2"))
	c.Assert(err, gc.IsNil)
	m2.M().AddFirstPartyCaveat([]byte("true 3"))
	m2.M().AddFirstPartyCaveat([]byte("true 4"))

	client := newClient(locator)
	client.addMacaroon(ts, "authz1", macaroon.Slice{m1.M()})
	client.addMacaroon(ts, "authz2", macaroon.Slice{m2.M()})

	m, err := client.capability(testContext, ts, readOp("e1"), readOp("e2"))
	c.Assert(err, gc.IsNil)

	c.Assert(macaroonConditions(m.M().Caveats(), false), jc.DeepEquals, []string{
		"true 1",
		"true 2",
		"true 3",
		"true 4",
	})
}

func (s *checkerSuite) TestLoginOnly(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := identchecker.ClosedAuthorizer
	ts := newService(auth, ids, locator)
	authInfo, err := newClient(locator).do(asUser("bob"), ts, identchecker.LoginOp)
	c.Assert(err, gc.IsNil)
	c.Assert(authInfo.Identity, gc.Equals, identchecker.SimpleIdentity("bob"))
}

func (s *checkerSuite) TestAuthWithIdentityFromContext(c *gc.C) {
	locator := make(dischargerLocator)
	ids := basicAuthIdService{}
	auth := opACL{readOp("e1"): {"sherlock"}, readOp("e2"): {"bob"}}
	ts := newService(auth, ids, locator)

	// Check that we can perform the ops with basic auth in the
	// context.
	authInfo, err := newClient(locator).do(contextWithBasicAuth(testContext, "sherlock", "holmes"), ts, readOp("e1"))
	c.Assert(err, gc.IsNil)
	c.Assert(authInfo.Identity, gc.Equals, identchecker.SimpleIdentity("sherlock"))
	c.Assert(authInfo.Macaroons, gc.HasLen, 0)
}

func (s *checkerSuite) TestAuthLoginOpWithIdentityFromContext(c *gc.C) {
	locator := make(dischargerLocator)
	ids := basicAuthIdService{}
	ts := newService(nil, ids, locator)

	// Check that we can use LoginOp when auth isn't granted through macaroons.
	authInfo, err := newClient(locator).do(contextWithBasicAuth(testContext, "sherlock", "holmes"), ts, identchecker.LoginOp)
	c.Assert(err, gc.IsNil)
	if err == nil && authInfo == nil {
		c.Fatalf("nil err and nil authInfo")
	}
	c.Assert(authInfo.Identity, gc.Equals, identchecker.SimpleIdentity("sherlock"))
	c.Assert(authInfo.Macaroons, gc.HasLen, 0)
}

func (s *checkerSuite) TestAllowWithOpsAuthorizer(c *gc.C) {
	locator := make(dischargerLocator)
	oven := newMacaroonStore(mustGenerateKey(), locator)
	ts := &service{
		// Note: we're making a checker with no Authorizer and no IdentityClient.
		checker: identchecker.NewChecker(identchecker.CheckerParams{
			Checker:          testChecker,
			OpsAuthorizer:    hierarchicalOpsAuthorizer{},
			MacaroonVerifier: oven,
		}),
		oven: oven,
	}
	// Manufacture a macaroon granting access to /user/bob and
	// everything underneath it (by virtue of the hierarchicalOpsAuthorizer).
	m, err := ts.oven.NewMacaroon(testContext, bakery.LatestVersion, nil, bakery.Op{
		Entity: "path-/user/bob",
		Action: "*",
	})
	c.Assert(err, gc.Equals, nil)
	// Check that we can do some operation.
	_, err = ts.do(testContext, []macaroon.Slice{{m.M()}}, writeOp("path-/user/bob/foo"))
	c.Assert(err, gc.Equals, nil)

	// Check that we can't do an operation on an entity outside the
	// original operation's purview.
	_, err = ts.do(testContext, []macaroon.Slice{{m.M()}}, writeOp("path-/user/alice"))
	c.Assert(err, gc.ErrorMatches, `permission denied`)
}

func (s *checkerSuite) TestDuplicateLoginMacaroons(c *gc.C) {
	locator := make(dischargerLocator)
	ids := s.newIdService("ids", locator)
	auth := identchecker.ClosedAuthorizer
	ts := newService(auth, ids, locator)

	// Acquire a login macaroon for bob.
	client1 := newClient(locator)
	authInfo, err := client1.do(asUser("bob"), ts, identchecker.LoginOp)
	c.Assert(err, gc.IsNil)
	c.Assert(authInfo.Identity, gc.Equals, identchecker.SimpleIdentity("bob"))

	// Acquire a login macaroon for alice.
	client2 := newClient(locator)
	authInfo, err = client2.do(asUser("alice"), ts, identchecker.LoginOp)
	c.Assert(err, gc.IsNil)
	c.Assert(authInfo.Identity, gc.Equals, identchecker.SimpleIdentity("alice"))

	// Combine the two login macaroons into one client.
	client3 := newClient(locator)
	client3.addMacaroon(ts, "1.bob", client1.macaroons[ts]["authn"])
	client3.addMacaroon(ts, "2.alice", client2.macaroons[ts]["authn"])

	// We should authenticate as bob (because macaroons are presented ordered
	// by "cookie"name)
	authInfo, err = client3.do(testContext, ts, identchecker.LoginOp)
	c.Assert(err, gc.IsNil)
	c.Assert(authInfo.Identity, gc.Equals, identchecker.SimpleIdentity("bob"))
	c.Assert(authInfo.Used, jc.DeepEquals, []bool{true, false})

	// Try them the other way around and we should authenticate as alice.
	client3 = newClient(locator)
	client3.addMacaroon(ts, "1.alice", client2.macaroons[ts]["authn"])
	client3.addMacaroon(ts, "2.bob", client1.macaroons[ts]["authn"])

	authInfo, err = client3.do(testContext, ts, identchecker.LoginOp)
	c.Assert(err, gc.IsNil)
	c.Assert(authInfo.Identity, gc.Equals, identchecker.SimpleIdentity("alice"))
	c.Assert(authInfo.Used, jc.DeepEquals, []bool{true, false})
}

func (s *checkerSuite) TestMacaroonOpsFatalError(c *gc.C) {
	// When we get a non-VerificationError error from the
	// oven, we don't do any more verification.
	checker := identchecker.NewChecker(identchecker.CheckerParams{
		MacaroonVerifier: macaroonVerifierWithError{errgo.New("an error")},
	})
	m, err := macaroon.New(nil, nil, "", macaroon.V2)
	c.Assert(err, gc.IsNil)
	_, err = checker.Auth(macaroon.Slice{m}).Allow(testContext, identchecker.LoginOp)
	c.Assert(err, gc.ErrorMatches, `cannot retrieve macaroon: an error`)
}

func macaroonConditions(caveats []macaroon.Caveat, allowThird bool) []string {
	conds := make([]string, len(caveats))
	for i, cav := range caveats {
		if cav.Location != "" {
			if !allowThird {
				panic("found unexpected third party caveat")
			}
			continue
		}
		conds[i] = string(cav.Id)
	}
	return conds
}

func readOp(entity string) bakery.Op {
	return bakery.Op{
		Entity: entity,
		Action: "read",
	}
}

func writeOp(entity string) bakery.Op {
	return bakery.Op{
		Entity: entity,
		Action: "write",
	}
}

// opACL implements identchecker.Authorizer by looking the operation
// up in the given map. If the username is in the associated slice
// or the slice contains "everyone", authorization is granted.
type opACL map[bakery.Op][]string

func (auth opACL) Authorize(ctx context.Context, id identchecker.Identity, ops []bakery.Op) (allowed []bool, caveats []checkers.Caveat, err error) {
	return identchecker.ACLAuthorizer{
		GetACL: func(ctx context.Context, op bakery.Op) ([]string, bool, error) {
			return auth[op], true, nil
		},
	}.Authorize(ctx, id, ops)
}

type idService struct {
	location string
	*discharger
	suite *checkerSuite
}

func (s *checkerSuite) newIdService(location string, locator dischargerLocator) *idService {
	ids := &idService{
		location: location,
		suite:    s,
	}
	key := mustGenerateKey()
	ids.discharger = &discharger{
		key:     key,
		checker: ids,
		locator: locator,
	}
	locator[location] = ids.discharger
	return ids
}

func (ids *idService) CheckThirdPartyCaveat(ctx context.Context, info *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
	if string(info.Condition) != "is-authenticated-user" {
		return nil, errgo.Newf("third party condition not recognized")
	}
	username := dischargeUserFromContext(ctx)
	if username == "" {
		return nil, errgo.Newf("no current user")
	}
	ids.suite.discharges = append(ids.suite.discharges, dischargeRecord{
		location: ids.location,
		user:     username,
	})
	return []checkers.Caveat{
		checkers.DeclaredCaveat("username", username),
	}, nil
}

func (ids *idService) IdentityFromContext(ctx context.Context) (identchecker.Identity, []checkers.Caveat, error) {
	return nil, []checkers.Caveat{{
		Location:  ids.location,
		Condition: "is-authenticated-user",
	}}, nil
}

func (ids *idService) DeclaredIdentity(ctx context.Context, declared map[string]string) (identchecker.Identity, error) {
	user, ok := declared["username"]
	if !ok {
		return nil, errgo.Newf("no username declared")
	}
	return identchecker.SimpleIdentity(user), nil
}

type dischargeUserKey struct{}

func contextWithDischargeUser(ctx context.Context, username string) context.Context {
	return context.WithValue(ctx, dischargeUserKey{}, username)
}

func dischargeUserFromContext(ctx context.Context) string {
	username, _ := ctx.Value(dischargeUserKey{}).(string)
	return username
}

type basicAuthIdService struct{}

func (basicAuthIdService) IdentityFromContext(ctx context.Context) (identchecker.Identity, []checkers.Caveat, error) {
	user, pass := basicAuthFromContext(ctx)
	if user != "sherlock" || pass != "holmes" {
		return nil, nil, nil
	}
	return identchecker.SimpleIdentity(user), nil, nil
}

func (basicAuthIdService) DeclaredIdentity(ctx context.Context, declared map[string]string) (identchecker.Identity, error) {
	return nil, errgo.Newf("no identity declarations in basic auth id service")
}

// service represents a service that requires authorization.
// Clients can make requests to the service to perform operations
// and may receive a macaroon to discharge if the authorization
// process requires it.
type service struct {
	checker *identchecker.Checker
	oven    *bakery.Oven
}

func newService(auth identchecker.Authorizer, idm identchecker.IdentityClient, locator bakery.ThirdPartyLocator) *service {
	oven := newMacaroonStore(mustGenerateKey(), locator)
	return &service{
		checker: identchecker.NewChecker(identchecker.CheckerParams{
			Checker:          testChecker,
			Authorizer:       auth,
			IdentityClient:   idm,
			MacaroonVerifier: oven,
		}),
		oven: oven,
	}
}

// do makes a request to the service to perform the given operations
// using the given macaroons for authorization.
// It may return a dischargeRequiredError containing a macaroon
// that needs to be discharged.
func (svc *service) do(ctx context.Context, ms []macaroon.Slice, ops ...bakery.Op) (*identchecker.AuthInfo, error) {
	authInfo, err := svc.checker.Auth(ms...).Allow(ctx, ops...)
	return authInfo, svc.maybeDischargeRequiredError(err)
}

func (svc *service) capability(ctx context.Context, ms []macaroon.Slice, ops ...bakery.Op) (*bakery.Macaroon, error) {
	ai, err := svc.checker.Auth(ms...).Allow(ctx, ops...)
	if err != nil {
		return nil, svc.maybeDischargeRequiredError(err)
	}
	m, err := svc.oven.NewMacaroon(ctx, bakery.LatestVersion, nil, ops...)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	for _, cond := range ai.Conditions() {
		if strings.HasPrefix(cond, "declared ") {
			// TODO check namespace too.
			continue
		}
		if err := m.M().AddFirstPartyCaveat([]byte(cond)); err != nil {
			return nil, errgo.Mask(err)
		}
	}
	return m, nil
}

func (svc *service) maybeDischargeRequiredError(err error) error {
	derr, ok := errgo.Cause(err).(*bakery.DischargeRequiredError)
	if !ok {
		return errgo.Mask(err)
	}
	// If we're making a login macaroon, it should have ForAuthentication
	// set in derr.
	for _, op := range derr.Ops {
		if op == identchecker.LoginOp && !derr.ForAuthentication {
			panic(errgo.Newf("found discharge-required error for LoginOp without ForAuthentication"))
		}
	}
	m, err := svc.oven.NewMacaroon(testContext, bakery.LatestVersion, derr.Caveats, derr.Ops...)
	if err != nil {
		return errgo.Mask(err)
	}
	name := "authz"
	if len(derr.Ops) == 1 && derr.Ops[0] == identchecker.LoginOp {
		name = "authn"
	}
	return &dischargeRequiredError{
		name: name,
		m:    m,
	}
}

type discharger struct {
	key     *bakery.KeyPair
	locator bakery.ThirdPartyLocator
	checker bakery.ThirdPartyCaveatChecker
}

type dischargeRequiredError struct {
	name string
	m    *bakery.Macaroon
}

func (*dischargeRequiredError) Error() string {
	return "discharge required"
}

func (d *discharger) discharge(ctx context.Context, cav macaroon.Caveat, payload []byte) (*bakery.Macaroon, error) {
	m, err := bakery.Discharge(ctx, bakery.DischargeParams{
		Id:      cav.Id,
		Caveat:  payload,
		Key:     d.key,
		Checker: d.checker,
		Locator: d.locator,
	})
	if err != nil {
		return nil, errgo.Mask(err)
	}
	return m, nil
}

type dischargerLocator map[string]*discharger

// ThirdPartyInfo implements the bakery.ThirdPartyLocator interface.
func (l dischargerLocator) ThirdPartyInfo(ctx context.Context, loc string) (bakery.ThirdPartyInfo, error) {
	d, ok := l[loc]
	if !ok {
		return bakery.ThirdPartyInfo{}, bakery.ErrNotFound
	}
	return bakery.ThirdPartyInfo{
		PublicKey: d.key.Public,
		Version:   bakery.LatestVersion,
	}, nil
}

type hierarchicalOpsAuthorizer struct{}

func (a hierarchicalOpsAuthorizer) AuthorizeOps(ctx context.Context, authorizedOp bakery.Op, queryOps []bakery.Op) ([]bool, []checkers.Caveat, error) {
	ok := make([]bool, len(queryOps))
	for i, op := range queryOps {
		if isParentPathEntity(authorizedOp.Entity, op.Entity) && (authorizedOp.Action == op.Action || authorizedOp.Action == "*") {
			ok[i] = true
		}
	}
	return ok, nil, nil
}

// isParentPathEntity reports whether both entity1 and entity2
// represent paths and entity1 is a parent of entity2.
func isParentPathEntity(entity1, entity2 string) bool {
	path1, path2 := strings.TrimPrefix(entity1, "path-"), strings.TrimPrefix(entity2, "path-")
	if len(path1) == len(entity1) || len(path2) == len(entity2) {
		return false
	}
	if !strings.HasPrefix(path2, path1) {
		return false
	}
	if len(path1) == len(path2) {
		return true
	}
	return path2[len(path1)] == '/'
}

type client struct {
	key         *bakery.KeyPair
	macaroons   map[*service]map[string]macaroon.Slice
	dischargers dischargerLocator
}

func newClient(dischargers dischargerLocator) *client {
	return &client{
		key:         mustGenerateKey(),
		dischargers: dischargers,
		// macaroons holds the macaroons applicable to each service.
		// This is the test equivalent of the cookie jar used by httpbakery.
		macaroons: make(map[*service]map[string]macaroon.Slice),
	}
}

const maxRetries = 3

// do performs a set of operations on the given service.
// It includes all the macaroons in c.macaroons[svc] as authorization
// information on the request.
func (c *client) do(ctx context.Context, svc *service, ops ...bakery.Op) (*identchecker.AuthInfo, error) {
	var authInfo *identchecker.AuthInfo
	err := c.doFunc(ctx, svc, func(ms []macaroon.Slice) (err error) {
		authInfo, err = svc.do(ctx, ms, ops...)
		return
	})
	return authInfo, err
}

// capability returns a capability macaroon for the given operations.
func (c *client) capability(ctx context.Context, svc *service, ops ...bakery.Op) (*bakery.Macaroon, error) {
	var m *bakery.Macaroon
	err := c.doFunc(ctx, svc, func(ms []macaroon.Slice) (err error) {
		m, err = svc.capability(ctx, ms, ops...)
		return
	})
	return m, err
}

func (c *client) dischargedCapability(ctx context.Context, svc *service, ops ...bakery.Op) (macaroon.Slice, error) {
	m, err := c.capability(ctx, svc, ops...)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	return c.dischargeAll(ctx, m)
}

func (c *client) doFunc(ctx context.Context, svc *service, f func(ms []macaroon.Slice) error) error {
	for i := 0; i < maxRetries; i++ {
		err := f(c.requestMacaroons(svc))
		derr, ok := errgo.Cause(err).(*dischargeRequiredError)
		if !ok {
			return err
		}
		ms, err := c.dischargeAll(ctx, derr.m)
		if err != nil {
			return errgo.Mask(err)
		}
		c.addMacaroon(svc, derr.name, ms)
	}
	return errgo.New("discharge failed too many times")
}

func (c *client) clearMacaroons(svc *service) {
	if svc == nil {
		c.macaroons = make(map[*service]map[string]macaroon.Slice)
		return
	}
	delete(c.macaroons, svc)
}

func (c *client) addMacaroon(svc *service, name string, m macaroon.Slice) {
	if c.macaroons[svc] == nil {
		c.macaroons[svc] = make(map[string]macaroon.Slice)
	}
	c.macaroons[svc][name] = m
}

func (c *client) requestMacaroons(svc *service) []macaroon.Slice {
	mmap := c.macaroons[svc]
	// Put all the macaroons in the slice ordered by key
	// so that we have deterministic behaviour in the tests.
	names := make([]string, 0, len(mmap))
	for name := range mmap {
		names = append(names, name)
	}
	sort.Strings(names)
	ms := make([]macaroon.Slice, len(names))
	for i, name := range names {
		ms[i] = mmap[name]
	}
	return ms
}

func (c *client) dischargeAll(ctx context.Context, m *bakery.Macaroon) (macaroon.Slice, error) {
	return bakery.DischargeAll(ctx, m, func(ctx context.Context, cav macaroon.Caveat, payload []byte) (*bakery.Macaroon, error) {
		d := c.dischargers[cav.Location]
		if d == nil {
			return nil, errgo.Newf("third party discharger %q not found", cav.Location)
		}
		return d.discharge(ctx, cav, payload)
	})
}

func newMacaroonStore(key *bakery.KeyPair, locator bakery.ThirdPartyLocator) *bakery.Oven {
	return bakery.NewOven(bakery.OvenParams{
		Key:     key,
		Locator: locator,
	})
}

// macaroonVerifierWithError is an implementation of MacaroonVerifier that
// returns the given error on all store operations.
type macaroonVerifierWithError struct {
	err error
}

func (s macaroonVerifierWithError) VerifyMacaroon(ctx context.Context, ms macaroon.Slice) (ops []bakery.Op, conditions []string, err error) {
	return nil, nil, errgo.Mask(s.err, errgo.Any)
}
