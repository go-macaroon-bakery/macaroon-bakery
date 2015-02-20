package bakery_test

import (
	"fmt"

	gc "gopkg.in/check.v1"
	"gopkg.in/macaroon.v1"

	"gopkg.in/macaroon-bakery.v1/bakery"
	"gopkg.in/macaroon-bakery.v1/bakery/checkers"
)

type DischargeSuite struct{}

var _ = gc.Suite(&DischargeSuite{})

func alwaysOK(string) error {
	return nil
}

func (*DischargeSuite) TestDischargeAllNoDischarges(c *gc.C) {
	rootKey := []byte("root key")
	m, err := macaroon.New(rootKey, "id0", "loc0")
	c.Assert(err, gc.IsNil)
	ms, err := bakery.DischargeAll(m, noDischarge(c))
	c.Assert(err, gc.IsNil)
	c.Assert(ms, gc.HasLen, 1)
	c.Assert(ms[0], gc.Equals, m)

	err = m.Verify(rootKey, alwaysOK, nil)
	c.Assert(err, gc.IsNil)
}

func (*DischargeSuite) TestDischargeAllManyDischarges(c *gc.C) {
	rootKey := []byte("root key")
	m0, err := macaroon.New(rootKey, "id0", "location0")
	c.Assert(err, gc.IsNil)
	totalRequired := 40
	id := 1
	addCaveats := func(m *macaroon.Macaroon) {
		for i := 0; i < 2; i++ {
			if totalRequired == 0 {
				break
			}
			cid := fmt.Sprint("id", id)
			err := m.AddThirdPartyCaveat([]byte("root key "+cid), cid, "somewhere")
			c.Assert(err, gc.IsNil)
			id++
			totalRequired--
		}
	}
	addCaveats(m0)
	getDischarge := func(loc string, cav macaroon.Caveat) (*macaroon.Macaroon, error) {
		c.Assert(loc, gc.Equals, "location0")
		m, err := macaroon.New([]byte("root key "+cav.Id), cav.Id, "")
		c.Assert(err, gc.IsNil)
		addCaveats(m)
		return m, nil
	}
	ms, err := bakery.DischargeAll(m0, getDischarge)
	c.Assert(err, gc.IsNil)
	c.Assert(ms, gc.HasLen, 41)

	err = ms[0].Verify(rootKey, alwaysOK, ms[1:])
	c.Assert(err, gc.IsNil)
}

func (*DischargeSuite) TestDischargeAllLocalDischarge(c *gc.C) {
	svc, err := bakery.NewService(bakery.NewServiceParams{})
	c.Assert(err, gc.IsNil)

	clientKey, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)

	m, err := svc.NewMacaroon("", nil, []checkers.Caveat{
		bakery.LocalThirdPartyCaveat(&clientKey.Public),
	})
	c.Assert(err, gc.IsNil)

	ms, err := bakery.DischargeAllWithKey(m, noDischarge(c), clientKey)
	c.Assert(err, gc.IsNil)

	err = svc.Check(ms, checkers.New())
	c.Assert(err, gc.IsNil)
}

func noDischarge(c *gc.C) func(string, macaroon.Caveat) (*macaroon.Macaroon, error) {
	return func(string, macaroon.Caveat) (*macaroon.Macaroon, error) {
		c.Errorf("getDischarge called unexpectedly")
		return nil, fmt.Errorf("nothing")
	}
}
