package checkers_test

import (
	"fmt"
	"net"
	"time"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v1"

	"gopkg.in/macaroon-bakery.v1/bakery"
	"gopkg.in/macaroon-bakery.v1/bakery/checkers"
)

type CheckersSuite struct{}

var _ = gc.Suite(&CheckersSuite{})

// Freeze time for the tests.
var now = func() time.Time {
	now, err := time.Parse(time.RFC3339Nano, "2006-01-02T15:04:05.123Z")
	if err != nil {
		panic(err)
	}
	*checkers.TimeNow = func() time.Time {
		return now
	}
	return now
}()

type checkTest struct {
	caveat      string
	expectError string
	expectCause func(err error) bool
}

var isCaveatNotRecognized = errgo.Is(checkers.ErrCaveatNotRecognized)

var checkerTests = []struct {
	about   string
	checker bakery.FirstPartyChecker
	checks  []checkTest
}{{
	about:   "empty MultiChecker",
	checker: checkers.New(),
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
	about: "MultiChecker with some values",
	checker: checkers.New(
		argChecker("a", "aval"),
		argChecker("b", "bval"),
	),
	checks: []checkTest{{
		caveat: "a aval",
	}, {
		caveat: "b bval",
	}, {
		caveat:      "a wrong",
		expectError: `caveat "a wrong" not satisfied: wrong arg`,
		expectCause: errgo.Is(errWrongArg),
	}},
}, {
	about: "MultiChecker with several of the same condition",
	checker: checkers.New(
		argChecker("a", "aval"),
		argChecker("a", "bval"),
	),
	checks: []checkTest{{
		caveat:      "a aval",
		expectError: `caveat "a aval" not satisfied: wrong arg`,
		expectCause: errgo.Is(errWrongArg),
	}, {
		caveat:      "a bval",
		expectError: `caveat "a bval" not satisfied: wrong arg`,
		expectCause: errgo.Is(errWrongArg),
	}},
}, {
	about: "nested MultiChecker",
	checker: checkers.New(
		argChecker("a", "aval"),
		argChecker("b", "bval"),
		checkers.New(
			argChecker("c", "cval"),
			checkers.New(
				argChecker("d", "dval"),
			),
			argChecker("e", "eval"),
		),
	),
	checks: []checkTest{{
		caveat: "a aval",
	}, {
		caveat: "b bval",
	}, {
		caveat: "c cval",
	}, {
		caveat: "d dval",
	}, {
		caveat: "e eval",
	}, {
		caveat:      "a wrong",
		expectError: `caveat "a wrong" not satisfied: wrong arg`,
		expectCause: errgo.Is(errWrongArg),
	}, {
		caveat:      "c wrong",
		expectError: `caveat "c wrong" not satisfied: wrong arg`,
		expectCause: errgo.Is(errWrongArg),
	}, {
		caveat:      "d wrong",
		expectError: `caveat "d wrong" not satisfied: wrong arg`,
		expectCause: errgo.Is(errWrongArg),
	}, {
		caveat:      "f something",
		expectError: `caveat "f something" not satisfied: caveat not recognized`,
		expectCause: isCaveatNotRecognized,
	}},
}, {
	about: "Map with no items",
	checker: checkers.New(
		checkers.Map{},
	),
	checks: []checkTest{{
		caveat:      "a aval",
		expectError: `caveat "a aval" not satisfied: caveat not recognized`,
		expectCause: isCaveatNotRecognized,
	}},
}, {
	about: "Map with some values",
	checker: checkers.New(
		checkers.Map{
			"a": argChecker("a", "aval").Check,
			"b": argChecker("b", "bval").Check,
		},
	),
	checks: []checkTest{{
		caveat: "a aval",
	}, {
		caveat: "b bval",
	}, {
		caveat:      "a wrong",
		expectError: `caveat "a wrong" not satisfied: wrong arg`,
		expectCause: errgo.Is(errWrongArg),
	}, {
		caveat:      "b wrong",
		expectError: `caveat "b wrong" not satisfied: wrong arg`,
		expectCause: errgo.Is(errWrongArg),
	}},
}, {
	about: "time within limit",
	checker: checkers.New(
		checkers.TimeBefore,
	),
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
	about:   "declared, no entries",
	checker: checkers.New(checkers.Declared{}),
	checks: []checkTest{{
		caveat:      checkers.DeclaredCaveat("a", "aval").Condition,
		expectError: `caveat "declared a aval" not satisfied: got a=null, expected "aval"`,
	}, {
		caveat:      checkers.CondDeclared,
		expectError: `caveat "declared" not satisfied: declared caveat has no value`,
	}},
}, {
	about: "declared, some entries",
	checker: checkers.New(checkers.Declared{
		"a":   "aval",
		"b":   "bval",
		"spc": " a b",
	}),
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
	about:   "error caveat",
	checker: checkers.New(),
	checks: []checkTest{{
		caveat:      checkers.ErrorCaveatf("").Condition,
		expectError: `caveat "error " not satisfied: bad caveat`,
	}, {
		caveat:      checkers.ErrorCaveatf("something %d", 134).Condition,
		expectError: `caveat "error something 134" not satisfied: bad caveat`,
	}},
}, {
	about:   "error caveat overrides other",
	checker: checkers.New(argChecker("error", "something")),
	checks: []checkTest{{
		caveat:      checkers.ErrorCaveatf("something").Condition,
		expectError: `caveat "error something" not satisfied: bad caveat`,
	}},
}}

