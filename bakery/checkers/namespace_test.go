package checkers_test

import (
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
)

type NamespaceSuite struct{}

var _ = gc.Suite(&NamespaceSuite{})

var resolveTests = []struct {
	about        string
	ns           *checkers.Namespace
	uri          string
	expectPrefix string
	expectOK     bool
}{{
	about:        "successful resolve",
	ns:           checkers.NewNamespace(map[string]string{"testns": "t"}),
	uri:          "testns",
	expectPrefix: "t",
	expectOK:     true,
}, {
	about: "unsuccessful resolve",
	ns:    checkers.NewNamespace(map[string]string{"testns": "t"}),
	uri:   "foo",
}, {
	about:        "several of the same prefix",
	ns:           checkers.NewNamespace(map[string]string{"testns": "t", "otherns": "t"}),
	uri:          "otherns",
	expectPrefix: "t",
	expectOK:     true,
}, {
	about: "resolve with nil Namespace",
	uri:   "testns",
}}

func (*NamespaceSuite) TestResolve(c *gc.C) {
	for i, test := range resolveTests {
		c.Logf("test %d: %s", i, test.about)
		prefix, ok := test.ns.Resolve(test.uri)
		c.Check(ok, gc.Equals, test.expectOK)
		c.Check(prefix, gc.Equals, test.expectPrefix)
	}
}

func (*NamespaceSuite) TestRegister(c *gc.C) {
	ns := checkers.NewNamespace(nil)
	ns.Register("testns", "t")
	prefix, ok := ns.Resolve("testns")
	c.Assert(prefix, gc.Equals, "t")
	c.Assert(ok, gc.Equals, true)

	ns.Register("other", "o")
	prefix, ok = ns.Resolve("other")
	c.Assert(prefix, gc.Equals, "o")
	c.Assert(ok, gc.Equals, true)

	// If we re-register the same URL, it does nothing.
	ns.Register("other", "p")
	prefix, ok = ns.Resolve("other")
	c.Assert(prefix, gc.Equals, "o")
	c.Assert(ok, gc.Equals, true)
}

var resolveCaveatTests = []struct {
	about  string
	ns     map[string]string
	caveat checkers.Caveat
	expect checkers.Caveat
}{{
	about: "no namespace",
	caveat: checkers.Caveat{
		Condition: "foo",
	},
	expect: checkers.Caveat{
		Condition: "foo",
	},
}, {
	about: "with registered namespace",
	ns: map[string]string{
		"testns": "t",
	},
	caveat: checkers.Caveat{
		Condition: "foo",
		Namespace: "testns",
	},
	expect: checkers.Caveat{
		Condition: "t:foo",
	},
}, {
	about: "with unregistered namespace",
	caveat: checkers.Caveat{
		Condition: "foo",
		Namespace: "testns",
	},
	expect: checkers.Caveat{
		Condition: `error caveat "foo" in unregistered namespace "testns"`,
		Namespace: checkers.StdNamespace,
	},
}, {
	about: "with empty prefix",
	ns: map[string]string{
		"testns": "",
	},
	caveat: checkers.Caveat{
		Condition: "foo",
		Namespace: "testns",
	},
	expect: checkers.Caveat{
		Condition: "foo",
	},
}}

func (*NamespaceSuite) TestResolveCaveatWithNamespace(c *gc.C) {
	for i, test := range resolveCaveatTests {
		c.Logf("test %d: %s", i, test.about)
		ns := checkers.NewNamespace(test.ns)
		c.Assert(ns.ResolveCaveat(test.caveat), jc.DeepEquals, test.expect)
	}
}
