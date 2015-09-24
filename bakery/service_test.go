package bakery_test

import (
	"encoding/json"
	"fmt"

	gc "gopkg.in/check.v1"
	"gopkg.in/macaroon.v1"

	jc "github.com/juju/testing/checkers"
	"gopkg.in/macaroon-bakery.v1/bakery"
	"gopkg.in/macaroon-bakery.v1/bakery/checkers"
)

type ServiceSuite struct{}

var _ = gc.Suite(&ServiceSuite{})

// TestSingleServiceFirstParty creates a single service
// with a macaroon with one first party caveat.
// It creates a request with this macaroon and checks that the service
// can verify this macaroon as valid.
func (s *ServiceSuite) TestSingleServiceFirstParty(c *gc.C) {
	p := bakery.NewServiceParams{
		Location: "loc",
		Store:    nil,
		Key:      nil,
		Locator:  nil,
	}
	service, err := bakery.NewService(p)
	c.Assert(err, gc.IsNil)

	primary, err := service.NewMacaroon("", nil, nil)
	c.Assert(err, gc.IsNil)
	c.Assert(primary.Location(), gc.Equals, "loc")
	cav := checkers.Caveat{
		Location:  "",
		Condition: "something",
	}
	err = service.AddCaveat(primary, cav)
	c.Assert(err, gc.IsNil)

	err = service.Check(macaroon.Slice{primary}, strcmpChecker("something"))
	c.Assert(err, gc.IsNil)
}

// TestMacaroonPaperFig6 implements an example flow as described in the macaroons paper:
// http://theory.stanford.edu/~ataly/Papers/macaroons.pdf
// There are three services, ts, fs, as:
// ts is a storage service which has deligated authority to a forum service fs.
// The forum service wants to require its users to be logged into to an authentication service as.
//
// The client obtains a macaroon from fs (minted by ts, with a third party caveat addressed to as).
// The client obtains a discharge macaroon from as to satisfy this caveat.
// The target service verifies the original macaroon it delegated to fs
// No direct contact between as and ts is required
func (s *ServiceSuite) TestMacaroonPaperFig6(c *gc.C) {
	locator := make(bakery.PublicKeyLocatorMap)
	as := newService(c, "as-loc", locator)
	ts := newService(c, "ts-loc", locator)
	fs := newService(c, "fs-loc", locator)

	// ts creates a macaroon.
	tsMacaroon, err := ts.NewMacaroon("", nil, nil)
	c.Assert(err, gc.IsNil)

	// ts somehow sends the macaroon to fs which adds a third party caveat to be discharged by as.
	err = fs.AddCaveat(tsMacaroon, checkers.Caveat{Location: "as-loc", Condition: "user==bob"})
	c.Assert(err, gc.IsNil)

	// client asks for a discharge macaroon for each third party caveat
	d, err := bakery.DischargeAll(tsMacaroon, func(firstPartyLocation string, cav macaroon.Caveat) (*macaroon.Macaroon, error) {
		c.Assert(firstPartyLocation, gc.Equals, "ts-loc")
		c.Assert(cav.Location, gc.Equals, "as-loc")
		mac, err := as.Discharge(strcmpChecker("user==bob"), cav.Id)
		c.Assert(err, gc.IsNil)
		return mac, nil
	})
	c.Assert(err, gc.IsNil)

	err = ts.Check(d, strcmpChecker(""))
	c.Assert(err, gc.IsNil)
}

func macStr(m *macaroon.Macaroon) string {
	data, err := json.MarshalIndent(m, "\t", "\t")
	if err != nil {
		panic(err)
	}
	return string(data)
}

// TestMacaroonPaperFig6FailsWithoutDischarges runs a similar test as TestMacaroonPaperFig6
// without the client discharging the third party caveats.
func (s *ServiceSuite) TestMacaroonPaperFig6FailsWithoutDischarges(c *gc.C) {
	locator := make(bakery.PublicKeyLocatorMap)
	ts := newService(c, "ts-loc", locator)
	fs := newService(c, "fs-loc", locator)
	_ = newService(c, "as-loc", locator)

	// ts creates a macaroon.
	tsMacaroon, err := ts.NewMacaroon("", nil, nil)
	c.Assert(err, gc.IsNil)

	// ts somehow sends the macaroon to fs which adds a third party caveat to be discharged by as.
	err = fs.AddCaveat(tsMacaroon, checkers.Caveat{Location: "as-loc", Condition: "user==bob"})
	c.Assert(err, gc.IsNil)

	// client makes request to ts
	err = ts.Check(macaroon.Slice{tsMacaroon}, strcmpChecker(""))
	c.Assert(err, gc.ErrorMatches, `verification failed: cannot find discharge macaroon for caveat ".*"`)
}

