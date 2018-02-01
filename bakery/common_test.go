package bakery_test

import (
	"encoding/json"
	"fmt"
	"time"

	"golang.org/x/net/context"
	gc "gopkg.in/check.v1"
	"gopkg.in/macaroon.v2"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
)

// testContext holds the testing background context - its associated time when checking
// time-before caveats will always be the value of epoch.
var testContext = checkers.ContextWithClock(context.Background(), stoppedClock{epoch})

var basicOp = bakery.Op{"basic", "basic"}

var (
	epoch = time.Date(1900, 11, 17, 19, 00, 13, 0, time.UTC)
)

var testChecker = func() *checkers.Checker {
	c := checkers.New(nil)
	c.Namespace().Register("testns", "")
	c.Register("str", "testns", strCheck)
	c.Register("true", "testns", trueCheck)
	return c
}()

// newBakery returns a new Bakery instance using a new
// key pair, and registers the key with the given locator if provided.
//
// It uses testChecker to check first party caveats.
func newBakery(location string, locator *bakery.ThirdPartyStore) *bakery.Bakery {
	key := mustGenerateKey()
	p := bakery.BakeryParams{
		Key:      key,
		Checker:  testChecker,
		Location: location,
	}
	if locator != nil {
		p.Locator = locator
		locator.AddInfo(location, bakery.ThirdPartyInfo{
			PublicKey: key.Public,
			Version:   bakery.LatestVersion,
		})
	}
	return bakery.New(p)
}

func noDischarge(c *gc.C) func(context.Context, macaroon.Caveat, []byte) (*bakery.Macaroon, error) {
	return func(context.Context, macaroon.Caveat, []byte) (*bakery.Macaroon, error) {
		c.Errorf("getDischarge called unexpectedly")
		return nil, fmt.Errorf("nothing")
	}
}

type strKey struct{}

func strContext(s string) context.Context {
	return context.WithValue(testContext, strKey{}, s)
}

func strCaveat(s string) checkers.Caveat {
	return checkers.Caveat{
		Condition: "str " + s,
		Namespace: "testns",
	}
}

func trueCaveat(s string) checkers.Caveat {
	return checkers.Caveat{
		Condition: "true " + s,
		Namespace: "testns",
	}
}

// trueCheck always succeeds.
func trueCheck(ctx context.Context, cond, args string) error {
	return nil
}

// strCheck checks that the string value in the context
// matches the argument to the condition.
func strCheck(ctx context.Context, cond, args string) error {
	expect, _ := ctx.Value(strKey{}).(string)
	if args != expect {
		return fmt.Errorf("%s doesn't match %s", cond, expect)
	}
	return nil
}

type thirdPartyStrcmpChecker string

func (c thirdPartyStrcmpChecker) CheckThirdPartyCaveat(_ context.Context, cavInfo *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
	if string(cavInfo.Condition) != string(c) {
		return nil, fmt.Errorf("%s doesn't match %s", cavInfo.Condition, c)
	}
	return nil, nil
}

type thirdPartyCheckerWithCaveats []checkers.Caveat

func (c thirdPartyCheckerWithCaveats) CheckThirdPartyCaveat(_ context.Context, cavInfo *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
	return c, nil
}

func macStr(m *macaroon.Macaroon) string {
	data, err := json.MarshalIndent(m, "\t", "\t")
	if err != nil {
		panic(err)
	}
	return string(data)
}

type stoppedClock struct {
	t time.Time
}

func (t stoppedClock) Now() time.Time {
	return t.t
}

func mustGenerateKey() *bakery.KeyPair {
	return bakery.MustGenerateKey()
}