var errWrongArg = errgo.New("wrong arg")

func argChecker(expectCond, checkArg string) checkers.Checker {
	return checkers.CheckerFunc{
		Condition_: expectCond,
		Check_: func(cond, arg string) error {
			if cond != expectCond {
				panic(fmt.Errorf("got condition %q want %q", cond, expectCond))
			}
			if arg != checkArg {
				return errWrongArg
			}
			return nil
		},
	}
}

func (s *CheckersSuite) TestCheckers(c *gc.C) {
	for i, test := range checkerTests {
		c.Logf("test %d: %s", i, test.about)
		for j, check := range test.checks {
			c.Logf("\tcheck %d", j)
			err := test.checker.CheckFirstPartyCaveat(check.caveat)
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

func (s *CheckersSuite) TestClientIPAddrCaveat(c *gc.C) {
	cav := checkers.ClientIPAddrCaveat(net.IP{127, 0, 0, 1})
	c.Assert(cav, gc.Equals, checkers.Caveat{
		Condition: "client-ip-addr 127.0.0.1",
	})
	cav = checkers.ClientIPAddrCaveat(net.ParseIP("2001:4860:0:2001::68"))
	c.Assert(cav, gc.Equals, checkers.Caveat{
		Condition: "client-ip-addr 2001:4860:0:2001::68",
	})
	cav = checkers.ClientIPAddrCaveat(nil)
	c.Assert(cav, gc.Equals, checkers.Caveat{
		Condition: "error bad IP address []",
	})
	cav = checkers.ClientIPAddrCaveat(net.IP{123, 3})
	c.Assert(cav, gc.Equals, checkers.Caveat{
		Condition: "error bad IP address [123 3]",
	})
}

func (s *CheckersSuite) TestClientOriginCaveat(c *gc.C) {
	cav := checkers.ClientOriginCaveat("")
	c.Assert(cav, gc.Equals, checkers.Caveat{
		Condition: "origin ",
	})
	cav = checkers.ClientOriginCaveat("somewhere")
	c.Assert(cav, gc.Equals, checkers.Caveat{
		Condition: "origin somewhere",
	})
}

var inferDeclaredTests = []struct {
	about   string
	caveats [][]checkers.Caveat
	expect  checkers.Declared
}{{
	about:  "no macaroons",
	expect: checkers.Declared{},
}, {
	about: "single macaroon with one declaration",
	caveats: [][]checkers.Caveat{{{
		Condition: "declared foo bar",
	}}},
	expect: checkers.Declared{
		"foo": "bar",
	},
}, {
	about: "only one argument to declared",
	caveats: [][]checkers.Caveat{{{
		Condition: "declared foo",
	}}},
	expect: checkers.Declared{},
}, {
	about: "spaces in value",
	caveats: [][]checkers.Caveat{{{
		Condition: "declared foo bar bloggs",
	}}},
	expect: checkers.Declared{
		"foo": "bar bloggs",
	},
}, {
	about: "attribute with declared prefix",
	caveats: [][]checkers.Caveat{{{
		Condition: "declaredccf foo",
	}}},
	expect: checkers.Declared{},
}, {
	about: "several macaroons with different declares",
	caveats: [][]checkers.Caveat{{
		checkers.DeclaredCaveat("a", "aval"),
		checkers.DeclaredCaveat("b", "bval"),
	}, {
		checkers.DeclaredCaveat("c", "cval"),
		checkers.DeclaredCaveat("d", "dval"),
	}},
	expect: checkers.Declared{
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
	expect: checkers.Declared{
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
	expect: checkers.Declared{
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
	expect: checkers.Declared{
		"a": "aval",
	},
}, {
	about: "unparseable caveats ignored",
	caveats: [][]checkers.Caveat{{{
		Condition: " bad",
	},
		checkers.DeclaredCaveat("a", "aval"),
	}},
	expect: checkers.Declared{
		"a": "aval",
	},
}}

func (*CheckersSuite) TestInferDeclared(c *gc.C) {
	for i, test := range inferDeclaredTests {
		c.Logf("test %d: %s", i, test.about)
		ms := make(macaroon.Slice, len(test.caveats))
		for i, caveats := range test.caveats {
			m, err := macaroon.New(nil, fmt.Sprint(i), "")
			c.Assert(err, gc.IsNil)
			for _, cav := range caveats {
				if cav.Location == "" {
					m.AddFirstPartyCaveat(cav.Condition)
				} else {
					m.AddThirdPartyCaveat(nil, cav.Condition, cav.Location)
				}
			}
			ms[i] = m
		}
		c.Assert(checkers.InferDeclared(ms), jc.DeepEquals, test.expect)
	}
}

var operationCheckerTests = []struct {
	about       string
	caveat      checkers.Caveat
	oc          checkers.OperationChecker
	expectError string
}{{
	about:  "allowed operation",
	caveat: checkers.AllowCaveat("op1", "op2", "op3"),
	oc:     checkers.OperationChecker("op1"),
}, {
	about:  "not denied oc",
	caveat: checkers.DenyCaveat("op1", "op2", "op3"),
	oc:     checkers.OperationChecker("op4"),
}, {
	about:       "not allowed oc",
	caveat:      checkers.AllowCaveat("op1", "op2", "op3"),
	oc:          checkers.OperationChecker("op4"),
	expectError: "op4 not allowed",
}, {
	about:       "denied oc",
	caveat:      checkers.DenyCaveat("op1", "op2", "op3"),
	oc:          checkers.OperationChecker("op1"),
	expectError: "op1 not allowed",
}, {
	about:       "unrecognised caveat",
	caveat:      checkers.ErrorCaveatf("unrecognized"),
	oc:          checkers.OperationChecker("op1"),
	expectError: "caveat not recognized",
}, {
	about:  "empty deny caveat",
	caveat: checkers.DenyCaveat(),
	oc:     checkers.OperationChecker("op1"),
}}

func (*CheckersSuite) TestOperationChecker(c *gc.C) {
	for i, test := range operationCheckerTests {
		c.Logf("%d: %s", i, test.about)
		cond, arg, err := checkers.ParseCaveat(test.caveat.Condition)
		c.Assert(err, gc.IsNil)
		c.Assert(test.oc.Condition(), gc.Equals, "")
		err = test.oc.Check(cond, arg)
		if test.expectError == "" {
			c.Assert(err, gc.IsNil)
			continue
		}
		c.Assert(err, gc.ErrorMatches, test.expectError)
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
