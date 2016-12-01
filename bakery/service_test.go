package bakery_test

import (
	"fmt"
	"unicode/utf8"

	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	"golang.org/x/net/context"
	gc "gopkg.in/check.v1"
	"gopkg.in/macaroon.v2-unstable"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
)

type ServiceSuite struct {
	testing.IsolationSuite
}

var _ = gc.Suite(&ServiceSuite{})

// TestSingleServiceFirstParty creates a single service
// with a macaroon with one first party caveat.
// It creates a request with this macaroon and checks that the service
// can verify this macaroon as valid.
func (s *ServiceSuite) TestSingleServiceFirstParty(c *gc.C) {
	oc := newBakery("bakerytest", nil)

	primary, err := oc.Oven.NewMacaroon(testContext, macaroon.LatestVersion, ages, nil, bakery.LoginOp)
	c.Assert(err, gc.IsNil)
	c.Assert(primary.Location(), gc.Equals, "bakerytest")
	err = oc.Oven.AddCaveat(testContext, primary, strCaveat("something"))

	_, err = oc.Checker.Auth(macaroon.Slice{primary}).Allow(strContext("something"), bakery.LoginOp)
	c.Assert(err, gc.IsNil)
}

// TestMacaroonPaperFig6 implements an example flow as described in the macaroons paper:
// http://theory.stanford.edu/~ataly/Papers/macaroons.pdf
// There are three services, ts, fs, as:
// ts is a store service which has deligated authority to a forum service fs.
// The forum service wants to require its users to be logged into to an authentication service as.
//
// The client obtains a macaroon from fs (minted by ts, with a third party caveat addressed to as).
// The client obtains a discharge macaroon from as to satisfy this caveat.
// The target service verifies the original macaroon it delegated to fs
// No direct contact between as and ts is required
func (s *ServiceSuite) TestMacaroonPaperFig6(c *gc.C) {
	locator := bakery.NewThirdPartyStore()
	as := newBakery("as-loc", locator)
	ts := newBakery("ts-loc", locator)
	fs := newBakery("fs-loc", locator)

	// ts creates a macaroon.
	tsMacaroon, err := ts.Oven.NewMacaroon(testContext, macaroon.LatestVersion, ages, nil, bakery.LoginOp)
	c.Assert(err, gc.IsNil)

	// ts somehow sends the macaroon to fs which adds a third party caveat to be discharged by as.
	err = fs.Oven.AddCaveat(testContext, tsMacaroon, checkers.Caveat{Location: "as-loc", Condition: "user==bob"})
	c.Assert(err, gc.IsNil)

	// client asks for a discharge macaroon for each third party caveat
	d, err := bakery.DischargeAll(testContext, tsMacaroon, func(ctx context.Context, cav macaroon.Caveat) (*macaroon.Macaroon, error) {
		c.Assert(cav.Location, gc.Equals, "as-loc")

		return discharge(ctx, as.Oven, thirdPartyStrcmpChecker("user==bob"), ts.Checker.Namespace(), cav.Id)
	})
	c.Assert(err, gc.IsNil)

	_, err = ts.Checker.Auth(d).Allow(testContext, bakery.LoginOp)
	c.Assert(err, gc.IsNil)
}

func (s *ServiceSuite) TestDischargeWithVersion1Macaroon(c *gc.C) {
	locator := bakery.NewThirdPartyStore()
	as := newBakery("as-loc", locator)
	ts := newBakery("ts-loc", locator)

	// ts creates a old-version macaroon.
	tsMacaroon, err := ts.Oven.NewMacaroon(testContext, macaroon.V1, ages, nil, bakery.LoginOp)
	c.Assert(err, gc.IsNil)
	err = ts.Oven.AddCaveat(testContext, tsMacaroon, checkers.Caveat{Location: "as-loc", Condition: "something"})
	c.Assert(err, gc.IsNil)

	// client asks for a discharge macaroon for each third party caveat
	d, err := bakery.DischargeAll(testContext, tsMacaroon, func(ctx context.Context, cav macaroon.Caveat) (*macaroon.Macaroon, error) {
		// Make sure that the caveat id really is old-style.
		c.Assert(cav.Id, jc.Satisfies, utf8.Valid)
		return discharge(ctx, as.Oven, thirdPartyStrcmpChecker("something"), ts.Checker.Namespace(), cav.Id)
	})
	c.Assert(err, gc.IsNil)

	_, err = ts.Checker.Auth(d).Allow(testContext, bakery.LoginOp)
	c.Assert(err, gc.IsNil)

	for _, m := range d {
		c.Assert(m.Version(), gc.Equals, macaroon.V1)
	}
}

