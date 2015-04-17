package bakerytest_test

import (
	"fmt"
	"net/http"
	"net/url"

	gc "gopkg.in/check.v1"

	"gopkg.in/macaroon-bakery.v0/bakery"
	"gopkg.in/macaroon-bakery.v0/bakery/checkers"
	"gopkg.in/macaroon-bakery.v0/bakerytest"
	"gopkg.in/macaroon-bakery.v0/httpbakery"
)

type suite struct {
	httpClient *http.Client
}

func (s *suite) SetUpTest(c *gc.C) {
	s.httpClient = httpbakery.NewHTTPClient()
}

var _ = gc.Suite(&suite{})

func noCaveatChecker(_ *http.Request, cond, arg string) ([]checkers.Caveat, error) {
	return nil, nil
}

func (s *suite) TestDischargerSimple(c *gc.C) {
	d := bakerytest.NewDischarger(nil, noCaveatChecker)
	defer d.Close()

	svc, err := bakery.NewService(bakery.NewServiceParams{
		Location: "here",
		Locator:  d,
	})
	c.Assert(err, gc.IsNil)
	m, err := svc.NewMacaroon("", nil, []checkers.Caveat{{
		Location:  d.Location(),
		Condition: "something",
	}})
	c.Assert(err, gc.IsNil)
	ms, err := httpbakery.DischargeAll(m, s.httpClient, noInteraction)
	c.Assert(err, gc.IsNil)
	c.Assert(ms, gc.HasLen, 2)

	err = svc.Check(ms, failChecker)
	c.Assert(err, gc.IsNil)
}

var failChecker = bakery.FirstPartyCheckerFunc(func(s string) error {
	return fmt.Errorf("fail %s", s)
})

func (s *suite) TestDischargerTwoLevels(c *gc.C) {
	d1checker := func(_ *http.Request, cond, arg string) ([]checkers.Caveat, error) {
		if cond != "xtrue" {
			return nil, fmt.Errorf("caveat refused")
		}
		return nil, nil
	}
	d1 := bakerytest.NewDischarger(nil, d1checker)
	defer d1.Close()
	d2checker := func(_ *http.Request, cond, arg string) ([]checkers.Caveat, error) {
		return []checkers.Caveat{{
			Location:  d1.Location(),
			Condition: "x" + cond,
		}}, nil
	}
	d2 := bakerytest.NewDischarger(d1, d2checker)
	defer d2.Close()
	locator := bakery.PublicKeyLocatorMap{
		d1.Location(): d1.Service.PublicKey(),
		d2.Location(): d2.Service.PublicKey(),
	}
	c.Logf("map: %s", locator)
	svc, err := bakery.NewService(bakery.NewServiceParams{
		Location: "here",
		Locator:  locator,
	})
	c.Assert(err, gc.IsNil)
	m, err := svc.NewMacaroon("", nil, []checkers.Caveat{{
		Location:  d2.Location(),
		Condition: "true",
	}})
	c.Assert(err, gc.IsNil)

	ms, err := httpbakery.DischargeAll(m, s.httpClient, noInteraction)
	c.Assert(err, gc.IsNil)
	c.Assert(ms, gc.HasLen, 3)

	err = svc.Check(ms, failChecker)
	c.Assert(err, gc.IsNil)

	err = svc.AddCaveat(m, checkers.Caveat{
		Location:  d2.Location(),
		Condition: "nope",
	})
	c.Assert(err, gc.IsNil)

	ms, err = httpbakery.DischargeAll(m, s.httpClient, noInteraction)
	c.Assert(err, gc.ErrorMatches, `cannot get discharge from "http://[^"]*": third party refused discharge: cannot discharge: caveat refused`)
	c.Assert(ms, gc.HasLen, 0)
}

func noInteraction(*url.URL) error {
	return fmt.Errorf("unexpected interaction required")
}
