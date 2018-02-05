package bakery_test

import (
	"sort"
	"strings"
	"time"

	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	"golang.org/x/net/context"
	gc "gopkg.in/check.v1"
	errgo "gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v2"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
)

type checkerSuite struct {
	jujutesting.LoggingSuite
}

var _ = gc.Suite(&checkerSuite{})

func (s *checkerSuite) TestCapability(c *gc.C) {
	ts := newService(nil)

	m := ts.newMacaroon(readOp("something"))

	// Check that we can exercise the capability directly on the service
	// with no discharging required.
	authInfo, err := ts.do(testContext, []macaroon.Slice{m}, readOp("something"))
	c.Assert(err, gc.IsNil)
	c.Assert(authInfo, gc.NotNil)
	c.Assert(authInfo.Macaroons, gc.HasLen, 1)
	c.Assert(authInfo.Macaroons[0][0].Id(), jc.DeepEquals, m[0].Id())
	c.Assert(authInfo.Used, jc.DeepEquals, []bool{true})
}

func (s *checkerSuite) TestCapabilityMultipleEntities(c *gc.C) {
	ts := newService(nil)

	m := ts.newMacaroon(readOp("e1"), readOp("e2"), readOp("e3"))

	// Check that we can exercise the capability directly on the service
	// with no discharging required.
	_, err := ts.do(testContext, []macaroon.Slice{m}, readOp("e1"), readOp("e2"), readOp("e3"))
	c.Assert(err, gc.IsNil)

	// Check that we can exercise the capability to act on a subset of the operations.
	_, err = ts.do(testContext, []macaroon.Slice{m}, readOp("e2"), readOp("e3"))
	c.Assert(err, gc.IsNil)
	_, err = ts.do(testContext, []macaroon.Slice{m}, readOp("e3"))
	c.Assert(err, gc.IsNil)
}

func (s *checkerSuite) TestMultipleCapabilities(c *gc.C) {
	ts := newService(nil)

	// Acquire two capabilities as different users and check
	// that we can combine them together to do both operations
	// at once.
	m1 := ts.newMacaroon(readOp("e1"))
	m2 := ts.newMacaroon(readOp("e2"))

	authInfo, err := ts.do(testContext, []macaroon.Slice{m1, m2}, readOp("e1"), readOp("e2"))
	c.Assert(err, gc.IsNil)

	c.Assert(authInfo, gc.NotNil)
	c.Assert(authInfo.Macaroons, gc.HasLen, 2)
	c.Assert(authInfo.Used, jc.DeepEquals, []bool{true, true})
}

func (s *checkerSuite) TestCombineCapabilities(c *gc.C) {
	ts := newService(nil)

	// Acquire two capabilities as different users and check
	// that we can combine them together into a single capability
	// capable of both operations.

	m1 := ts.newMacaroon(readOp("e1"), readOp("e3"))
	m2 := ts.newMacaroon(readOp("e2"))

	m, err := ts.capability(testContext, []macaroon.Slice{m1, m2}, readOp("e1"), readOp("e2"), readOp("e3"))
	c.Assert(err, gc.IsNil)

	_, err = ts.do(testContext, []macaroon.Slice{{m.M()}}, readOp("e1"), readOp("e2"), readOp("e3"))
	c.Assert(err, gc.IsNil)
}

func (s *checkerSuite) TestCapabilityCombinesFirstPartyCaveats(c *gc.C) {
	ts := newService(nil)

	// Acquire two capabilities as different users, add some first party caveats
	// and combine them together into a single capability
	// capable of both operations.
	m1 := ts.newMacaroon(readOp("e1"))
	m1[0].AddFirstPartyCaveat([]byte("true 1"))
	m1[0].AddFirstPartyCaveat([]byte("true 2"))
	m2 := ts.newMacaroon(readOp("e2"))
	m2[0].AddFirstPartyCaveat([]byte("true 3"))
	m2[0].AddFirstPartyCaveat([]byte("true 4"))

	client := newClient(nil)
	client.addMacaroon(ts, "authz1", m1)
	client.addMacaroon(ts, "authz2", m2)

	m, err := client.capability(testContext, ts, readOp("e1"), readOp("e2"))
	c.Assert(err, gc.IsNil)

	c.Assert(macaroonConditions(m.M().Caveats(), false), jc.DeepEquals, []string{
		"true 1",
		"true 2",
		"true 3",
		"true 4",
	})
}

