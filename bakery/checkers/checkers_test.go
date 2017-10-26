package checkers_test

import (
	"fmt"
	"time"

	jc "github.com/juju/testing/checkers"
	"golang.org/x/net/context"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v2"

	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
)

type CheckersSuite struct{}

var _ = gc.Suite(&CheckersSuite{})

// A frozen time for the tests.
var now = time.Date(2006, time.January, 2, 15, 4, 5, int(123*time.Millisecond), time.UTC)

// testClock is a Clock implementation that always returns the above time.
type testClock struct{}

func (testClock) Now() time.Time {
	return now
}

// FirstPartyCaveatChecker is declared here so we can avoid a cyclic
// dependency on the bakery.
type FirstPartyCaveatChecker interface {
	CheckFirstPartyCaveat(ctx context.Context, caveat string) error
}

type checkTest struct {
	caveat      string
	expectError string
	expectCause func(err error) bool
}

var isCaveatNotRecognized = errgo.Is(checkers.ErrCaveatNotRecognized)

var checkerTests = []struct {
	about      string
	addContext func(context.Context, *checkers.Namespace) context.Context
	checks     []checkTest
}{{
	about: "nothing in context, no extra checkers",
	checks: []checkTest{{
		caveat:      "something",
		expectError: `caveat "something" not satisfied: caveat not recognized`,
		expectCause: isCaveatNotRecognized,
	}, {
		caveat:      "",
		expectError: `cannot parse caveat "": empty caveat`,
		expectCause: isCaveatNotRecognized,
	}, {
		caveat:      " hello",
		expectError: `cannot parse caveat " hello": caveat starts with space character`,
		expectCause: isCaveatNotRecognized,
	}},
}, {
	about: "one failed caveat",
	checks: []checkTest{{
		caveat: "t:a aval",
	}, {
		caveat: "t:b bval",
	}, {
		caveat:      "t:a wrong",
		expectError: `caveat "t:a wrong" not satisfied: wrong arg`,
		expectCause: errgo.Is(errWrongArg),
	}},
}, {
	about: "time from clock",
	addContext: func(ctx context.Context, _ *checkers.Namespace) context.Context {
		return checkers.ContextWithClock(ctx, testClock{})
	},
	checks: []checkTest{{
		caveat: checkers.TimeBeforeCaveat(now.Add(1)).Condition,
	}, {
		caveat:      checkers.TimeBeforeCaveat(now).Condition,
		expectError: `caveat "time-before 2006-01-02T15:04:05.123Z" not satisfied: macaroon has expired`,
	}, {
		caveat:      checkers.TimeBeforeCaveat(now.Add(-1)).Condition,
		expectError: `caveat "time-before 2006-01-02T15:04:05.122999999Z" not satisfied: macaroon has expired`,
	}, {
		caveat:      `time-before bad-date`,
		expectError: `caveat "time-before bad-date" not satisfied: parsing time "bad-date" as "2006-01-02T15:04:05.999999999Z07:00": cannot parse "bad-date" as "2006"`,
	}, {
		caveat:      checkers.TimeBeforeCaveat(now).Condition + " ",
		expectError: `caveat "time-before 2006-01-02T15:04:05.123Z " not satisfied: parsing time "2006-01-02T15:04:05.123Z ": extra text:  `,
	}},
}, {
	about: "real time",
	checks: []checkTest{{
		caveat:      checkers.TimeBeforeCaveat(time.Date(2010, time.January, 1, 0, 0, 0, 0, time.UTC)).Condition,
		expectError: `caveat "time-before 2010-01-01T00:00:00Z" not satisfied: macaroon has expired`,
	}, {
		caveat: checkers.TimeBeforeCaveat(time.Date(3000, time.January, 1, 0, 0, 0, 0, time.UTC)).Condition,
	}},
}, {
	about: "declared, no entries",
	checks: []checkTest{{
		caveat:      checkers.DeclaredCaveat("a", "aval").Condition,
		expectError: `caveat "declared a aval" not satisfied: got a=null, expected "aval"`,
	}, {
		caveat:      checkers.CondDeclared,
		expectError: `caveat "declared" not satisfied: declared caveat has no value`,
	}},
}, {
	about: "declared, some entries",
	addContext: func(ctx context.Context, ns *checkers.Namespace) context.Context {
		m, _ := macaroon.New([]byte("k"), []byte("id"), "", macaroon.LatestVersion)
		add := func(attr, val string) {
			cav := ns.ResolveCaveat(checkers.DeclaredCaveat(attr, val))
			err := m.AddFirstPartyCaveat([]byte(cav.Condition))
			if err != nil {
				panic(err)
			}
		}
		add("a", "aval")
		add("b", "bval")
		add("spc", " a b")
		return checkers.ContextWithMacaroons(ctx, ns, macaroon.Slice{m})
	},
	checks: []checkTest{{
		caveat: checkers.DeclaredCaveat("a", "aval").Condition,
	}, {
		caveat: checkers.DeclaredCaveat("b", "bval").Condition,
	}, {
		caveat: checkers.DeclaredCaveat("spc", " a b").Condition,
	}, {
		caveat:      checkers.DeclaredCaveat("a", "bval").Condition,
		expectError: `caveat "declared a bval" not satisfied: got a="aval", expected "bval"`,
	}, {
		caveat:      checkers.DeclaredCaveat("a", " aval").Condition,
		expectError: `caveat "declared a  aval" not satisfied: got a="aval", expected " aval"`,
	}, {
		caveat:      checkers.DeclaredCaveat("spc", "a b").Condition,
		expectError: `caveat "declared spc a b" not satisfied: got spc=" a b", expected "a b"`,
	}, {
		caveat:      checkers.DeclaredCaveat("", "a b").Condition,
		expectError: `caveat "error invalid caveat 'declared' key \\"\\"" not satisfied: bad caveat`,
	}, {
		caveat:      checkers.DeclaredCaveat("a b", "a b").Condition,
		expectError: `caveat "error invalid caveat 'declared' key \\"a b\\"" not satisfied: bad caveat`,
	}},
}, {
	about: "error caveat",
	checks: []checkTest{{
		caveat:      checkers.ErrorCaveatf("").Condition,
		expectError: `caveat "error" not satisfied: bad caveat`,
	}, {
		caveat:      checkers.ErrorCaveatf("something %d", 134).Condition,
		expectError: `caveat "error something 134" not satisfied: bad caveat`,
	}},
}}

