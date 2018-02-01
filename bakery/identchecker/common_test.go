package identchecker_test

import (
	"encoding/json"
	"time"

	"golang.org/x/net/context"
	"gopkg.in/macaroon.v2"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
)

// testContext holds the testing background context - its associated time when checking
// time-before caveats will always be the value of epoch.
var testContext = checkers.ContextWithClock(context.Background(), stoppedClock{epoch})

var (
	epoch = time.Date(1900, 11, 17, 19, 00, 13, 0, time.UTC)
)

var testChecker = func() *checkers.Checker {
	c := checkers.New(nil)
	c.Namespace().Register("testns", "")
	c.Register("true", "testns", trueCheck)
	return c
}()

// trueCheck always succeeds.
func trueCheck(ctx context.Context, cond, args string) error {
	return nil
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

func contextWithBasicAuth(ctx context.Context, user, password string) context.Context {
	return context.WithValue(ctx, basicAuthKey{}, basicAuth{user, password})
}

func basicAuthFromContext(ctx context.Context) (user, password string) {
	auth, _ := ctx.Value(basicAuthKey{}).(basicAuth)
	return auth.user, auth.password
}

func mustGenerateKey() *bakery.KeyPair {
	return bakery.MustGenerateKey()
}
