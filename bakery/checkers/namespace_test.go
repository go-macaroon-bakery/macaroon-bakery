package checkers_test

import (
	"testing"

	qt "github.com/frankban/quicktest"

	"github.com/go-macaroon-bakery/macaroon-bakery/v3/bakery/checkers"
)

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

func TestResolve(t *testing.T) {
	c := qt.New(t)
	for i, test := range resolveTests {
		c.Logf("test %d: %s", i, test.about)
		prefix, ok := test.ns.Resolve(test.uri)
		c.Check(ok, qt.Equals, test.expectOK)
		c.Check(prefix, qt.Equals, test.expectPrefix)
	}
}

func TestRegister(t *testing.T) {
	c := qt.New(t)
	ns := checkers.NewNamespace(nil)
	ns.Register("testns", "t")
	prefix, ok := ns.Resolve("testns")
	c.Assert(prefix, qt.Equals, "t")
	c.Assert(ok, qt.Equals, true)

	ns.Register("other", "o")
	prefix, ok = ns.Resolve("other")
	c.Assert(prefix, qt.Equals, "o")
	c.Assert(ok, qt.Equals, true)

	// If we re-register the same URL, it does nothing.
	ns.Register("other", "p")
	prefix, ok = ns.Resolve("other")
	c.Assert(prefix, qt.Equals, "o")
	c.Assert(ok, qt.Equals, true)
}

var namespaceEqualTests = []struct {
	about    string
	ns1, ns2 *checkers.Namespace
	expect   bool
}{{
	about:  "both nil",
	expect: true,
}, {
	about:  "ns1 nil",
	ns2:    checkers.NewNamespace(nil),
	expect: false,
}, {
	about:  "ns2 nil",
	ns1:    checkers.NewNamespace(nil),
	expect: false,
}, {
	about:  "different lengths",
	ns1:    checkers.NewNamespace(map[string]string{"testns": "t", "otherns": "t"}),
	ns2:    checkers.NewNamespace(map[string]string{"testns": "t"}),
	expect: false,
}, {
	about:  "all same",
	ns1:    checkers.NewNamespace(map[string]string{"testns": "t", "otherns": "t"}),
	ns2:    checkers.NewNamespace(map[string]string{"testns": "t", "otherns": "t"}),
	expect: true,
}, {
	about:  "different contents",
	ns1:    checkers.NewNamespace(map[string]string{"testns": "t", "otherns": "t"}),
	ns2:    checkers.NewNamespace(map[string]string{"testns": "t1", "otherns": "t"}),
	expect: false,
}}

func TestEqual(t *testing.T) {
	c := qt.New(t)
	for i, test := range namespaceEqualTests {
		c.Logf("test %d: %s", i, test.about)
		c.Assert(test.ns1.Equal(test.ns2), qt.Equals, test.expect)
	}
}

func TestRegisterBadURI(t *testing.T) {
	c := qt.New(t)
	ns := checkers.NewNamespace(nil)
	c.Assert(func() {
		ns.Register("", "x")
	}, qt.PanicMatches, `cannot register invalid URI "" \(prefix "x"\)`)
}