var firstPartyCaveatSquashingTests = []struct {
	about   string
	caveats []checkers.Caveat
	expect  []checkers.Caveat
}{{
	about: "duplicates removed",
	caveats: []checkers.Caveat{
		trueCaveat("1"),
		trueCaveat("2"),
		trueCaveat("1"),
		trueCaveat("2"),
		trueCaveat("3"),
	},
	expect: []checkers.Caveat{
		trueCaveat("1"),
		trueCaveat("2"),
		trueCaveat("3"),
	},
}, {
	about: "earliest time before",
	caveats: []checkers.Caveat{
		checkers.TimeBeforeCaveat(epoch.Add(24 * time.Hour)),
		trueCaveat("1"),
		checkers.TimeBeforeCaveat(epoch.Add(1 * time.Hour)),
		checkers.TimeBeforeCaveat(epoch.Add(5 * time.Minute)),
	},
	expect: []checkers.Caveat{
		checkers.TimeBeforeCaveat(epoch.Add(5 * time.Minute)),
		trueCaveat("1"),
	},
}}

func (s *checkerSuite) TestFirstPartyCaveatSquashing(c *gc.C) {
	ts := newService(nil)
	for i, test := range firstPartyCaveatSquashingTests {
		c.Logf("test %d: %v", i, test.about)

		// Make a first macaroon with all the required first party caveats.
		m1 := ts.newMacaroon(readOp("e1"))
		for _, cond := range resolveCaveats(ts.checker.Namespace(), test.caveats) {
			err := m1[0].AddFirstPartyCaveat([]byte(cond))
			c.Assert(err, gc.Equals, nil)
		}

		m2 := ts.newMacaroon(readOp("e2"))
		err := m2[0].AddFirstPartyCaveat([]byte("notused"))
		c.Assert(err, gc.Equals, nil)

		client := newClient(nil)
		client.addMacaroon(ts, "authz1", m1)
		client.addMacaroon(ts, "authz2", m2)

		m3, err := client.capability(testContext, ts, readOp("e1"))
		c.Assert(err, gc.IsNil)
		c.Assert(macaroonConditions(m3.M().Caveats(), false), jc.DeepEquals, resolveCaveats(m3.Namespace(), test.expect))
	}
}

func (s *checkerSuite) TestAllowDirect(c *gc.C) {
	ts := newService(nil)
	client := newClient(nil)
	client.addMacaroon(ts, "auth1", ts.newMacaroon(readOp("e1")))
	client.addMacaroon(ts, "auth2", ts.newMacaroon(readOp("e2")))
	ai, err := client.do(testContext, ts, readOp("e1"), readOp("e2"))
	c.Assert(err, gc.Equals, nil)
	c.Assert(ai.Macaroons, gc.HasLen, 2)
	c.Assert(ai.Used, jc.DeepEquals, []bool{true, true})
	c.Assert(ai.OpIndexes, jc.DeepEquals, map[bakery.Op]int{
		readOp("e1"): 0,
		readOp("e2"): 1,
	})
}

func (s *checkerSuite) TestAllowAlwaysAllowsNoOp(c *gc.C) {
	ts := newService(nil)
	client := newClient(nil)
	_, err := client.do(testContext, ts, bakery.Op{})
	c.Assert(err, gc.Equals, nil)
}

func (s *checkerSuite) TestAllowWithInvalidMacaroon(c *gc.C) {
	ts := newService(nil)
	client := newClient(nil)
	m1 := ts.newMacaroon(readOp("e1"), readOp("e2"))
	m1[0].AddFirstPartyCaveat([]byte("invalid"))
	m2 := ts.newMacaroon(readOp("e1"))
	client.addMacaroon(ts, "auth1", m1)
	client.addMacaroon(ts, "auth2", m2)
	// Check that we can't do both operations.
	ai, err := client.do(testContext, ts, readOp("e1"), readOp("e2"))
	c.Assert(err, gc.ErrorMatches, `caveat "invalid" not satisfied: caveat not recognized`)
	c.Assert(ai, gc.IsNil)

	ai, err = client.do(testContext, ts, readOp("e1"))
	c.Assert(err, gc.Equals, nil)
	c.Assert(ai.Used, jc.DeepEquals, []bool{false, true})
	c.Assert(ai.OpIndexes, jc.DeepEquals, map[bakery.Op]int{
		readOp("e1"): 1,
	})
}

func (s *checkerSuite) TestAllowed(c *gc.C) {
	ts := newService(nil)

	// Get two capabilities with overlapping operations.
	m1 := ts.newMacaroon(readOp("e1"), readOp("e2"))
	m2 := ts.newMacaroon(readOp("e2"), readOp("e3"))

	authInfo, err := ts.checker.Auth(m1, m2).Allowed(context.Background())
	c.Assert(err, gc.IsNil)
	c.Assert(authInfo.Macaroons, gc.HasLen, 2)
	c.Assert(authInfo.Used, jc.DeepEquals, []bool{true, true})
	c.Assert(authInfo.OpIndexes, jc.DeepEquals, map[bakery.Op]int{
		readOp("e1"): 0,
		readOp("e2"): 0,
		readOp("e3"): 1,
	})
}

