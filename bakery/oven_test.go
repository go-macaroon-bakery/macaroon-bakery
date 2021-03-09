package bakery_test

import (
	"testing"

	qt "github.com/frankban/quicktest"
	"gopkg.in/macaroon.v2"

	"github.com/go-macaroon-bakery/macaroon-bakery/v3/bakery"
)

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

func TestCanonicalOps(t *testing.T) {
	c := qt.New(t)
	for i, test := range canonicalOpsTests {
		c.Logf("test %d: %v", i, test.about)
		ops := append([]bakery.Op(nil), test.ops...)
		c.Assert(bakery.CanonicalOps(ops), qt.DeepEquals, test.expect)
		// Verify that the original slice isn't changed.
		c.Assert(ops, qt.DeepEquals, test.ops)
	}
}

func TestMultipleOps(t *testing.T) {
	c := qt.New(t)
	oven := bakery.NewOven(bakery.OvenParams{})
	ops := []bakery.Op{{"one", "read"}, {"one", "write"}, {"two", "read"}}
	m, err := oven.NewMacaroon(testContext, bakery.LatestVersion, nil, ops...)
	c.Assert(err, qt.IsNil)
	gotOps, conds, err := oven.VerifyMacaroon(testContext, macaroon.Slice{m.M()})
	c.Assert(err, qt.IsNil)
	c.Assert(conds, qt.HasLen, 0)
	c.Assert(bakery.CanonicalOps(gotOps), qt.DeepEquals, ops)
}

func TestMultipleOpsInId(t *testing.T) {
	c := qt.New(t)
	oven := bakery.NewOven(bakery.OvenParams{})

	ops := []bakery.Op{{"one", "read"}, {"one", "write"}, {"two", "read"}}
	m, err := oven.NewMacaroon(testContext, bakery.LatestVersion, nil, ops...)
	c.Assert(err, qt.IsNil)
	gotOps, conds, err := oven.VerifyMacaroon(testContext, macaroon.Slice{m.M()})
	c.Assert(err, qt.IsNil)
	c.Assert(conds, qt.HasLen, 0)
	c.Assert(bakery.CanonicalOps(gotOps), qt.DeepEquals, ops)
}

func TestMultipleOpsInIdWithVersion1(t *testing.T) {
	c := qt.New(t)
	oven := bakery.NewOven(bakery.OvenParams{})

	ops := []bakery.Op{{"one", "read"}, {"one", "write"}, {"two", "read"}}
	m, err := oven.NewMacaroon(testContext, bakery.Version1, nil, ops...)
	c.Assert(err, qt.IsNil)
	gotOps, conds, err := oven.VerifyMacaroon(testContext, macaroon.Slice{m.M()})
	c.Assert(err, qt.IsNil)
	c.Assert(conds, qt.HasLen, 0)
	c.Assert(bakery.CanonicalOps(gotOps), qt.DeepEquals, ops)
}