func (s *ServiceSuite) TestVersion1MacaroonId(c *gc.C) {
	// In the version 1 bakery, macaroon ids were hex-encoded with a hyphenated
	// UUID suffix.
	rootKeyStore := bakery.NewMemRootKeyStore()
	b := bakery.New(bakery.BakeryParams{
		RootKeyStore:   rootKeyStore,
		IdentityClient: oneIdentity{},
	})

	key, id, err := rootKeyStore.RootKey(testContext)
	c.Assert(err, gc.IsNil)

	_, err = rootKeyStore.Get(testContext, id)
	c.Assert(err, gc.IsNil)

	m, err := macaroon.New(key, []byte(fmt.Sprintf("%s-deadl00f", id)), "", macaroon.V1)
	c.Assert(err, gc.IsNil)

	_, err = b.Checker.Auth(macaroon.Slice{m}).Allow(testContext, bakery.LoginOp)
	c.Assert(err, gc.IsNil)
}

// TestMacaroonPaperFig6FailsWithoutDischarges runs a similar test as TestMacaroonPaperFig6
// without the client discharging the third party caveats.
func (s *ServiceSuite) TestMacaroonPaperFig6FailsWithoutDischarges(c *gc.C) {
	locator := bakery.NewThirdPartyStore()
	ts := newBakery("ts-loc", locator)
	fs := newBakery("fs-loc", locator)
	newBakery("as-loc", locator)

	// ts creates a macaroon.
	tsMacaroon, err := ts.Oven.NewMacaroon(testContext, macaroon.LatestVersion, ages, nil, bakery.LoginOp)
	c.Assert(err, gc.IsNil)

	// ts somehow sends the macaroon to fs which adds a third party caveat to be discharged by as.
	err = fs.Oven.AddCaveat(testContext, tsMacaroon, checkers.Caveat{Location: "as-loc", Condition: "user==bob"})
	c.Assert(err, gc.IsNil)

	// client makes request to ts
	_, err = ts.Checker.Auth(macaroon.Slice{tsMacaroon}).Allow(testContext, bakery.LoginOp)
	c.Assert(err, gc.ErrorMatches, `verification failed: cannot find discharge macaroon for caveat .*`, gc.Commentf("%#v", err))
}

// TestMacaroonPaperFig6FailsWithBindingOnTamperedSignature runs a similar test as TestMacaroonPaperFig6
// with the discharge macaroon binding being done on a tampered signature.
func (s *ServiceSuite) TestMacaroonPaperFig6FailsWithBindingOnTamperedSignature(c *gc.C) {
	locator := bakery.NewThirdPartyStore()
	as := newBakery("as-loc", locator)
	ts := newBakery("ts-loc", locator)
	fs := newBakery("fs-loc", locator)

	// ts creates a macaroon.
	tsMacaroon, err := ts.Oven.NewMacaroon(testContext, macaroon.LatestVersion, ages, nil, bakery.LoginOp)
	c.Assert(err, gc.IsNil)

	// ts somehow sends the macaroon to fs which adds a third party caveat to be discharged by as.
	err = fs.Oven.AddCaveat(testContext, tsMacaroon, checkers.Caveat{Location: "as-loc", Condition: "user==bob"})
	c.Assert(err, gc.IsNil)

	// client asks for a discharge macaroon for each third party caveat
	d, err := bakery.DischargeAll(testContext, tsMacaroon, func(ctx context.Context, cav macaroon.Caveat) (*macaroon.Macaroon, error) {
		c.Assert(cav.Location, gc.Equals, "as-loc")
		return discharge(ctx, as.Oven, thirdPartyStrcmpChecker("user==bob"), ts.Checker.Namespace(), cav.Id)
	})
	c.Assert(err, gc.IsNil)

	// client has all the discharge macaroons. For each discharge macaroon bind it to our tsMacaroon
	// and add it to our request.
	for _, dm := range d[1:] {
		dm.Bind([]byte("tampered-signature")) // Bind against an incorrect signature.
	}

	// client makes request to ts.
	_, err = ts.Checker.Auth(d).Allow(testContext, bakery.LoginOp)
	// TODO fix this error message.
	c.Assert(err, gc.ErrorMatches, "verification failed: signature mismatch after caveat verification")
}

