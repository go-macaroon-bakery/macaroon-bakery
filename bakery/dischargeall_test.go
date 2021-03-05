package bakery_test

import (
	"context"
	"fmt"
	"testing"

	qt "github.com/frankban/quicktest"
	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v2"

	"gopkg.in/macaroon-bakery.v3/bakery"
	"gopkg.in/macaroon-bakery.v3/bakery/checkers"
)

func alwaysOK(string) error {
	return nil
}

func TestDischargeAllNoDischarges(t *testing.T) {
	c := qt.New(t)
	rootKey := []byte("root key")
	m, err := bakery.NewMacaroon(rootKey, []byte("id0"), "loc0", bakery.LatestVersion, testChecker.Namespace())
	c.Assert(err, qt.IsNil)
	ms, err := bakery.DischargeAll(testContext, m, noDischarge(c))
	c.Assert(err, qt.IsNil)
	c.Assert(ms, qt.HasLen, 1)
	c.Assert(ms[0].Signature(), qt.DeepEquals, m.M().Signature())

	err = m.M().Verify(rootKey, alwaysOK, nil)
	c.Assert(err, qt.IsNil)
}

func TestDischargeAllManyDischarges(t *testing.T) {
	c := qt.New(t)
	rootKey := []byte("root key")
	m0, err := bakery.NewMacaroon(rootKey, []byte("id0"), "loc0", bakery.LatestVersion, nil)
	c.Assert(err, qt.IsNil)
	totalRequired := 40
	id := 1
	addCaveats := func(m *bakery.Macaroon) {
		for i := 0; i < 2; i++ {
			if totalRequired == 0 {
				break
			}
			cid := fmt.Sprint("id", id)
			err := m.M().AddThirdPartyCaveat([]byte("root key "+cid), []byte(cid), "somewhere")
			c.Assert(err, qt.IsNil)
			id++
			totalRequired--
		}
	}
	addCaveats(m0)
	getDischarge := func(_ context.Context, cav macaroon.Caveat, payload []byte) (*bakery.Macaroon, error) {
		c.Check(payload, qt.IsNil)
		m, err := bakery.NewMacaroon([]byte("root key "+string(cav.Id)), cav.Id, "", bakery.LatestVersion, nil)
		c.Assert(err, qt.IsNil)
		addCaveats(m)
		return m, nil
	}
	ms, err := bakery.DischargeAll(testContext, m0, getDischarge)
	c.Assert(err, qt.IsNil)
	c.Assert(ms, qt.HasLen, 41)

	err = ms[0].Verify(rootKey, alwaysOK, ms[1:])
	c.Assert(err, qt.IsNil)
}

func TestDischargeAllManyDischargesWithRealThirdPartyCaveats(t *testing.T) {
	c := qt.New(t)
	// This is the same flow as TestDischargeAllManyDischarges except that we're
	// using actual third party caveats as added by Macaroon.AddCaveat and
	// we use a larger number of caveats so that caveat ids will need to get larger.
	locator := bakery.NewThirdPartyStore()
	bakeries := make(map[string]*bakery.Bakery)
	bakeryId := 0
	addBakery := func() string {
		bakeryId++
		loc := fmt.Sprint("loc", bakeryId)
		bakeries[loc] = newBakery(loc, locator)
		return loc
	}
	ts := newBakery("ts-loc", locator)
	const totalDischargesRequired = 40
	stillRequired := totalDischargesRequired
	checker := func(_ context.Context, ci *bakery.ThirdPartyCaveatInfo) (caveats []checkers.Caveat, _ error) {
		if string(ci.Condition) != "something" {
			return nil, errgo.Newf("unexpected condition")
		}
		for i := 0; i < 3; i++ {
			if stillRequired <= 0 {
				break
			}
			caveats = append(caveats, checkers.Caveat{
				Location:  addBakery(),
				Condition: "something",
			})
			stillRequired--
		}
		return caveats, nil
	}

	rootKey := []byte("root key")
	m0, err := bakery.NewMacaroon(rootKey, []byte("id0"), "ts-loc", bakery.LatestVersion, nil)
	c.Assert(err, qt.IsNil)
	err = m0.AddCaveat(testContext, checkers.Caveat{
		Location:  addBakery(),
		Condition: "something",
	}, ts.Oven.Key(), locator)
	c.Assert(err, qt.IsNil)
	// We've added a caveat (the first) so one less caveat is required.
	stillRequired--
	getDischarge := func(ctx context.Context, cav macaroon.Caveat, payload []byte) (*bakery.Macaroon, error) {
		return bakery.Discharge(ctx, bakery.DischargeParams{
			Id:      cav.Id,
			Caveat:  payload,
			Key:     bakeries[cav.Location].Oven.Key(),
			Checker: bakery.ThirdPartyCaveatCheckerFunc(checker),
			Locator: locator,
		})
	}
	ms, err := bakery.DischargeAll(testContext, m0, getDischarge)
	c.Assert(err, qt.IsNil)
	c.Assert(ms, qt.HasLen, totalDischargesRequired+1)

	err = ms[0].Verify(rootKey, alwaysOK, ms[1:])
	c.Assert(err, qt.IsNil)
}

func TestDischargeAllLocalDischarge(t *testing.T) {
	c := qt.New(t)
	oc := newBakery("ts", nil)

	clientKey, err := bakery.GenerateKey()
	c.Assert(err, qt.IsNil)

	m, err := oc.Oven.NewMacaroon(testContext, bakery.LatestVersion, []checkers.Caveat{
		bakery.LocalThirdPartyCaveat(&clientKey.Public, bakery.LatestVersion),
	}, basicOp)
	c.Assert(err, qt.IsNil)

	ms, err := bakery.DischargeAllWithKey(testContext, m, noDischarge(c), clientKey)
	c.Assert(err, qt.IsNil)

	_, err = oc.Checker.Auth(ms).Allow(testContext, basicOp)
	c.Assert(err, qt.IsNil)
}

func TestDischargeAllLocalDischargeVersion1(t *testing.T) {
	c := qt.New(t)
	oc := newBakery("ts", nil)

	clientKey, err := bakery.GenerateKey()
	c.Assert(err, qt.IsNil)

	m, err := oc.Oven.NewMacaroon(testContext, bakery.Version1, []checkers.Caveat{
		bakery.LocalThirdPartyCaveat(&clientKey.Public, bakery.Version1),
	}, basicOp)
	c.Assert(err, qt.IsNil)

	ms, err := bakery.DischargeAllWithKey(testContext, m, noDischarge(c), clientKey)
	c.Assert(err, qt.IsNil)

	_, err = oc.Checker.Auth(ms).Allow(testContext, basicOp)
	c.Assert(err, qt.IsNil)
}
