package bakery_test

import (
	"fmt"

	"github.com/juju/testing"
	gc "gopkg.in/check.v1"
	"gopkg.in/macaroon.v2-unstable"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
)

type DischargeSuite struct {
	testing.IsolationSuite
}

var _ = gc.Suite(&DischargeSuite{})

func alwaysOK(string) error {
	return nil
}

var macaroonCurrentVersion = bakery.MacaroonVersion(bakery.LatestVersion)

func (*DischargeSuite) TestDischargeAllNoDischarges(c *gc.C) {
	rootKey := []byte("root key")
	m, err := macaroon.New(rootKey, []byte("id0"), "loc0", macaroonCurrentVersion)
	c.Assert(err, gc.IsNil)
	ms, err := bakery.DischargeAll(testContext, m, noDischarge(c))
	c.Assert(err, gc.IsNil)
	c.Assert(ms, gc.HasLen, 1)
	c.Assert(ms[0], gc.Equals, m)

	err = m.Verify(rootKey, alwaysOK, nil)
	c.Assert(err, gc.IsNil)
}

func (*DischargeSuite) TestDischargeAllManyDischarges(c *gc.C) {
	rootKey := []byte("root key")
	m0, err := macaroon.New(rootKey, []byte("id0"), "location0", macaroonCurrentVersion)
	c.Assert(err, gc.IsNil)
	totalRequired := 40
	id := 1
	addCaveats := func(m *macaroon.Macaroon) {
		for i := 0; i < 2; i++ {
			if totalRequired == 0 {
				break
			}
			cid := fmt.Sprint("id", id)
			err := m.AddThirdPartyCaveat([]byte("root key "+cid), []byte(cid), "somewhere")
			c.Assert(err, gc.IsNil)
			id++
			totalRequired--
		}
	}
	addCaveats(m0)
	getDischarge := func(cav macaroon.Caveat) (*macaroon.Macaroon, error) {
		m, err := macaroon.New([]byte("root key "+string(cav.Id)), cav.Id, "", macaroonCurrentVersion)
		c.Assert(err, gc.IsNil)
		addCaveats(m)
		return m, nil
	}
	ms, err := bakery.DischargeAll(testContext, m0, getDischarge)
	c.Assert(err, gc.IsNil)
	c.Assert(ms, gc.HasLen, 41)

	err = ms[0].Verify(rootKey, alwaysOK, ms[1:])
	c.Assert(err, gc.IsNil)
}

func (*DischargeSuite) TestDischargeAllLocalDischarge(c *gc.C) {
	oc := newBakery("ts", nil)

	clientKey, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)

	m, err := oc.Oven.NewMacaroon(testContext, macaroon.LatestVersion, ages, []checkers.Caveat{
		bakery.LocalThirdPartyCaveat(&clientKey.Public, bakery.LatestVersion),
	}, bakery.LoginOp)
	c.Assert(err, gc.IsNil)

	ms, err := bakery.DischargeAllWithKey(testContext, m, noDischarge(c), clientKey)
	c.Assert(err, gc.IsNil)

	_, err = oc.Checker.Auth(ms).Allow(testContext, loginOps...)
	c.Assert(err, gc.IsNil)
}

func (*DischargeSuite) TestDischargeAllLocalDischargeVersion1(c *gc.C) {
	oc := newBakery("ts", nil)

	clientKey, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)

	m, err := oc.Oven.NewMacaroon(testContext, macaroon.V1, ages, []checkers.Caveat{
		bakery.LocalThirdPartyCaveat(&clientKey.Public, bakery.Version1),
	}, bakery.LoginOp)
	c.Assert(err, gc.IsNil)

	ms, err := bakery.DischargeAllWithKey(testContext, m, noDischarge(c), clientKey)
	c.Assert(err, gc.IsNil)

	_, err = oc.Checker.Auth(ms).Allow(testContext, bakery.LoginOp)
	c.Assert(err, gc.IsNil)
}
