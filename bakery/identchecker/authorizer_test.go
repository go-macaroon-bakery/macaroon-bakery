package identchecker_test

import (
	"context"
	"testing"

	qt "github.com/frankban/quicktest"
	"gopkg.in/errgo.v1"

	"github.com/go-macaroon-bakery/macaroon-bakery/v3/bakery"
	"github.com/go-macaroon-bakery/macaroon-bakery/v3/bakery/checkers"
	"github.com/go-macaroon-bakery/macaroon-bakery/v3/bakery/identchecker"
)

func TestAuthorizerFunc(t *testing.T) {
	c := qt.New(t)
	f := func(ctx context.Context, id identchecker.Identity, op bakery.Op) (bool, []checkers.Caveat, error) {
		c.Assert(ctx, qt.Equals, testContext)
		c.Assert(id, qt.Equals, identchecker.SimpleIdentity("bob"))
		switch op.Entity {
		case "a":
			return false, nil, nil
		case "b":
			return true, nil, nil
		case "c":
			return true, []checkers.Caveat{{
				Location:  "somewhere",
				Condition: "c",
			}}, nil
		case "d":
			return true, []checkers.Caveat{{
				Location:  "somewhere",
				Condition: "d",
			}}, nil
		}
		c.Fatalf("unexpected entity: %q", op.Entity)
		return false, nil, nil
	}
	allowed, caveats, err := identchecker.AuthorizerFunc(f).Authorize(testContext, identchecker.SimpleIdentity("bob"), []bakery.Op{{"a", "x"}, {"b", "x"}, {"c", "x"}, {"d", "x"}})
	c.Assert(err, qt.IsNil)
	c.Assert(allowed, qt.DeepEquals, []bool{false, true, true, true})
	c.Assert(caveats, qt.DeepEquals, []checkers.Caveat{{
		Location:  "somewhere",
		Condition: "c",
	}, {
		Location:  "somewhere",
		Condition: "d",
	}})
}

var aclAuthorizerTests = []struct {
	about         string
	auth          identchecker.ACLAuthorizer
	identity      identchecker.Identity
	ops           []bakery.Op
	expectAllowed []bool
	expectError   string
}{{
	about: "no ops, no problem",
	auth: identchecker.ACLAuthorizer{
		GetACL: func(ctx context.Context, op bakery.Op) ([]string, bool, error) {
			return nil, false, nil
		},
	},
}, {
	about: "identity that does not implement ACLIdentity; user should be denied except for everyone group",
	auth: identchecker.ACLAuthorizer{
		GetACL: func(ctx context.Context, op bakery.Op) ([]string, bool, error) {
			if op.Entity == "a" {
				return []string{identchecker.Everyone}, true, nil
			} else {
				return []string{"alice"}, false, nil
			}
		},
	},
	identity: simplestIdentity("bob"),
	ops: []bakery.Op{{
		Entity: "a",
		Action: "a",
	}, {
		Entity: "b",
		Action: "b",
	}},
	expectAllowed: []bool{true, false},
}, {
	about: "identity that does not implement ACLIdentity with user == Id; user should be denied except for everyone group",
	auth: identchecker.ACLAuthorizer{
		GetACL: func(ctx context.Context, op bakery.Op) ([]string, bool, error) {
			if op.Entity == "a" {
				return []string{identchecker.Everyone}, true, nil
			} else {
				return []string{"bob"}, false, nil
			}
		},
	},
	identity: simplestIdentity("bob"),
	ops: []bakery.Op{{
		Entity: "a",
		Action: "a",
	}, {
		Entity: "b",
		Action: "b",
	}},
	expectAllowed: []bool{true, false},
}, {
	about: "permission denied for everyone without allow-public",
	auth: identchecker.ACLAuthorizer{
		GetACL: func(ctx context.Context, op bakery.Op) ([]string, bool, error) {
			return []string{identchecker.Everyone}, false, nil
		},
	},
	identity: simplestIdentity("bob"),
	ops: []bakery.Op{{
		Entity: "a",
		Action: "a",
	}},
	expectAllowed: []bool{false},
}, {
	about: "permission granted to anyone with no identity with allow-public",
	auth: identchecker.ACLAuthorizer{
		GetACL: func(ctx context.Context, op bakery.Op) ([]string, bool, error) {
			return []string{identchecker.Everyone}, true, nil
		},
	},
	ops: []bakery.Op{{
		Entity: "a",
		Action: "a",
	}},
	expectAllowed: []bool{true},
}, {
	about: "error return causes all authorization to fail",
	auth: identchecker.ACLAuthorizer{
		GetACL: func(ctx context.Context, op bakery.Op) ([]string, bool, error) {
			if op.Entity == "a" {
				return []string{identchecker.Everyone}, true, nil
			} else {
				return nil, false, errgo.New("some error")
			}
		},
	},
	ops: []bakery.Op{{
		Entity: "a",
		Action: "a",
	}, {
		Entity: "b",
		Action: "b",
	}},
	expectError: "some error",
}}

func TestACLAuthorizer(t *testing.T) {
	c := qt.New(t)
	for i, test := range aclAuthorizerTests {
		c.Logf("test %d: %v", i, test.about)
		allowed, caveats, err := test.auth.Authorize(context.Background(), test.identity, test.ops)
		if test.expectError != "" {
			c.Assert(err, qt.ErrorMatches, test.expectError)
			c.Assert(allowed, qt.IsNil)
			c.Assert(caveats, qt.IsNil)
			continue
		}
		c.Assert(err, qt.IsNil)
		c.Assert(caveats, qt.IsNil)
		c.Assert(allowed, qt.DeepEquals, test.expectAllowed)
	}
}

// simplestIdentity implements Identity for a string. Unlike
// simpleIdentity, it does not implement ACLIdentity.
type simplestIdentity string

func (id simplestIdentity) Id() string {
	return string(id)
}

func (simplestIdentity) Domain() string {
	return ""
}
