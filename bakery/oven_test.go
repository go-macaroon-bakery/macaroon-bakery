package bakery_test

import (
	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/macaroon.v2"

	"gopkg.in/macaroon-bakery.v2/bakery"
)

type ovenSuite struct {
	jujutesting.LoggingSuite
}

var _ = gc.Suite(&ovenSuite{})

var canonicalOpsTests = []struct {
	about  string
	ops    []bakery.Op
	expect []bakery.Op
}{{
	about: "empty slice",
}, {
	about:  "one element",
	ops:    []bakery.Op{{"a", "a"}},
	expect: []bakery.Op{{"a", "a"}},
}, {
	about:  "all in order",
	ops:    []bakery.Op{{"a", "a"}, {"a", "b"}, {"c", "c"}},
	expect: []bakery.Op{{"a", "a"}, {"a", "b"}, {"c", "c"}},
}, {
	about:  "out of order",
	ops:    []bakery.Op{{"c", "c"}, {"a", "b"}, {"a", "a"}},
	expect: []bakery.Op{{"a", "a"}, {"a", "b"}, {"c", "c"}},
}, {
	about:  "with duplicates",
	ops:    []bakery.Op{{"c", "c"}, {"a", "b"}, {"a", "a"}, {"c", "a"}, {"c", "b"}, {"c", "c"}, {"a", "a"}},
	expect: []bakery.Op{{"a", "a"}, {"a", "b"}, {"c", "a"}, {"c", "b"}, {"c", "c"}},
}, {
	about:  "make sure we've got the fields right",
	ops:    []bakery.Op{{Entity: "read", Action: "two"}, {Entity: "read", Action: "one"}, {Entity: "write", Action: "one"}},
	expect: []bakery.Op{{Entity: "read", Action: "one"}, {Entity: "read", Action: "two"}, {Entity: "write", Action: "one"}},
}}

func (*ovenSuite) TestCanonicalOps(c *gc.C) {
	for i, test := range canonicalOpsTests {
		c.Logf("test %d: %v", i, test.about)
		ops := append([]bakery.Op(nil), test.ops...)
		c.Assert(bakery.CanonicalOps(ops), jc.DeepEquals, test.expect)
		// Verify that the original slice isn't changed.
		c.Assert(ops, jc.DeepEquals, test.ops)
	}
}

func (*ovenSuite) TestMultipleOps(c *gc.C) {
	oven := bakery.NewOven(bakery.OvenParams{})
	ops := []bakery.Op{{"one", "read"}, {"one", "write"}, {"two", "read"}}
	m, err := oven.NewMacaroon(testContext, bakery.LatestVersion, nil, ops...)
	c.Assert(err, gc.IsNil)
	gotOps, conds, err := oven.VerifyMacaroon(testContext, macaroon.Slice{m.M()})
	c.Assert(err, gc.IsNil)
	c.Assert(conds, gc.HasLen, 0)
	c.Assert(bakery.CanonicalOps(gotOps), jc.DeepEquals, ops)
}

func (*ovenSuite) TestMultipleOpsInId(c *gc.C) {
	oven := bakery.NewOven(bakery.OvenParams{})

	ops := []bakery.Op{{"one", "read"}, {"one", "write"}, {"two", "read"}}
	m, err := oven.NewMacaroon(testContext, bakery.LatestVersion, nil, ops...)
	c.Assert(err, gc.IsNil)
	gotOps, conds, err := oven.VerifyMacaroon(testContext, macaroon.Slice{m.M()})
	c.Assert(err, gc.IsNil)
	c.Assert(conds, gc.HasLen, 0)
	c.Assert(bakery.CanonicalOps(gotOps), jc.DeepEquals, ops)
}

func (*ovenSuite) TestMultipleOpsInIdWithVersion1(c *gc.C) {
	oven := bakery.NewOven(bakery.OvenParams{})

	ops := []bakery.Op{{"one", "read"}, {"one", "write"}, {"two", "read"}}
	m, err := oven.NewMacaroon(testContext, bakery.Version1, nil, ops...)
	c.Assert(err, gc.IsNil)
	gotOps, conds, err := oven.VerifyMacaroon(testContext, macaroon.Slice{m.M()})
	c.Assert(err, gc.IsNil)
	c.Assert(conds, gc.HasLen, 0)
	c.Assert(bakery.CanonicalOps(gotOps), jc.DeepEquals, ops)
}
