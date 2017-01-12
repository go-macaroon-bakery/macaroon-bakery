package checkers_test

import (
	"time"

	jc "github.com/juju/testing/checkers"
	"golang.org/x/net/context"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
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
	CheckFirstPartyCaveat(ctxt context.Context, caveat string) error
}

type checkTest struct {
	caveat      string
	expectError string
	expectCause func(err error) bool
}

var isCaveatNotRecognized = errgo.Is(checkers.ErrCaveatNotRecognized)

var checkerTests = []struct {
	about      string
	addContext func(context.Context) context.Context
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
	addContext: func(ctxt context.Context) context.Context {
		return checkers.ContextWithClock(ctxt, testClock{})
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
		caveat:      "t:declared-foo aval",
		expectError: `caveat "t:declared-foo aval" not satisfied: got "aval", expected ""`,
	}, {
		caveat: "t:declared-foo",
	}, {
		caveat: "t:declared-foo ",
	}},
}, {
	about: "declared, some entries",
	addContext: func(ctxt context.Context) context.Context {
		return checkers.ContextWithDeclared(ctxt, checkers.Declared{
			Condition: "t:declared-foo",
			Value:     "aval",
		})
	},
	checks: []checkTest{{
		caveat: "t:declared-foo aval",
	}, {
		caveat:      "t:declared-foo bval",
		expectError: `caveat "t:declared-foo bval" not satisfied: got "bval", expected "aval"`,
	}, {
		caveat:      "t:declared-foo  aval",
		expectError: `caveat "t:declared-foo  aval" not satisfied: got " aval", expected "aval"`,
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
	checkers.RegisterDeclaredCaveat(checker, "declared-foo", "testns")
	for i, test := range checkerTests {
		c.Logf("test %d: %s", i, test.about)
		ctxt := context.Background()
		if test.addContext != nil {
			ctxt = test.addContext(ctxt)
		}
		for j, check := range test.checks {
			c.Logf("\tcheck %d", j)
			err := checker.CheckFirstPartyCaveat(ctxt, check.caveat)
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
	about      string
	declCond   string
	conditions []string
	expect     checkers.Declared
}{{
	about:    "no conditions",
	declCond: "declared-foo",
	expect: checkers.Declared{
		Condition: "declared-foo",
	},
}, {
	about:      "single macaroon with one declaration, empty prefix",
	conditions: []string{"declared-foo bar"},
	declCond:   "declared-foo",
	expect: checkers.Declared{
		Condition: "declared-foo",
		Value:     "bar",
	},
}, {
	about:      "spaces in value",
	conditions: []string{"declared-foo foo bar"},
	declCond:   "declared-foo",
	expect: checkers.Declared{
		Condition: "declared-foo",
		Value:     "foo bar",
	},
}, {
	about:      "condition with declared prefix",
	declCond:   "declared-foo",
	conditions: []string{"declared-fooccf foo"},
	expect: checkers.Declared{
		Condition: "declared-foo",
	},
}, {
	about:      "condition with no arguments",
	declCond:   "declared-foo",
	conditions: []string{"declared-fooccf foo"},
	expect: checkers.Declared{
		Condition: "declared-foo",
	},
}, {
	about: "several different caveats",
	conditions: []string{
		"declared-foo a",
		"bar b",
		"x y",
	},
	declCond: "declared-foo",
	expect: checkers.Declared{
		Condition: "declared-foo",
		Value:     "a",
	},
}, {
	about: "duplicate values",
	conditions: []string{
		"declared-foo a",
		"declared-foo a",
	},
	declCond: "declared-foo",
	expect: checkers.Declared{
		Condition: "declared-foo",
		Value:     "a",
	},
}, {
	about: "one empty, one not",
	conditions: []string{
		"declared-foo aval",
		"declared-foo",
	},
	declCond: "declared-foo",
	expect: checkers.Declared{
		Condition: "declared-foo",
	},
}, {
	about: "conflicting values",
	conditions: []string{
		"declared-foo aval",
		"declared-foo bval",
	},
	declCond: "declared-foo",
	expect: checkers.Declared{
		Condition: "declared-foo",
	},
}, {
	about:      "unparseable caveats ignored",
	conditions: []string{" bad", "a aval"},
	declCond:   "a",
	expect: checkers.Declared{
		Condition: "a",
		Value:     "aval",
	},
}}

func (*CheckersSuite) TestInferDeclared(c *gc.C) {
	for i, test := range inferDeclaredTests {
		c.Logf("test %d: %s", i, test.about)
		c.Assert(checkers.InferDeclared(test.declCond, test.conditions), gc.Equals, test.expect)
	}
}

var operationsCheckerTests = []struct {
	about       string
	caveat      checkers.Caveat
	ops         []string
	expectError string
}{{
	about:  "all allowed",
	caveat: checkers.AllowCaveat("op1", "op2", "op4", "op3"),
	ops:    []string{"op1", "op3", "op2"},
}, {
	about:  "none denied",
	caveat: checkers.DenyCaveat("op1", "op2"),
	ops:    []string{"op3", "op4"},
}, {
	about:       "one not allowed",
	caveat:      checkers.AllowCaveat("op1", "op2"),
	ops:         []string{"op1", "op3"},
	expectError: `op3 not allowed`,
}, {
	about:       "one denied",
	caveat:      checkers.DenyCaveat("op1", "op2"),
	ops:         []string{"op4", "op5", "op2"},
	expectError: `op2 not allowed`,
}, {
	about:       "no operations, allow caveat",
	caveat:      checkers.AllowCaveat("op1"),
	ops:         []string{},
	expectError: `op1 not allowed`,
}, {
	about:  "no operations, deny caveat",
	caveat: checkers.DenyCaveat("op1"),
	ops:    []string{},
}, {
	about: "no operations, empty allow caveat",
	caveat: checkers.Caveat{
		Condition: checkers.CondAllow,
	},
	ops:         []string{},
	expectError: `no operations allowed`,
}}

func (*CheckersSuite) TestOperationsChecker(c *gc.C) {
	checker := checkers.New(nil)
	for i, test := range operationsCheckerTests {
		c.Logf("%d: %s", i, test.about)
		ctxt := checkers.ContextWithOperations(context.Background(), test.ops...)
		err := checker.CheckFirstPartyCaveat(ctxt, test.caveat.Condition)
		if test.expectError == "" {
			c.Assert(err, gc.IsNil)
			continue
		}
		c.Assert(err, gc.ErrorMatches, ".*: "+test.expectError)
	}
}

var operationErrorCaveatTests = []struct {
	about           string
	caveat          checkers.Caveat
	expectCondition string
}{{
	about:           "empty allow",
	caveat:          checkers.AllowCaveat(),
	expectCondition: "error no operations allowed",
}, {
	about:           "allow: invalid operation name",
	caveat:          checkers.AllowCaveat("op1", "operation number 2"),
	expectCondition: `error invalid operation name "operation number 2"`,
}, {
	about:           "deny: invalid operation name",
	caveat:          checkers.DenyCaveat("op1", "operation number 2"),
	expectCondition: `error invalid operation name "operation number 2"`,
}}

func (*CheckersSuite) TestOperationErrorCaveatTest(c *gc.C) {
	for i, test := range operationErrorCaveatTests {
		c.Logf("%d: %s", i, test.about)
		c.Assert(test.caveat.Condition, gc.Matches, test.expectCondition)
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
		checker.Register(name, ns, func(ctxt context.Context, cond, arg string) error {
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

func succeed(ctxt context.Context, cond, arg string) error {
	return nil
}