var errWrongArg = errgo.New("wrong arg")

// argChecker returns a checker function that checks
// that the caveat condition is checkArg.
func argChecker(c *gc.C, expectCond, checkArg string) checkers.Func {
	return func(_ context.Context, cond, arg string) error {
		c.Assert(cond, gc.Equals, expectCond)
		if arg != checkArg {
			return errWrongArg
		}
		return nil
	}
}

func (s *CheckersSuite) TestCheckers(c *gc.C) {
	checker := checkers.New(nil)
	checker.Namespace().Register("testns", "t")
	checker.Register("a", "testns", argChecker(c, "t:a", "aval"))
	checker.Register("b", "testns", argChecker(c, "t:b", "bval"))
	for i, test := range checkerTests {
		c.Logf("test %d: %s", i, test.about)
		ctx := context.Background()
		if test.addContext != nil {
			ctx = test.addContext(ctx, checker.Namespace())
		}
		for j, check := range test.checks {
			c.Logf("\tcheck %d", j)
			err := checker.CheckFirstPartyCaveat(ctx, check.caveat)
			if check.expectError != "" {
				c.Assert(err, gc.ErrorMatches, check.expectError)
				if check.expectCause == nil {
					check.expectCause = errgo.Any
				}
				c.Assert(check.expectCause(errgo.Cause(err)), gc.Equals, true)
			} else {
				c.Assert(err, gc.IsNil)
			}
		}
	}
}

