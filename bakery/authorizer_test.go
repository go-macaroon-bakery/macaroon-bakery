package bakery_test

import (
	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	"golang.org/x/net/context"
	gc "gopkg.in/check.v1"
	errgo "gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
)

type authorizerSuite struct {
	jujutesting.LoggingSuite
}

var _ = gc.Suite(&authorizerSuite{})

func (*authorizerSuite) TestAuthorizerFunc(c *gc.C) {
	f := func(ctx context.Context, id bakery.Identity, op bakery.Op) (bool, []checkers.Caveat, error) {
		c.Assert(ctx, gc.Equals, testContext)
		c.Assert(id, gc.Equals, bakery.SimpleIdentity("bob"))
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
	allowed, caveats, err := bakery.AuthorizerFunc(f).Authorize(testContext, bakery.SimpleIdentity("bob"), []bakery.Op{{"a", "x"}, {"b", "x"}, {"c", "x"}, {"d", "x"}})
	c.Assert(err, gc.IsNil)
	c.Assert(allowed, jc.DeepEquals, []bool{false, true, true, true})
	c.Assert(caveats, jc.DeepEquals, []checkers.Caveat{{
		Location:  "somewhere",
		Condition: "c",
	}, {
		Location:  "somewhere",
		Condition: "d",
	}})
}

var aclAuthorizerTests = []struct {
	about         string
	auth          bakery.ACLAuthorizer
	identity      bakery.Identity
	ops           []bakery.Op
	expectAllowed []bool
	expectError   string
}{{
	about: "no ops, no problem",
	auth: bakery.ACLAuthorizer{
		GetACL: func(ctx context.Context, op bakery.Op) ([]string, bool, error) {
			return nil, false, nil
		},
	},
}, {
	about: "identity that does not implement ACLIdentity; user should be denied except for everyone group",
	auth: bakery.ACLAuthorizer{
		GetACL: func(ctx context.Context, op bakery.Op) ([]string, bool, error) {
			if op.Entity == "a" {
				return []string{bakery.Everyone}, true, nil
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
	auth: bakery.ACLAuthorizer{
		GetACL: func(ctx context.Context, op bakery.Op) ([]string, bool, error) {
			if op.Entity == "a" {
				return []string{bakery.Everyone}, true, nil
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
	auth: bakery.ACLAuthorizer{
		GetACL: func(ctx context.Context, op bakery.Op) ([]string, bool, error) {
			return []string{bakery.Everyone}, false, nil
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
	auth: bakery.ACLAuthorizer{
		GetACL: func(ctx context.Context, op bakery.Op) ([]string, bool, error) {
			return []string{bakery.Everyone}, true, nil
		},
	},
	ops: []bakery.Op{{
		Entity: "a",
		Action: "a",
	}},
	expectAllowed: []bool{true},
}, {
	about: "error return causes all authorization to fail",
	auth: bakery.ACLAuthorizer{
		GetACL: func(ctx context.Context, op bakery.Op) ([]string, bool, error) {
			if op.Entity == "a" {
				return []string{bakery.Everyone}, true, nil
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

func (*authorizerSuite) TestACLAuthorizer(c *gc.C) {
	for i, test := range aclAuthorizerTests {
		c.Logf("test %d: %v", i, test.about)
		allowed, caveats, err := test.auth.Authorize(context.Background(), test.identity, test.ops)
		if test.expectError != "" {
			c.Assert(err, gc.ErrorMatches, test.expectError)
			c.Assert(allowed, gc.IsNil)
			c.Assert(caveats, gc.IsNil)
			continue
		}
		c.Assert(err, gc.IsNil)
		c.Assert(caveats, gc.IsNil)
		c.Assert(allowed, jc.DeepEquals, test.expectAllowed)
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
