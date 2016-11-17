package bakery_test

import (
	"encoding/json"
	"fmt"
	"time"

	"golang.org/x/net/context"
	gc "gopkg.in/check.v1"
	"gopkg.in/macaroon.v2-unstable"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
)

// testContext holds the testing background context - its associated time when checking
// time-before caveats will always be the value of epoch.
var testContext = checkers.ContextWithClock(context.Background(), stoppedClock{epoch})

var (
	epoch = time.Date(1900, 11, 17, 19, 00, 13, 0, time.UTC)
	ages  = epoch.Add(24 * time.Hour)
)

var loginOps = []bakery.Op{bakery.LoginOp}

var testChecker = func() *checkers.Checker {
	c := checkers.New(nil)
	c.Namespace().Register("testns", "")
	c.Register("str", "testns", strCheck)
	c.Register("true", "testns", trueCheck)
	return c
}()

func noDischarge(c *gc.C) func(macaroon.Caveat) (*macaroon.Macaroon, error) {
	return func(macaroon.Caveat) (*macaroon.Macaroon, error) {
		c.Errorf("getDischarge called unexpectedly")
		return nil, fmt.Errorf("nothing")
	}
}

type noIdentities struct{}

func (noIdentities) IdentityFromContext(ctxt context.Context) (bakery.Identity, []checkers.Caveat, error) {
	return nil, nil, nil
}

func (noIdentities) DeclaredIdentity(declared map[string]string) (bakery.Identity, error) {
	return noone{}, nil
}

type noone struct{}

func (noone) Id() string {
	return "noone"
}

func (noone) Domain() string {
	return ""
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
func trueCheck(ctxt context.Context, cond, args string) error {
	return nil
}

// strCheck checks that the string value in the context
// matches the argument to the condition.
func strCheck(ctxt context.Context, cond, args string) error {
	expect, _ := ctxt.Value(strKey{}).(string)
	if args != expect {
		return fmt.Errorf("%s doesn't match %s", cond, expect)
	}
	return nil
}

type thirdPartyStrcmpChecker string

func (c thirdPartyStrcmpChecker) CheckThirdPartyCaveat(_ context.Context, cavInfo *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
	if cavInfo.Condition != string(c) {
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

type basicAuthKey struct{}

type basicAuth struct {
	user, password string
}

func contextWithBasicAuth(ctxt context.Context, user, password string) context.Context {
	return context.WithValue(ctxt, basicAuthKey{}, basicAuth{user, password})
}

func basicAuthFromContext(ctxt context.Context) (user, password string) {
	auth, _ := ctxt.Value(basicAuthKey{}).(basicAuth)
	return auth.user, auth.password
}

func mustGenerateKey() *bakery.KeyPair {
	key, err := bakery.GenerateKey()
	if err != nil {
		panic(err)
	}
	return key
}