// TestMacaroonPaperFig6FailsWithBindingOnTamperedSignature runs a similar test as TestMacaroonPaperFig6
// with the discharge macaroon binding being done on a tampered signature.
func (s *ServiceSuite) TestMacaroonPaperFig6FailsWithBindingOnTamperedSignature(c *gc.C) {
	locator := make(bakery.PublicKeyLocatorMap)
	as := newService(c, "as-loc", locator)
	ts := newService(c, "ts-loc", locator)
	fs := newService(c, "fs-loc", locator)

	// ts creates a macaroon.
	tsMacaroon, err := ts.NewMacaroon("", nil, nil)
	c.Assert(err, gc.IsNil)

	// ts somehow sends the macaroon to fs which adds a third party caveat to be discharged by as.
	err = fs.AddCaveat(tsMacaroon, checkers.Caveat{Location: "as-loc", Condition: "user==bob"})
	c.Assert(err, gc.IsNil)

	// client asks for a discharge macaroon for each third party caveat
	d, err := bakery.DischargeAll(tsMacaroon, func(firstPartyLocation string, cav macaroon.Caveat) (*macaroon.Macaroon, error) {
		c.Assert(firstPartyLocation, gc.Equals, "ts-loc")
		c.Assert(cav.Location, gc.Equals, "as-loc")
		mac, err := as.Discharge(strcmpChecker("user==bob"), cav.Id)
		c.Assert(err, gc.IsNil)
		return mac, nil
	})
	c.Assert(err, gc.IsNil)

	// client has all the discharge macaroons. For each discharge macaroon bind it to our tsMacaroon
	// and add it to our request.
	for _, dm := range d[1:] {
		dm.Bind([]byte("tampered-signature")) // Bind against an incorrect signature.
	}

	// client makes request to ts.
	err = ts.Check(d, strcmpChecker(""))
	c.Assert(err, gc.ErrorMatches, "verification failed: signature mismatch after caveat verification")
}

func (s *ServiceSuite) TestNeedDeclared(c *gc.C) {
	locator := make(bakery.PublicKeyLocatorMap)
	firstParty := newService(c, "first", locator)
	thirdParty := newService(c, "third", locator)

	// firstParty mints a macaroon with a third-party caveat addressed
	// to thirdParty with a need-declared caveat.
	m, err := firstParty.NewMacaroon("", nil, []checkers.Caveat{
		checkers.NeedDeclaredCaveat(checkers.Caveat{
			Location:  "third",
			Condition: "something",
		}, "foo", "bar"),
	})
	c.Assert(err, gc.IsNil)

	// The client asks for a discharge macaroon for each third party caveat.
	d, err := bakery.DischargeAll(m, func(_ string, cav macaroon.Caveat) (*macaroon.Macaroon, error) {
		return thirdParty.Discharge(strcmpChecker("something"), cav.Id)
	})
	c.Assert(err, gc.IsNil)

	// The required declared attributes should have been added
	// to the discharge macaroons.
	declared := checkers.InferDeclared(d)
	c.Assert(declared, gc.DeepEquals, checkers.Declared{
		"foo": "",
		"bar": "",
	})

	// Make sure the macaroons actually check out correctly
	// when provided with the declared checker.
	err = firstParty.Check(d, checkers.New(declared))
	c.Assert(err, gc.IsNil)

	// Try again when the third party does add a required declaration.

	// The client asks for a discharge macaroon for each third party caveat.
	d, err = bakery.DischargeAll(m, func(_ string, cav macaroon.Caveat) (*macaroon.Macaroon, error) {
		checker := thirdPartyCheckerWithCaveats{
			checkers.DeclaredCaveat("foo", "a"),
			checkers.DeclaredCaveat("arble", "b"),
		}
		return thirdParty.Discharge(checker, cav.Id)
	})
	c.Assert(err, gc.IsNil)

	// One attribute should have been added, the other was already there.
	declared = checkers.InferDeclared(d)
	c.Assert(declared, gc.DeepEquals, checkers.Declared{
		"foo":   "a",
		"bar":   "",
		"arble": "b",
	})

	err = firstParty.Check(d, checkers.New(declared))
	c.Assert(err, gc.IsNil)

	// Try again, but this time pretend a client is sneakily trying
	// to add another "declared" attribute to alter the declarations.
	d, err = bakery.DischargeAll(m, func(_ string, cav macaroon.Caveat) (*macaroon.Macaroon, error) {
		checker := thirdPartyCheckerWithCaveats{
			checkers.DeclaredCaveat("foo", "a"),
			checkers.DeclaredCaveat("arble", "b"),
		}
		m, err := thirdParty.Discharge(checker, cav.Id)
		c.Assert(err, gc.IsNil)

		// Sneaky client adds a first party caveat.
		m.AddFirstPartyCaveat(checkers.DeclaredCaveat("foo", "c").Condition)
		return m, nil
	})
	c.Assert(err, gc.IsNil)

	declared = checkers.InferDeclared(d)
	c.Assert(declared, gc.DeepEquals, checkers.Declared{
		"bar":   "",
		"arble": "b",
	})

	err = firstParty.Check(d, checkers.New(declared))
	c.Assert(err, gc.ErrorMatches, `verification failed: caveat "declared foo a" not satisfied: got foo=null, expected "a"`)
}

