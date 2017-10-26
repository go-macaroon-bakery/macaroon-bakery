package bakery_test

import (
	"fmt"
	"unicode/utf8"

	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	"golang.org/x/net/context"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v2"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
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

	primary, err := oc.Oven.NewMacaroon(testContext, bakery.LatestVersion, nil, basicOp)
	c.Assert(err, gc.IsNil)
	c.Assert(primary.M().Location(), gc.Equals, "bakerytest")
	err = oc.Oven.AddCaveat(testContext, primary, strCaveat("something"))

	_, err = oc.Checker.Auth(macaroon.Slice{primary.M()}).Allow(strContext("something"), basicOp)
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
	tsMacaroon, err := ts.Oven.NewMacaroon(testContext, bakery.LatestVersion, nil, basicOp)
	c.Assert(err, gc.IsNil)

	// ts somehow sends the macaroon to fs which adds a third party caveat to be discharged by as.
	err = fs.Oven.AddCaveat(testContext, tsMacaroon, checkers.Caveat{Location: "as-loc", Condition: "user==bob"})
	c.Assert(err, gc.IsNil)

	// client asks for a discharge macaroon for each third party caveat
	d, err := bakery.DischargeAll(testContext, tsMacaroon, func(ctx context.Context, cav macaroon.Caveat, payload []byte) (*bakery.Macaroon, error) {
		c.Assert(cav.Location, gc.Equals, "as-loc")

		return discharge(ctx, as.Oven, thirdPartyStrcmpChecker("user==bob"), cav, payload)
	})
	c.Assert(err, gc.IsNil)

	_, err = ts.Checker.Auth(d).Allow(testContext, basicOp)
	c.Assert(err, gc.IsNil)
}

func (s *ServiceSuite) TestDischargeWithVersion1Macaroon(c *gc.C) {
	locator := bakery.NewThirdPartyStore()
	as := newBakery("as-loc", locator)
	ts := newBakery("ts-loc", locator)

	// ts creates a old-version macaroon.
	tsMacaroon, err := ts.Oven.NewMacaroon(testContext, bakery.Version1, nil, basicOp)
	c.Assert(err, gc.IsNil)
	err = ts.Oven.AddCaveat(testContext, tsMacaroon, checkers.Caveat{Location: "as-loc", Condition: "something"})
	c.Assert(err, gc.IsNil)

	// client asks for a discharge macaroon for each third party caveat
	d, err := bakery.DischargeAll(testContext, tsMacaroon, func(ctx context.Context, cav macaroon.Caveat, payload []byte) (*bakery.Macaroon, error) {
		// Make sure that the caveat id really is old-style.
		c.Assert(cav.Id, jc.Satisfies, utf8.Valid)
		return discharge(ctx, as.Oven, thirdPartyStrcmpChecker("something"), cav, payload)
	})
	c.Assert(err, gc.IsNil)

	_, err = ts.Checker.Auth(d).Allow(testContext, basicOp)
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
		RootKeyStore:     rootKeyStore,
		LegacyMacaroonOp: basicOp,
	})

	key, id, err := rootKeyStore.RootKey(testContext)
	c.Assert(err, gc.IsNil)

	_, err = rootKeyStore.Get(testContext, id)
	c.Assert(err, gc.IsNil)

	m, err := macaroon.New(key, []byte(fmt.Sprintf("%s-deadl00f", id)), "", macaroon.V1)
	c.Assert(err, gc.IsNil)

	_, err = b.Checker.Auth(macaroon.Slice{m}).Allow(testContext, basicOp)
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
	tsMacaroon, err := ts.Oven.NewMacaroon(testContext, bakery.LatestVersion, nil, basicOp)
	c.Assert(err, gc.IsNil)

	// ts somehow sends the macaroon to fs which adds a third party caveat to be discharged by as.
	err = fs.Oven.AddCaveat(testContext, tsMacaroon, checkers.Caveat{Location: "as-loc", Condition: "user==bob"})
	c.Assert(err, gc.IsNil)

	// client makes request to ts
	_, err = ts.Checker.Auth(macaroon.Slice{tsMacaroon.M()}).Allow(testContext, basicOp)
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
	tsMacaroon, err := ts.Oven.NewMacaroon(testContext, bakery.LatestVersion, nil, basicOp)
	c.Assert(err, gc.IsNil)

	// ts somehow sends the macaroon to fs which adds a third party caveat to be discharged by as.
	err = fs.Oven.AddCaveat(testContext, tsMacaroon, checkers.Caveat{Location: "as-loc", Condition: "user==bob"})
	c.Assert(err, gc.IsNil)

	// client asks for a discharge macaroon for each third party caveat
	d, err := bakery.DischargeAll(testContext, tsMacaroon, func(ctx context.Context, cav macaroon.Caveat, payload []byte) (*bakery.Macaroon, error) {
		c.Assert(cav.Location, gc.Equals, "as-loc")
		return discharge(ctx, as.Oven, thirdPartyStrcmpChecker("user==bob"), cav, payload)
	})
	c.Assert(err, gc.IsNil)

	// client has all the discharge macaroons. For each discharge macaroon bind it to our tsMacaroon
	// and add it to our request.
	for _, dm := range d[1:] {
		dm.Bind([]byte("tampered-signature")) // Bind against an incorrect signature.
	}

	// client makes request to ts.
	_, err = ts.Checker.Auth(d).Allow(testContext, basicOp)
	// TODO fix this error message.
	c.Assert(err, gc.ErrorMatches, "verification failed: signature mismatch after caveat verification")
}