var inferDeclaredTests = []struct {
	about     string
	caveats   [][]checkers.Caveat
	expect    map[string]string
	namespace map[string]string
}{{
	about:  "no macaroons",
	expect: map[string]string{},
}, {
	about: "single macaroon with one declaration",
	caveats: [][]checkers.Caveat{{{
		Condition: "declared foo bar",
	}}},
	expect: map[string]string{
		"foo": "bar",
	},
}, {
	about: "only one argument to declared",
	caveats: [][]checkers.Caveat{{{
		Condition: "declared foo",
	}}},
	expect: map[string]string{},
}, {
	about: "spaces in value",
	caveats: [][]checkers.Caveat{{{
		Condition: "declared foo bar bloggs",
	}}},
	expect: map[string]string{
		"foo": "bar bloggs",
	},
}, {
	about: "attribute with declared prefix",
	caveats: [][]checkers.Caveat{{{
		Condition: "declaredccf foo",
	}}},
	expect: map[string]string{},
}, {
	about: "several macaroons with different declares",
	caveats: [][]checkers.Caveat{{
		checkers.DeclaredCaveat("a", "aval"),
		checkers.DeclaredCaveat("b", "bval"),
	}, {
		checkers.DeclaredCaveat("c", "cval"),
		checkers.DeclaredCaveat("d", "dval"),
	}},
	expect: map[string]string{
		"a": "aval",
		"b": "bval",
		"c": "cval",
		"d": "dval",
	},
}, {
	about: "duplicate values",
	caveats: [][]checkers.Caveat{{
		checkers.DeclaredCaveat("a", "aval"),
		checkers.DeclaredCaveat("a", "aval"),
		checkers.DeclaredCaveat("b", "bval"),
	}, {
		checkers.DeclaredCaveat("a", "aval"),
		checkers.DeclaredCaveat("b", "bval"),
		checkers.DeclaredCaveat("c", "cval"),
		checkers.DeclaredCaveat("d", "dval"),
	}},
	expect: map[string]string{
		"a": "aval",
		"b": "bval",
		"c": "cval",
		"d": "dval",
	},
}, {
	about: "conflicting values",
	caveats: [][]checkers.Caveat{{
		checkers.DeclaredCaveat("a", "aval"),
		checkers.DeclaredCaveat("a", "conflict"),
		checkers.DeclaredCaveat("b", "bval"),
	}, {
		checkers.DeclaredCaveat("a", "conflict"),
		checkers.DeclaredCaveat("b", "another conflict"),
		checkers.DeclaredCaveat("c", "cval"),
		checkers.DeclaredCaveat("d", "dval"),
	}},
	expect: map[string]string{
		"c": "cval",
		"d": "dval",
	},
}, {
	about: "third party caveats ignored",
	caveats: [][]checkers.Caveat{{{
		Condition: "declared a no conflict",
		Location:  "location",
	},
		checkers.DeclaredCaveat("a", "aval"),
	}},
	expect: map[string]string{
		"a": "aval",
	},
}, {
	about: "unparseable caveats ignored",
	caveats: [][]checkers.Caveat{{{
		Condition: " bad",
	},
		checkers.DeclaredCaveat("a", "aval"),
	}},
	expect: map[string]string{
		"a": "aval",
	},
}, {
	about: "infer with namespace",
	namespace: map[string]string{
		checkers.StdNamespace: "",
		"testns":              "t",
	},
	caveats: [][]checkers.Caveat{{
		checkers.DeclaredCaveat("a", "aval"),
		// A declared caveat from a different namespace doesn't
		// interfere.
		caveatWithNamespace(checkers.DeclaredCaveat("a", "bval"), "testns"),
	}},
	expect: map[string]string{
		"a": "aval",
	},
}}

func caveatWithNamespace(cav checkers.Caveat, uri string) checkers.Caveat {
	cav.Namespace = uri
	return cav
}

func (*CheckersSuite) TestInferDeclared(c *gc.C) {
	for i, test := range inferDeclaredTests {
		if test.namespace == nil {
			test.namespace = map[string]string{
				checkers.StdNamespace: "",
			}
		}
		ns := checkers.NewNamespace(test.namespace)
		c.Logf("test %d: %s", i, test.about)
		ms := make(macaroon.Slice, len(test.caveats))
		for i, caveats := range test.caveats {
			m, err := macaroon.New(nil, []byte(fmt.Sprint(i)), "", macaroon.LatestVersion)
			c.Assert(err, gc.IsNil)
			for _, cav := range caveats {
				cav = ns.ResolveCaveat(cav)
				if cav.Location == "" {
					m.AddFirstPartyCaveat([]byte(cav.Condition))
				} else {
					m.AddThirdPartyCaveat(nil, []byte(cav.Condition), cav.Location)
				}
			}
			ms[i] = m
		}
		c.Assert(checkers.InferDeclared(nil, ms), jc.DeepEquals, test.expect)
	}
}