func (s *ServiceSuite) TestDischargeTwoNeedDeclared(c *gc.C) {
	locator := make(bakery.PublicKeyLocatorMap)
	firstParty := newService(c, "first", locator)
	thirdParty := newService(c, "third", locator)

	// firstParty mints a macaroon with two third party caveats
	// with overlapping attributes.
	m, err := firstParty.NewMacaroon("", nil, []checkers.Caveat{
		checkers.NeedDeclaredCaveat(checkers.Caveat{
			Location:  "third",
			Condition: "x",
		}, "foo", "bar"),
		checkers.NeedDeclaredCaveat(checkers.Caveat{
			Location:  "third",
			Condition: "y",
		}, "bar", "baz"),
	})
	c.Assert(err, gc.IsNil)

	// The client asks for a discharge macaroon for each third party caveat.
	// Since no declarations are added by the discharger,
	d, err := bakery.DischargeAll(m, func(_ string, cav macaroon.Caveat) (*macaroon.Macaroon, error) {
		return thirdParty.Discharge(bakery.ThirdPartyCheckerFunc(func(_, caveat string) ([]checkers.Caveat, error) {
			return nil, nil
		}), cav.Id)
	})
	c.Assert(err, gc.IsNil)
	declared := checkers.InferDeclared(d)
	c.Assert(declared, gc.DeepEquals, checkers.Declared{
		"foo": "",
		"bar": "",
		"baz": "",
	})
	err = firstParty.Check(d, checkers.New(declared))
	c.Assert(err, gc.IsNil)

	// If they return conflicting values, the discharge fails.
	// The client asks for a discharge macaroon for each third party caveat.
	// Since no declarations are added by the discharger,
	d, err = bakery.DischargeAll(m, func(_ string, cav macaroon.Caveat) (*macaroon.Macaroon, error) {
		return thirdParty.Discharge(bakery.ThirdPartyCheckerFunc(func(_, caveat string) ([]checkers.Caveat, error) {
			switch caveat {
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
		}), cav.Id)
	})
	c.Assert(err, gc.IsNil)
	declared = checkers.InferDeclared(d)
	c.Assert(declared, gc.DeepEquals, checkers.Declared{
		"bar": "",
		"baz": "bazval",
	})
	err = firstParty.Check(d, checkers.New(declared))
	c.Assert(err, gc.ErrorMatches, `verification failed: caveat "declared foo fooval1" not satisfied: got foo=null, expected "fooval1"`)
}

func (s *ServiceSuite) TestDischargeMacaroonCannotBeUsedAsNormalMacaroon(c *gc.C) {
	locator := make(bakery.PublicKeyLocatorMap)
	firstParty := newService(c, "first", locator)
	thirdParty := newService(c, "third", locator)

	// First party mints a macaroon with a 3rd party caveat.
	m, err := firstParty.NewMacaroon("", nil, []checkers.Caveat{{
		Location:  "third",
		Condition: "true",
	}})
	c.Assert(err, gc.IsNil)

	// Acquire the discharge macaroon, but don't bind it to the original.
	d, err := thirdParty.Discharge(bakery.ThirdPartyCheckerFunc(func(_, caveat string) ([]checkers.Caveat, error) {
		return nil, nil
	}), m.Caveats()[0].Id)
	c.Assert(err, gc.IsNil)

	// Make sure it cannot be used as a normal macaroon in the third party.
	err = thirdParty.Check(macaroon.Slice{d}, checkers.New())
	c.Assert(err, gc.ErrorMatches, `verification failed: macaroon not found in storage`)
}

func (*ServiceSuite) TestCheckAnyWithNoMacaroons(c *gc.C) {
	svc := newService(c, "somewhere", nil)
	newMacaroons := func(caveats ...checkers.Caveat) macaroon.Slice {
		m, err := svc.NewMacaroon("", nil, caveats)
		c.Assert(err, gc.IsNil)
		return macaroon.Slice{m}
	}
	tests := []struct {
		about          string
		macaroons      []macaroon.Slice
		assert         map[string]string
		checker        checkers.Checker
		expectDeclared map[string]string
		expectError    string
	}{{
		about:       "no macaroons",
		expectError: "verification failed: no macaroons",
	}, {
		about: "one macaroon, no caveats",
		macaroons: []macaroon.Slice{
			newMacaroons(),
		},
	}, {
		about: "one macaroon, one unrecognized caveat",
		macaroons: []macaroon.Slice{
			newMacaroons(checkers.Caveat{
				Condition: "bad",
			}),
		},
		expectError: `verification failed: caveat "bad" not satisfied: caveat not recognized`,
	}, {
		about: "two macaroons, only one ok",
		macaroons: []macaroon.Slice{
			newMacaroons(checkers.Caveat{
				Condition: "bad",
			}),
			newMacaroons(),
		},
	}, {
		about: "macaroon with declared caveats",
		macaroons: []macaroon.Slice{
			newMacaroons(
				checkers.DeclaredCaveat("key1", "value1"),
				checkers.DeclaredCaveat("key2", "value2"),
			),
		},
		expectDeclared: map[string]string{
			"key1": "value1",
			"key2": "value2",
		},
	}, {
		about: "macaroon with declared values and asserted keys with wrong value",
		macaroons: []macaroon.Slice{
			newMacaroons(
				checkers.DeclaredCaveat("key1", "value1"),
				checkers.DeclaredCaveat("key2", "value2"),
			),
		},
		assert: map[string]string{
			"key1": "valuex",
		},
		expectError: `verification failed: caveat "declared key1 value1" not satisfied: got key1="valuex", expected "value1"`,
	}, {
		about: "macaroon with declared values and asserted keys with correct value",
		macaroons: []macaroon.Slice{
			newMacaroons(
				checkers.DeclaredCaveat("key1", "value1"),
				checkers.DeclaredCaveat("key2", "value2"),
			),
		},
		assert: map[string]string{
			"key1": "value1",
		},
		expectDeclared: map[string]string{
			"key1": "value1",
			"key2": "value2",
		},
	}}
	for i, test := range tests {
		c.Logf("test %d: %s", i, test.about)
		if test.expectDeclared == nil {
			test.expectDeclared = make(map[string]string)
		}
		if test.checker == nil {
			test.checker = checkers.New()
		}

		decl, err := svc.CheckAny(test.macaroons, test.assert, test.checker)
		if test.expectError != "" {
			c.Assert(err, gc.ErrorMatches, test.expectError)
			c.Assert(decl, gc.HasLen, 0)
			continue
		}
		c.Assert(err, gc.IsNil)
		c.Assert(decl, jc.DeepEquals, test.expectDeclared)
	}
}

func newService(c *gc.C, location string, locator bakery.PublicKeyLocatorMap) *bakery.Service {
	keyPair, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)

	svc, err := bakery.NewService(bakery.NewServiceParams{
		Location: location,
		Key:      keyPair,
		Locator:  locator,
	})
	c.Assert(err, gc.IsNil)
	if locator != nil {
		locator[location] = &keyPair.Public
	}
	return svc
}

type strcmpChecker string

func (c strcmpChecker) CheckFirstPartyCaveat(caveat string) error {
	if caveat != string(c) {
		return fmt.Errorf("%v doesn't match %s", caveat, c)
	}
	return nil
}

func (c strcmpChecker) CheckThirdPartyCaveat(caveatId string, caveat string) ([]checkers.Caveat, error) {
	if caveat != string(c) {
		return nil, fmt.Errorf("%v doesn't match %s", caveat, c)
	}
	return nil, nil
}

type thirdPartyCheckerWithCaveats []checkers.Caveat

func (c thirdPartyCheckerWithCaveats) CheckThirdPartyCaveat(caveatId string, caveat string) ([]checkers.Caveat, error) {
	return c, nil
}