func (s *checkerSuite) TestAllowWithOpsAuthorizer(c *gc.C) {
	store := newMacaroonStore(nil)
	ts := &service{
		checker: bakery.NewChecker(bakery.CheckerParams{
			Checker:          testChecker,
			OpsAuthorizer:    hierarchicalOpsAuthorizer{},
			MacaroonVerifier: store,
		}),
		store: store,
	}
	// Manufacture a macaroon granting access to /user/bob and
	// everything underneath it (by virtue of the hierarchicalOpsAuthorizer).
	m := ts.newMacaroon(bakery.Op{
		Entity: "path-/user/bob",
		Action: "*",
	})
	// Check that we can do some operation.
	_, err := ts.do(testContext, []macaroon.Slice{m}, writeOp("path-/user/bob/foo"))
	c.Assert(err, gc.Equals, nil)

	// Check that we can't do an operation on an entity outside the
	// original operation's purview.
	_, err = ts.do(testContext, []macaroon.Slice{m}, writeOp("path-/user/alice"))
	c.Assert(err, gc.ErrorMatches, `permission denied`)
}

func (s *checkerSuite) TestAllowWithOpsAuthorizerAndNoOp(c *gc.C) {
	store := newMacaroonStore(nil)
	ts := &service{
		checker: bakery.NewChecker(bakery.CheckerParams{
			Checker:          testChecker,
			OpsAuthorizer:    nopOpsAuthorizer{},
			MacaroonVerifier: store,
		}),
		store: store,
	}
	// Check that we can do a public operation with no operations authorized.
	_, err := ts.do(testContext, nil, readOp("public"))
	c.Assert(err, gc.Equals, nil)
}

func (s *checkerSuite) TestOpsAuthorizerError(c *gc.C) {
	store := newMacaroonStore(nil)
	ts := &service{
		checker: bakery.NewChecker(bakery.CheckerParams{
			Checker:          testChecker,
			OpsAuthorizer:    errorOpsAuthorizer{"some issue"},
			MacaroonVerifier: store,
		}),
		store: store,
	}
	_, err := ts.do(testContext, nil, readOp("public"))
	c.Assert(err, gc.ErrorMatches, "some issue")
}

func (s *checkerSuite) TestOpsAuthorizerWithCaveats(c *gc.C) {
	locator := make(dischargerLocator)
	store := newMacaroonStore(locator)
	var discharges []string
	locator["somewhere"] = &discharger{
		key:     mustGenerateKey(),
		locator: locator,
		checker: bakery.ThirdPartyCaveatCheckerFunc(func(_ context.Context, c *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
			discharges = append(discharges, string(c.Condition))
			return nil, nil
		}),
	}
	opAuth := map[bakery.Op]map[bakery.Op][]checkers.Caveat{
		readOp("everywhere1"): {
			readOp("somewhere1"): {{
				Location:  "somewhere",
				Condition: "somewhere1-1",
			}, {
				Location:  "somewhere",
				Condition: "somewhere1-2",
			}},
		},
		readOp("everywhere2"): {
			readOp("somewhere2"): {{
				Location:  "somewhere",
				Condition: "somewhere2-1",
			}, {
				Location:  "somewhere",
				Condition: "somewhere2-2",
			}},
		},
	}
	ts := &service{
		checker: bakery.NewChecker(bakery.CheckerParams{
			Checker:          testChecker,
			OpsAuthorizer:    caveatOpsAuthorizer{opAuth},
			MacaroonVerifier: store,
		}),
		store: store,
	}
	client := newClient(locator)
	client.addMacaroon(ts, "auth", ts.newMacaroon(readOp("everywhere1"), readOp("everywhere2")))
	_, err := client.do(testContext, ts, readOp("somewhere1"), readOp("somewhere2"))
	c.Assert(err, gc.Equals, nil)
	sort.Strings(discharges)
	c.Assert(discharges, jc.DeepEquals, []string{
		"somewhere1-1",
		"somewhere1-2",
		"somewhere2-1",
		"somewhere2-2",
	})
}

func (s *checkerSuite) TestMacaroonVerifierFatalError(c *gc.C) {
	// When we get a non-VerificationError error from the
	// opstore, we don't do any more verification.
	checker := bakery.NewChecker(bakery.CheckerParams{
		MacaroonVerifier: macaroonVerifierWithError{errgo.New("an error")},
	})
	m, err := macaroon.New(nil, nil, "", macaroon.V2)
	c.Assert(err, gc.IsNil)
	_, err = checker.Auth(macaroon.Slice{m}).Allow(testContext, basicOp)
	c.Assert(err, gc.ErrorMatches, `cannot retrieve macaroon: an error`)
}