func (*CheckersSuite) TestRegisterNilFuncPanics(c *gc.C) {
	checker := checkers.New(nil)
	c.Assert(func() {
		checker.Register("x", checkers.StdNamespace, nil)
	}, gc.PanicMatches, `nil check function registered for namespace ".*" when registering condition "x"`)
}

func (*CheckersSuite) TestRegisterNoRegisteredNamespace(c *gc.C) {
	checker := checkers.New(nil)
	c.Assert(func() {
		checker.Register("x", "testns", succeed)
	}, gc.PanicMatches, `no prefix registered for namespace "testns" when registering condition "x"`)
}

func (*CheckersSuite) TestRegisterEmptyPrefixConditionWithColon(c *gc.C) {
	checker := checkers.New(nil)
	checker.Namespace().Register("testns", "")
	c.Assert(func() {
		checker.Register("x:y", "testns", succeed)
	}, gc.PanicMatches, `caveat condition "x:y" in namespace "testns" contains a colon but its prefix is empty`)
}

func (*CheckersSuite) TestRegisterTwiceSameNamespace(c *gc.C) {
	checker := checkers.New(nil)
	checker.Namespace().Register("testns", "t")
	checker.Register("x", "testns", succeed)
	c.Assert(func() {
		checker.Register("x", "testns", succeed)
	}, gc.PanicMatches, `checker for "t:x" \(namespace "testns"\) already registered in namespace "testns"`)
}

func (*CheckersSuite) TestRegisterTwiceDifferentNamespace(c *gc.C) {
	checker := checkers.New(nil)
	checker.Namespace().Register("testns", "t")
	checker.Namespace().Register("otherns", "t")
	checker.Register("x", "testns", succeed)
	c.Assert(func() {
		checker.Register("x", "otherns", succeed)
	}, gc.PanicMatches, `checker for "t:x" \(namespace "otherns"\) already registered in namespace "testns"`)
}

func (*CheckersSuite) TestCheckerInfo(c *gc.C) {
	checker := checkers.NewEmpty(nil)
	checker.Namespace().Register("one", "t")
	checker.Namespace().Register("two", "t")
	checker.Namespace().Register("three", "")
	checker.Namespace().Register("four", "s")

	var calledVal string
	register := func(name, ns string) {
		checker.Register(name, ns, func(ctx context.Context, cond, arg string) error {
			calledVal = name + " " + ns
			return nil
		})
	}
	register("x", "one")
	register("y", "one")
	register("z", "two")
	register("a", "two")
	register("something", "three")
	register("other", "three")
	register("xxx", "four")

	expect := []checkers.CheckerInfo{{
		Namespace: "four",
		Name:      "xxx",
		Prefix:    "s",
	}, {
		Namespace: "one",
		Name:      "x",
		Prefix:    "t",
	}, {
		Namespace: "one",
		Name:      "y",
		Prefix:    "t",
	}, {
		Namespace: "three",
		Name:      "other",
		Prefix:    "",
	}, {
		Namespace: "three",
		Name:      "something",
		Prefix:    "",
	}, {
		Namespace: "two",
		Name:      "a",
		Prefix:    "t",
	}, {
		Namespace: "two",
		Name:      "z",
		Prefix:    "t",
	}}
	infos := checker.Info()
	// We can't use DeepEqual on functions so check that the right functions are
	// there by calling them, then set them to nil.
	c.Assert(infos, gc.HasLen, len(expect))
	for i := range infos {
		info := &infos[i]
		calledVal = ""
		info.Check(nil, "", "")
		c.Check(calledVal, gc.Equals, expect[i].Name+" "+expect[i].Namespace, gc.Commentf("index %d", i))
		info.Check = nil
	}
	c.Assert(infos, jc.DeepEquals, expect)
}

func succeed(ctx context.Context, cond, arg string) error {
	return nil
}
