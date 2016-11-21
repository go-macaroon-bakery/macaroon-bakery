package bakery_test

import (
	"fmt"

	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/macaroon.v2-unstable"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
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
	oven := bakery.NewOven(bakery.OvenParams{
		OpsStore: bakery.NewMemOpsStore(),
	})
	ops := []bakery.Op{{"one", "read"}, {"one", "write"}, {"two", "read"}}
	m, err := oven.NewMacaroon(testContext, macaroon.LatestVersion, ages, nil, ops...)
	c.Assert(err, gc.IsNil)
	gotOps, conds, err := oven.MacaroonOps(testContext, macaroon.Slice{m})
	c.Assert(err, gc.IsNil)
	c.Assert(conds, gc.HasLen, 1) // time-before caveat.
	c.Assert(bakery.CanonicalOps(gotOps), jc.DeepEquals, ops)
}

func (*ovenSuite) TestMultipleOpsInId(c *gc.C) {
	oven := bakery.NewOven(bakery.OvenParams{})

	ops := []bakery.Op{{"one", "read"}, {"one", "write"}, {"two", "read"}}
	m, err := oven.NewMacaroon(testContext, macaroon.LatestVersion, ages, nil, ops...)
	c.Assert(err, gc.IsNil)
	gotOps, conds, err := oven.MacaroonOps(testContext, macaroon.Slice{m})
	c.Assert(err, gc.IsNil)
	c.Assert(conds, gc.HasLen, 1) // time-before caveat.
	c.Assert(bakery.CanonicalOps(gotOps), jc.DeepEquals, ops)
}

func (*ovenSuite) TestMultipleOpsInIdWithVersion1(c *gc.C) {
	oven := bakery.NewOven(bakery.OvenParams{})

	ops := []bakery.Op{{"one", "read"}, {"one", "write"}, {"two", "read"}}
	m, err := oven.NewMacaroon(testContext, macaroon.V1, ages, nil, ops...)
	c.Assert(err, gc.IsNil)
	gotOps, conds, err := oven.MacaroonOps(testContext, macaroon.Slice{m})
	c.Assert(err, gc.IsNil)
	c.Assert(conds, gc.HasLen, 1) // time-before caveat.
	c.Assert(bakery.CanonicalOps(gotOps), jc.DeepEquals, ops)
}

func (*ovenSuite) TestHugeNumberOfOpsGivesSmallMacaroon(c *gc.C) {
	oven := bakery.NewOven(bakery.OvenParams{
		OpsStore: bakery.NewMemOpsStore(),
	})
	ops := make([]bakery.Op, 30000)
	for i := range ops {
		ops[i] = bakery.Op{fmt.Sprintf("entity%d", i), fmt.Sprintf("action%d", i)}
	}
	m, err := oven.NewMacaroon(testContext, macaroon.LatestVersion, ages, nil, ops...)
	c.Assert(err, gc.IsNil)

	// Sanity-check that all the operations really are stored there.
	gotOps, _, err := oven.MacaroonOps(testContext, macaroon.Slice{m})
	c.Assert(err, gc.IsNil)
	c.Assert(bakery.CanonicalOps(gotOps), jc.DeepEquals, bakery.CanonicalOps(ops))

	data, err := m.MarshalBinary()
	c.Assert(err, gc.IsNil)
	c.Logf("size %d", len(data))
	if want := 200; len(data) > want {
		c.Fatalf("encoded macaroon bigger than expected; got %d want < %d", len(data), want)
	}
}

func (*ovenSuite) TestOpsStoredOnlyOnce(c *gc.C) {
	store := bakery.NewMemOpsStore()
	oven := bakery.NewOven(bakery.OvenParams{
		OpsStore: store,
	})

	ops := []bakery.Op{{"one", "read"}, {"one", "write"}, {"two", "read"}}

	m, err := oven.NewMacaroon(testContext, macaroon.LatestVersion, ages, nil, ops...)
	c.Assert(err, gc.IsNil)
	gotOps, _, err := oven.MacaroonOps(testContext, macaroon.Slice{m})
	c.Assert(err, gc.IsNil)

	c.Assert(bakery.CanonicalOps(gotOps), jc.DeepEquals, bakery.CanonicalOps(ops))

	// Make another macaroon containing the same ops in a different order.
	ops = []bakery.Op{{"one", "write"}, {"one", "read"}, {"one", "read"}, {"two", "read"}}
	_, err = oven.NewMacaroon(testContext, macaroon.LatestVersion, ages, nil, ops...)
	c.Assert(err, gc.IsNil)

	c.Assert(bakery.MemOpsStoreLen(store), gc.Equals, 1)
}