func discharge(ctx context.Context, oven *bakery.Oven, checker bakery.ThirdPartyCaveatChecker, ns *checkers.Namespace, id []byte) (*macaroon.Macaroon, error) {
	m, caveats, err := bakery.Discharge(ctx, oven.Key(), checker, id)
	if err != nil {
		return nil, err
	}
	for _, cav := range caveats {
		err := bakery.AddCaveat(testContext, oven.Key(), oven.Locator(), m, cav, ns)
		if err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (s *ServiceSuite) TestNeedDeclared(c *gc.C) {
	locator := bakery.NewThirdPartyStore()
	firstParty := newBakery("first", locator)
	thirdParty := newBakery("third", locator)

	// firstParty mints a macaroon with a third-party caveat addressed
	// to thirdParty with a need-declared caveat.
	m, err := firstParty.Oven.NewMacaroon(testContext, macaroon.LatestVersion, ages, []checkers.Caveat{
		checkers.NeedDeclaredCaveat(checkers.Caveat{
			Location:  "third",
			Condition: "something",
		}, "foo", "bar"),
	}, bakery.LoginOp)

	c.Assert(err, gc.IsNil)

	// The client asks for a discharge macaroon for each third party caveat.
	d, err := bakery.DischargeAll(testContext, m, func(ctx context.Context, cav macaroon.Caveat) (*macaroon.Macaroon, error) {
		return discharge(ctx, thirdParty.Oven, thirdPartyStrcmpChecker("something"), firstParty.Checker.Namespace(), cav.Id)
	})
	c.Assert(err, gc.IsNil)

	// The required declared attributes should have been added
	// to the discharge macaroons.
	declared := checkers.InferDeclared(firstParty.Checker.Namespace(), d)
	c.Assert(declared, gc.DeepEquals, map[string]string{
		"foo": "",
		"bar": "",
	})

	// Make sure the macaroons actually check out correctly
	// when provided with the declared checker.
	ctxt := checkers.ContextWithDeclared(testContext, declared)
	_, err = firstParty.Checker.Auth(d).Allow(ctxt, bakery.LoginOp)
	c.Assert(err, gc.IsNil)

	// Try again when the third party does add a required declaration.

	// The client asks for a discharge macaroon for each third party caveat.
	d, err = bakery.DischargeAll(testContext, m, func(ctx context.Context, cav macaroon.Caveat) (*macaroon.Macaroon, error) {
		checker := thirdPartyCheckerWithCaveats{
			checkers.DeclaredCaveat("foo", "a"),
			checkers.DeclaredCaveat("arble", "b"),
		}
		return discharge(ctx, thirdParty.Oven, checker, firstParty.Checker.Namespace(), cav.Id)
	})
	c.Assert(err, gc.IsNil)

	// One attribute should have been added, the other was already there.
	declared = checkers.InferDeclared(firstParty.Checker.Namespace(), d)
	c.Assert(declared, gc.DeepEquals, map[string]string{
		"foo":   "a",
		"bar":   "",
		"arble": "b",
	})

	ctxt = checkers.ContextWithDeclared(testContext, declared)
	_, err = firstParty.Checker.Auth(d).Allow(testContext, bakery.LoginOp)
	c.Assert(err, gc.IsNil)

	// Try again, but this time pretend a client is sneakily trying
	// to add another "declared" attribute to alter the declarations.
	d, err = bakery.DischargeAll(testContext, m, func(ctx context.Context, cav macaroon.Caveat) (*macaroon.Macaroon, error) {
		checker := thirdPartyCheckerWithCaveats{
			checkers.DeclaredCaveat("foo", "a"),
			checkers.DeclaredCaveat("arble", "b"),
		}

		// Sneaky client adds a first party caveat.
		m, err := discharge(ctx, thirdParty.Oven, checker, firstParty.Checker.Namespace(), cav.Id)
		c.Assert(err, gc.IsNil)

		err = m.AddFirstPartyCaveat(checkers.DeclaredCaveat("foo", "c").Condition)
		c.Assert(err, gc.IsNil)
		return m, nil
	})

	c.Assert(err, gc.IsNil)

	declared = checkers.InferDeclared(firstParty.Checker.Namespace(), d)
	c.Assert(declared, gc.DeepEquals, map[string]string{
		"bar":   "",
		"arble": "b",
	})

	ctxt = checkers.ContextWithDeclared(testContext, declared)
	_, err = firstParty.Checker.Auth(d).Allow(testContext, bakery.LoginOp)
	c.Assert(err, gc.ErrorMatches, `cannot authorize login macaroon: caveat "declared foo a" not satisfied: got foo=null, expected "a"`)
}

func (s *ServiceSuite) TestDischargeTwoNeedDeclared(c *gc.C) {
	locator := bakery.NewThirdPartyStore()
	firstParty := newBakery("first", locator)
	thirdParty := newBakery("third", locator)

	// firstParty mints a macaroon with two third party caveats
	// with overlapping attributes.
	m, err := firstParty.Oven.NewMacaroon(testContext, macaroon.LatestVersion, ages, []checkers.Caveat{
		checkers.NeedDeclaredCaveat(checkers.Caveat{
			Location:  "third",
			Condition: "x",
		}, "foo", "bar"),
		checkers.NeedDeclaredCaveat(checkers.Caveat{
			Location:  "third",
			Condition: "y",
		}, "bar", "baz"),
	}, bakery.LoginOp)

	c.Assert(err, gc.IsNil)

	// The client asks for a discharge macaroon for each third party caveat.
	// Since no declarations are added by the discharger,
	d, err := bakery.DischargeAll(testContext, m, func(ctx context.Context, cav macaroon.Caveat) (*macaroon.Macaroon, error) {
		return discharge(ctx, thirdParty.Oven, bakery.ThirdPartyCaveatCheckerFunc(func(context.Context, *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
			return nil, nil
		}), firstParty.Checker.Namespace(), cav.Id)

	})
	c.Assert(err, gc.IsNil)
	declared := checkers.InferDeclared(firstParty.Checker.Namespace(), d)
	c.Assert(declared, gc.DeepEquals, map[string]string{
		"foo": "",
		"bar": "",
		"baz": "",
	})
	ctxt := checkers.ContextWithDeclared(testContext, declared)
	_, err = firstParty.Checker.Auth(d).Allow(ctxt, bakery.LoginOp)
	c.Assert(err, gc.IsNil)

	// If they return conflicting values, the discharge fails.
	// The client asks for a discharge macaroon for each third party caveat.
	// Since no declarations are added by the discharger,
	d, err = bakery.DischargeAll(testContext, m, func(ctx context.Context, cav macaroon.Caveat) (*macaroon.Macaroon, error) {
		return discharge(ctx, thirdParty.Oven, bakery.ThirdPartyCaveatCheckerFunc(func(_ context.Context, cavInfo *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
			switch cavInfo.Condition {
			case "x":
				return []checkers.Caveat{
					checkers.DeclaredCaveat("foo", "fooval1"),
				}, nil
			case "y":
				return []checkers.Caveat{
					checkers.DeclaredCaveat("foo", "fooval2"),
					checkers.DeclaredCaveat("baz", "bazval"),
				}, nil
			}
			return nil, fmt.Errorf("not matched")
		}), firstParty.Checker.Namespace(), cav.Id)

	})

	c.Assert(err, gc.IsNil)
	declared = checkers.InferDeclared(firstParty.Checker.Namespace(), d)
	c.Assert(declared, gc.DeepEquals, map[string]string{
		"bar": "",
		"baz": "bazval",
	})
	ctxt = checkers.ContextWithDeclared(testContext, declared)
	_, err = firstParty.Checker.Auth(d).Allow(testContext, bakery.LoginOp)
	c.Assert(err, gc.ErrorMatches, `cannot authorize login macaroon: caveat "declared foo fooval1" not satisfied: got foo=null, expected "fooval1"`)
}

func (s *ServiceSuite) TestDischargeMacaroonCannotBeUsedAsNormalMacaroon(c *gc.C) {
	locator := bakery.NewThirdPartyStore()
	firstParty := newBakery("first", locator)
	thirdParty := newBakery("third", locator)

	// First party mints a macaroon with a 3rd party caveat.
	m, err := firstParty.Oven.NewMacaroon(testContext, macaroon.LatestVersion, ages, []checkers.Caveat{{
		Location:  "third",
		Condition: "true",
	}}, bakery.LoginOp)
	c.Assert(err, gc.IsNil)

	var id []byte
	for _, cav := range m.Caveats() {
		if cav.Location != "" {
			id = cav.Id
		}
	}

	// Acquire the discharge macaroon, but don't bind it to the original.
	d, err := discharge(testContext, thirdParty.Oven, bakery.ThirdPartyCaveatCheckerFunc(func(context.Context, *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
		return nil, nil
	}), firstParty.Checker.Namespace(), id)

	c.Assert(err, gc.IsNil, gc.Commentf("id %q", m.Caveats()[0].Id))

	// Make sure it cannot be used as a normal macaroon in the third party.
	_, err = thirdParty.Checker.Auth(macaroon.Slice{d}).Allow(testContext, bakery.LoginOp)
	c.Assert(err, gc.ErrorMatches, `verification failed: macaroon not found in storage`)
}