func TestRegisterBadPrefix(t *testing.T) {
	c := qt.New(t)
	ns := checkers.NewNamespace(nil)
	c.Assert(func() {
		ns.Register("std", "x:1")
	}, qt.PanicMatches, `cannot register invalid prefix "x:1" for URI "std"`)
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

func TestResolveCaveatWithNamespace(t *testing.T) {
	c := qt.New(t)
	for i, test := range resolveCaveatTests {
		c.Logf("test %d: %s", i, test.about)
		ns := checkers.NewNamespace(test.ns)
		c.Assert(ns.ResolveCaveat(test.caveat), qt.DeepEquals, test.expect)
	}
}

var namespaceMarshalTests = []struct {
	about  string
	ns     map[string]string
	expect string
}{{
	about: "empty namespace",
}, {
	about: "standard namespace",
	ns: map[string]string{
		"std": "",
	},
	expect: "std:",
}, {
	about: "several elements",
	ns: map[string]string{
		"std":              "",
		"http://blah.blah": "blah",
		"one":              "two",
		"foo.com/x.v0.1":   "z",
	},
	expect: "foo.com/x.v0.1:z http://blah.blah:blah one:two std:",
}, {
	about: "sort by URI not by field",
	ns: map[string]string{
		"a":  "one",
		"a1": "two", // Note that '1' < ':'
	},
	expect: "a:one a1:two",
}}

func TestMarshal(t *testing.T) {
	c := qt.New(t)
	for i, test := range namespaceMarshalTests {
		c.Logf("test %d: %v", i, test.about)
		ns := checkers.NewNamespace(test.ns)
		data, err := ns.MarshalText()
		c.Assert(err, qt.Equals, nil)
		c.Assert(string(data), qt.Equals, test.expect)
		c.Assert(ns.String(), qt.Equals, test.expect)

		// Check that it can be unmarshaled to the same thing:
		var ns1 checkers.Namespace
		err = ns1.UnmarshalText(data)
		c.Assert(err, qt.Equals, nil)
		c.Assert(&ns1, qt.DeepEquals, ns)
	}
}

var namespaceUnmarshalTests = []struct {
	about       string
	text        string
	expect      map[string]string
	expectError string
}{{
	about: "empty text",
}, {
	about: "fields with extra space",
	text:  "   x:y \t\nz:\r",
	expect: map[string]string{
		"x": "y",
		"z": "",
	},
}, {
	about:       "field without colon",
	text:        "foo:x bar baz:g",
	expectError: `no colon in namespace field "bar"`,
}, {
	about:       "invalid URI",
	text:        "foo\xff:a",
	expectError: `invalid URI "foo\\xff" in namespace field "foo\\xff:a"`,
}, {
	about:       "empty URI",
	text:        "blah:x :b",
	expectError: `invalid URI "" in namespace field ":b"`,
}, {
	about:       "invalid prefix",
	text:        "p:\xff",
	expectError: `invalid prefix "\\xff" in namespace field "p:\\xff"`,
}, {
	about:       "duplicate URI",
	text:        "std: std:p",
	expectError: `duplicate URI "std" in namespace "std: std:p"`,
}}

func TestUnmarshal(t *testing.T) {
	c := qt.New(t)
	for i, test := range namespaceUnmarshalTests {
		c.Logf("test %d: %v", i, test.about)
		var ns checkers.Namespace
		err := ns.UnmarshalText([]byte(test.text))
		if test.expectError != "" {
			c.Assert(err, qt.ErrorMatches, test.expectError)
		} else {
			c.Assert(err, qt.Equals, nil)
			c.Assert(&ns, qt.DeepEquals, checkers.NewNamespace(test.expect))
		}
	}
}

func TestMarshalNil(t *testing.T) {
	c := qt.New(t)
	var ns *checkers.Namespace
	data, err := ns.MarshalText()
	c.Assert(err, qt.Equals, nil)
	c.Assert(data, qt.HasLen, 0)
}

var validTests = []struct {
	about  string
	test   func(string) bool
	s      string
	expect bool
}{{
	about:  "URI with schema",
	test:   checkers.IsValidSchemaURI,
	s:      "http://foo.com",
	expect: true,
}, {
	about: "URI with space",
	test:  checkers.IsValidSchemaURI,
	s:     "a\rb",
}, {
	about: "URI with unicode space",
	test:  checkers.IsValidSchemaURI,
	s:     "x\u2003y",
}, {
	about: "empty URI",
	test:  checkers.IsValidSchemaURI,
}, {
	about: "URI with invalid UTF-8",
	test:  checkers.IsValidSchemaURI,
	s:     "\xff",
}, {
	about: "prefix with colon",
	test:  checkers.IsValidPrefix,
	s:     "x:y",
}, {
	about: "prefix with space",
	test:  checkers.IsValidPrefix,
	s:     "x y",
}, {
	about: "prefix with unicode space",
	test:  checkers.IsValidPrefix,
	s:     "\u3000",
}, {
	about:  "empty prefix",
	test:   checkers.IsValidPrefix,
	expect: true,
}}

func TestValid(t *testing.T) {
	c := qt.New(t)
	for i, test := range validTests {
		c.Check(test.test(test.s), qt.Equals, test.expect, qt.Commentf("test %d: %s", i, test.about))
	}
}