// resolveCaveats resolves all the given caveats with the
// given namespace and includes the condition
// from each one. It will panic if it finds a third party caveat.
func resolveCaveats(ns *checkers.Namespace, caveats []checkers.Caveat) []string {
	conds := make([]string, len(caveats))
	for i, cav := range caveats {
		if cav.Location != "" {
			panic("found unexpected third party caveat")
		}
		conds[i] = ns.ResolveCaveat(cav).Condition
	}
	return conds
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

// service represents a service that requires authorization.
// Clients can make requests to the service to perform operations
// and may receive a macaroon to discharge if the authorization
// process requires it.
type service struct {
	checker *bakery.Checker
	store   *macaroonStore
}

func newService(locator bakery.ThirdPartyLocator) *service {
	store := newMacaroonStore(locator)
	return &service{
		checker: bakery.NewChecker(bakery.CheckerParams{
			Checker:          testChecker,
			MacaroonVerifier: store,
		}),
		store: store,
	}
}

// do makes a request to the service to perform the given operations
// using the given macaroons for authorization.
// It may return a dischargeRequiredError containing a macaroon
// that needs to be discharged.
func (svc *service) do(ctx context.Context, ms []macaroon.Slice, ops ...bakery.Op) (*bakery.AuthInfo, error) {
	authInfo, err := svc.checker.Auth(ms...).Allow(ctx, ops...)
	return authInfo, svc.maybeDischargeRequiredError(err)
}

// newMacaroon returns a macaroon with no caveats that allows the given operations.
func (svc *service) newMacaroon(ops ...bakery.Op) macaroon.Slice {
	m, err := svc.store.NewMacaroon(testContext, ops, nil, svc.checker.Namespace())
	if err != nil {
		panic(err)
	}
	return macaroon.Slice{m.M()}
}

// capability checks that the given macaroons have authorization for the
// given operations and, if so, returns a macaroon that has that authorization.
func (svc *service) capability(ctx context.Context, ms []macaroon.Slice, ops ...bakery.Op) (*bakery.Macaroon, error) {
	ai, err := svc.checker.Auth(ms...).Allow(ctx, ops...)
	if err != nil {
		return nil, svc.maybeDischargeRequiredError(err)
	}
	m, err := svc.store.NewMacaroon(ctx, ops, nil, svc.checker.Namespace())
	if err != nil {
		return nil, errgo.Mask(err)
	}
	for _, cond := range ai.Conditions() {
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
	m, err := svc.store.NewMacaroon(testContext, derr.Ops, derr.Caveats, svc.checker.Namespace())
	if err != nil {
		return errgo.Mask(err)
	}
	return &dischargeRequiredError{
		name: "authz",
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

type nopOpsAuthorizer struct{}

func (nopOpsAuthorizer) AuthorizeOps(ctx context.Context, authorizedOp bakery.Op, queryOps []bakery.Op) ([]bool, []checkers.Caveat, error) {
	if authorizedOp != bakery.NoOp {
		return nil, nil, nil
	}
	authed := make([]bool, len(queryOps))
	for i, op := range queryOps {
		if op.Entity == "public" {
			authed[i] = true
		}
	}
	return authed, nil, nil
}

type hierarchicalOpsAuthorizer struct{}

func (hierarchicalOpsAuthorizer) AuthorizeOps(ctx context.Context, authorizedOp bakery.Op, queryOps []bakery.Op) ([]bool, []checkers.Caveat, error) {
	ok := make([]bool, len(queryOps))
	for i, op := range queryOps {
		if isParentPathEntity(authorizedOp.Entity, op.Entity) && (authorizedOp.Action == op.Action || authorizedOp.Action == "*") {
			ok[i] = true
		}
	}
	return ok, nil, nil
}

type errorOpsAuthorizer struct {
	err string
}

func (a errorOpsAuthorizer) AuthorizeOps(ctx context.Context, authorizedOp bakery.Op, queryOps []bakery.Op) ([]bool, []checkers.Caveat, error) {
	return nil, nil, errgo.New(a.err)
}

type caveatOpsAuthorizer struct {
	// authOps holds a map from authorizing op to the
	// ops that it authorizes to the caveats associated with that.
	authOps map[bakery.Op]map[bakery.Op][]checkers.Caveat
}

func (a caveatOpsAuthorizer) AuthorizeOps(ctx context.Context, authorizedOp bakery.Op, queryOps []bakery.Op) ([]bool, []checkers.Caveat, error) {
	authed := make([]bool, len(queryOps))
	var caveats []checkers.Caveat
	for i, op := range queryOps {
		if authCaveats, ok := a.authOps[authorizedOp][op]; ok {
			caveats = append(caveats, authCaveats...)
			authed[i] = true
		}
	}
	return authed, caveats, nil
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
func (c *client) do(ctx context.Context, svc *service, ops ...bakery.Op) (*bakery.AuthInfo, error) {
	var authInfo *bakery.AuthInfo
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