func discharge(ctx context.Context, oven *bakery.Oven, checker bakery.ThirdPartyCaveatChecker, cav macaroon.Caveat, payload []byte) (*bakery.Macaroon, error) {
	return bakery.Discharge(ctx, bakery.DischargeParams{
		Key:     oven.Key(),
		Locator: oven.Locator(),
		Id:      cav.Id,
		Caveat:  payload,
		Checker: checker,
	})
}

func (s *ServiceSuite) TestNeedDeclared(c *gc.C) {
	locator := bakery.NewThirdPartyStore()
	firstParty := newBakery("first", locator)
	thirdParty := newBakery("third", locator)

	// firstParty mints a macaroon with a third-party caveat addressed
	// to thirdParty with a need-declared caveat.
	m, err := firstParty.Oven.NewMacaroon(testContext, bakery.LatestVersion, []checkers.Caveat{
		checkers.NeedDeclaredCaveat(checkers.Caveat{
			Location:  "third",
			Condition: "something",
		}, "foo", "bar"),
	}, basicOp)

	c.Assert(err, gc.IsNil)

	// The client asks for a discharge macaroon for each third party caveat.
	d, err := bakery.DischargeAll(testContext, m, func(ctx context.Context, cav macaroon.Caveat, payload []byte) (*bakery.Macaroon, error) {
		return discharge(ctx, thirdParty.Oven, thirdPartyStrcmpChecker("something"), cav, payload)
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
	ctx := checkers.ContextWithMacaroons(testContext, firstParty.Checker.Namespace(), d)
	_, err = firstParty.Checker.Auth(d).Allow(ctx, basicOp)
	c.Assert(err, gc.IsNil)

	// Try again when the third party does add a required declaration.

	// The client asks for a discharge macaroon for each third party caveat.
	d, err = bakery.DischargeAll(testContext, m, func(ctx context.Context, cav macaroon.Caveat, payload []byte) (*bakery.Macaroon, error) {
		checker := thirdPartyCheckerWithCaveats{
			checkers.DeclaredCaveat("foo", "a"),
			checkers.DeclaredCaveat("arble", "b"),
		}
		return discharge(ctx, thirdParty.Oven, checker, cav, payload)
	})
	c.Assert(err, gc.IsNil)

	// One attribute should have been added, the other was already there.
	declared = checkers.InferDeclared(firstParty.Checker.Namespace(), d)
	c.Assert(declared, gc.DeepEquals, map[string]string{
		"foo":   "a",
		"bar":   "",
		"arble": "b",
	})

	ctx = checkers.ContextWithMacaroons(testContext, firstParty.Checker.Namespace(), d)
	_, err = firstParty.Checker.Auth(d).Allow(ctx, basicOp)
	c.Assert(err, gc.IsNil)

	// Try again, but this time pretend a client is sneakily trying
	// to add another "declared" attribute to alter the declarations.
	d, err = bakery.DischargeAll(testContext, m, func(ctx context.Context, cav macaroon.Caveat, payload []byte) (*bakery.Macaroon, error) {
		checker := thirdPartyCheckerWithCaveats{
			checkers.DeclaredCaveat("foo", "a"),
			checkers.DeclaredCaveat("arble", "b"),
		}

		// Sneaky client adds a first party caveat.
		m, err := discharge(ctx, thirdParty.Oven, checker, cav, payload)
		c.Assert(err, gc.IsNil)

		err = m.AddCaveat(ctx, checkers.DeclaredCaveat("foo", "c"), nil, nil)
		c.Assert(err, gc.IsNil)
		return m, nil
	})

	c.Assert(err, gc.IsNil)

	declared = checkers.InferDeclared(firstParty.Checker.Namespace(), d)
	c.Assert(declared, gc.DeepEquals, map[string]string{
		"bar":   "",
		"arble": "b",
	})

	ctx = checkers.ContextWithMacaroons(testContext, firstParty.Checker.Namespace(), d)
	_, err = firstParty.Checker.Auth(d).Allow(testContext, basicOp)
	c.Assert(err, gc.ErrorMatches, `caveat "declared foo a" not satisfied: got foo=null, expected "a"`)
}

func (s *ServiceSuite) TestDischargeTwoNeedDeclared(c *gc.C) {
	locator := bakery.NewThirdPartyStore()
	firstParty := newBakery("first", locator)
	thirdParty := newBakery("third", locator)

	// firstParty mints a macaroon with two third party caveats
	// with overlapping attributes.
	m, err := firstParty.Oven.NewMacaroon(testContext, bakery.LatestVersion, []checkers.Caveat{
		checkers.NeedDeclaredCaveat(checkers.Caveat{
			Location:  "third",
			Condition: "x",
		}, "foo", "bar"),
		checkers.NeedDeclaredCaveat(checkers.Caveat{
			Location:  "third",
			Condition: "y",
		}, "bar", "baz"),
	}, basicOp)

	c.Assert(err, gc.IsNil)

	// The client asks for a discharge macaroon for each third party caveat.
	// Since no declarations are added by the discharger,
	d, err := bakery.DischargeAll(testContext, m, func(ctx context.Context, cav macaroon.Caveat, payload []byte) (*bakery.Macaroon, error) {
		return discharge(ctx, thirdParty.Oven, bakery.ThirdPartyCaveatCheckerFunc(func(context.Context, *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
			return nil, nil
		}), cav, payload)

	})
	c.Assert(err, gc.IsNil)
	declared := checkers.InferDeclared(firstParty.Checker.Namespace(), d)
	c.Assert(declared, gc.DeepEquals, map[string]string{
		"foo": "",
		"bar": "",
		"baz": "",
	})
	ctx := checkers.ContextWithMacaroons(testContext, firstParty.Checker.Namespace(), d)
	_, err = firstParty.Checker.Auth(d).Allow(ctx, basicOp)
	c.Assert(err, gc.IsNil)

	// If they return conflicting values, the discharge fails.
	// The client asks for a discharge macaroon for each third party caveat.
	// Since no declarations are added by the discharger,
	d, err = bakery.DischargeAll(testContext, m, func(ctx context.Context, cav macaroon.Caveat, payload []byte) (*bakery.Macaroon, error) {
		return discharge(ctx, thirdParty.Oven, bakery.ThirdPartyCaveatCheckerFunc(func(_ context.Context, cavInfo *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
			switch string(cavInfo.Condition) {
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
		}), cav, payload)

	})

	c.Assert(err, gc.IsNil)
	declared = checkers.InferDeclared(firstParty.Checker.Namespace(), d)
	c.Assert(declared, gc.DeepEquals, map[string]string{
		"bar": "",
		"baz": "bazval",
	})
	ctx = checkers.ContextWithMacaroons(testContext, firstParty.Checker.Namespace(), d)
	_, err = firstParty.Checker.Auth(d).Allow(testContext, basicOp)
	c.Assert(err, gc.ErrorMatches, `caveat "declared foo fooval1" not satisfied: got foo=null, expected "fooval1"`)
}

func (s *ServiceSuite) TestDischargeMacaroonCannotBeUsedAsNormalMacaroon(c *gc.C) {
	locator := bakery.NewThirdPartyStore()
	firstParty := newBakery("first", locator)
	thirdParty := newBakery("third", locator)

	// First party mints a macaroon with a 3rd party caveat.
	m, err := firstParty.Oven.NewMacaroon(testContext, bakery.LatestVersion, []checkers.Caveat{{
		Location:  "third",
		Condition: "true",
	}}, basicOp)
	c.Assert(err, gc.IsNil)

	// Acquire the discharge macaroon, but don't bind it to the original.
	var unbound *macaroon.Macaroon
	_, err = bakery.DischargeAll(testContext, m, func(ctx context.Context, cav macaroon.Caveat, payload []byte) (*bakery.Macaroon, error) {
		m, err := discharge(ctx, thirdParty.Oven, thirdPartyStrcmpChecker("true"), cav, payload)
		if err == nil {
			unbound = m.M().Clone()
		}
		return m, err
	})
	c.Assert(err, gc.IsNil)
	c.Assert(unbound, gc.NotNil)

	// Make sure it cannot be used as a normal macaroon in the third party.
	_, err = thirdParty.Checker.Auth(macaroon.Slice{unbound}).Allow(testContext, basicOp)
	c.Assert(err, gc.ErrorMatches, `cannot retrieve macaroon: cannot unmarshal macaroon id: .*`)
}

func (s *ServiceSuite) TestThirdPartyDischargeMacaroonIdsAreSmall(c *gc.C) {
	locator := bakery.NewThirdPartyStore()
	bakeries := map[string]*bakery.Bakery{
		"ts-loc":  newBakery("ts-loc", locator),
		"as1-loc": newBakery("as1-loc", locator),
		"as2-loc": newBakery("as2-loc", locator),
	}
	ts := bakeries["ts-loc"]

	tsMacaroon, err := ts.Oven.NewMacaroon(testContext, bakery.LatestVersion, nil, basicOp)
	c.Assert(err, gc.IsNil)
	err = ts.Oven.AddCaveat(testContext, tsMacaroon, checkers.Caveat{Location: "as1-loc", Condition: "something"})
	c.Assert(err, gc.IsNil)

	checker := func(loc string) bakery.ThirdPartyCaveatCheckerFunc {
		return func(_ context.Context, cavInfo *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
			switch loc {
			case "as1-loc":
				return []checkers.Caveat{{
					Condition: "something",
					Location:  "as2-loc",
				}}, nil
			case "as2-loc":
				return nil, nil
			default:
				return nil, errgo.Newf("unknown location %q", loc)
			}
		}
	}

	d, err := bakery.DischargeAll(testContext, tsMacaroon, func(ctx context.Context, cav macaroon.Caveat, payload []byte) (*bakery.Macaroon, error) {
		return discharge(ctx, bakeries[cav.Location].Oven, checker(cav.Location), cav, payload)
	})
	c.Assert(err, gc.IsNil)

	_, err = ts.Checker.Auth(d).Allow(testContext, basicOp)
	c.Assert(err, gc.IsNil)

	for i, m := range d {
		for j, cav := range m.Caveats() {
			if cav.VerificationId != nil && len(cav.Id) > 3 {
				c.Errorf("caveat id on caveat %d of macaroon %d is too big (%q)", j, i, cav.Id)
			}
		}
	}
}
