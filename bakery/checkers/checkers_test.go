package checkers_test

import (
	"fmt"
	"time"

	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v0/bakery"
	"gopkg.in/macaroon-bakery.v0/bakery/checkers"
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

var isCaveatNotRecognized = errgo.Is(bakery.ErrCaveatNotRecognized)

var checkerTests = []struct {
	about   string
	checker bakery.FirstPartyChecker
	checks  []checkTest
}{{
	about:   "empty MultiChecker",
	checker: checkers.New(),
	checks: []checkTest{{
		caveat:      "something",
		expectError: `caveat "something" not fulfilled: caveat not recognized`,
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
		expectError: `caveat "a wrong" not fulfilled: wrong arg`,
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
		expectError: `caveat "a aval" not fulfilled: wrong arg`,
		expectCause: errgo.Is(errWrongArg),
	}, {
		caveat:      "a bval",
		expectError: `caveat "a bval" not fulfilled: wrong arg`,
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
		expectError: `caveat "a wrong" not fulfilled: wrong arg`,
		expectCause: errgo.Is(errWrongArg),
	}, {
		caveat:      "c wrong",
		expectError: `caveat "c wrong" not fulfilled: wrong arg`,
		expectCause: errgo.Is(errWrongArg),
	}, {
		caveat:      "d wrong",
		expectError: `caveat "d wrong" not fulfilled: wrong arg`,
		expectCause: errgo.Is(errWrongArg),
	}, {
		caveat:      "f something",
		expectError: `caveat "f something" not fulfilled: caveat not recognized`,
		expectCause: isCaveatNotRecognized,
	}},
}, {
	about: "Map with no items",
	checker: checkers.New(
		checkers.Map{},
	),
	checks: []checkTest{{
		caveat:      "a aval",
		expectError: `caveat "a aval" not fulfilled: caveat not recognized`,
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
		expectError: `caveat "a wrong" not fulfilled: wrong arg`,
		expectCause: errgo.Is(errWrongArg),
	}, {
		caveat:      "b wrong",
		expectError: `caveat "b wrong" not fulfilled: wrong arg`,
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
		expectError: `caveat "time-before 2006-01-02T15:04:05.123Z" not fulfilled: macaroon has expired`,
	}, {
		caveat:      checkers.TimeBeforeCaveat(now.Add(-1)).Condition,
		expectError: `caveat "time-before 2006-01-02T15:04:05.122999999Z" not fulfilled: macaroon has expired`,
	}, {
		caveat:      `time-before bad-date`,
		expectError: `caveat "time-before bad-date" not fulfilled: parsing time "bad-date" as "2006-01-02T15:04:05.999999999Z07:00": cannot parse "bad-date" as "2006"`,
	}, {
		caveat:      checkers.TimeBeforeCaveat(now).Condition + " ",
		expectError: `caveat "time-before 2006-01-02T15:04:05.123Z " not fulfilled: parsing time "2006-01-02T15:04:05.123Z ": extra text:  `,
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

func (s *CheckersSuite) TestMultiChecker(c *gc.C) {
	c.Logf("time is %s", now)
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
