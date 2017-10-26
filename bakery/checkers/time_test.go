package checkers_test

import (
	"time"

	gc "gopkg.in/check.v1"
	"gopkg.in/macaroon.v2"

	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
)

type timeSuite struct{}

var _ = gc.Suite(&timeSuite{})

var t1 = time.Now()
var t2 = t1.Add(1 * time.Hour)
var t3 = t2.Add(1 * time.Hour)

var expireTimeTests = []struct {
	about         string
	caveats       []macaroon.Caveat
	expectTime    time.Time
	expectExpires bool
}{{
	about: "nil caveats",
}, {
	about:   "empty caveats",
	caveats: []macaroon.Caveat{},
}, {
	about: "single time-before caveat",
	caveats: []macaroon.Caveat{
		macaroon.Caveat{
			Id: []byte(checkers.TimeBeforeCaveat(t1).Condition),
		},
	},
	expectTime:    t1,
	expectExpires: true,
}, {
	about: "multiple time-before caveat",
	caveats: []macaroon.Caveat{
		macaroon.Caveat{
			Id: []byte(checkers.TimeBeforeCaveat(t2).Condition),
		},
		macaroon.Caveat{
			Id: []byte(checkers.TimeBeforeCaveat(t1).Condition),
		},
	},
	expectTime:    t1,
	expectExpires: true,
}, {
	about: "mixed caveats",
	caveats: []macaroon.Caveat{
		macaroon.Caveat{
			Id: []byte(checkers.TimeBeforeCaveat(t1).Condition),
		},
		macaroon.Caveat{
			Id: []byte("allow bar"),
		},
		macaroon.Caveat{
			Id: []byte(checkers.TimeBeforeCaveat(t2).Condition),
		},
		macaroon.Caveat{
			Id: []byte("deny foo"),
		},
	},
	expectTime:    t1,
	expectExpires: true,
}, {
	about: "invalid time-before caveat",
	caveats: []macaroon.Caveat{
		macaroon.Caveat{
			Id: []byte(checkers.CondTimeBefore + " tomorrow"),
		},
	},
}}

func (s *timeSuite) TestExpireTime(c *gc.C) {
	for i, test := range expireTimeTests {
		c.Logf("%d. %s", i, test.about)
		t, expires := checkers.ExpiryTime(nil, test.caveats)
		c.Assert(t.Equal(test.expectTime), gc.Equals, true, gc.Commentf("obtained: %s, expected: %s", t, test.expectTime))
		c.Assert(expires, gc.Equals, test.expectExpires)
	}
}

var macaroonsExpireTimeTests = []struct {
	about         string
	macaroons     macaroon.Slice
	expectTime    time.Time
	expectExpires bool
}{{
	about: "nil macaroons",
}, {
	about:     "empty macaroons",
	macaroons: macaroon.Slice{},
}, {
	about: "single macaroon without caveats",
	macaroons: macaroon.Slice{
		mustNewMacaroon(),
	},
}, {
	about: "multiple macaroon without caveats",
	macaroons: macaroon.Slice{
		mustNewMacaroon(),
		mustNewMacaroon(),
	},
}, {
	about: "single macaroon with time-before caveat",
	macaroons: macaroon.Slice{
		mustNewMacaroon(
			checkers.TimeBeforeCaveat(t1).Condition,
		),
	},
	expectTime:    t1,
	expectExpires: true,
}, {
	about: "single macaroon with multiple time-before caveats",
	macaroons: macaroon.Slice{
		mustNewMacaroon(
			checkers.TimeBeforeCaveat(t2).Condition,
			checkers.TimeBeforeCaveat(t1).Condition,
		),
	},
	expectTime:    t1,
	expectExpires: true,
}, {
	about: "multiple macaroons with multiple time-before caveats",
	macaroons: macaroon.Slice{
		mustNewMacaroon(
			checkers.TimeBeforeCaveat(t3).Condition,
			checkers.TimeBeforeCaveat(t2).Condition,
		),
		mustNewMacaroon(
			checkers.TimeBeforeCaveat(t3).Condition,
			checkers.TimeBeforeCaveat(t1).Condition,
		),
	},
	expectTime:    t1,
	expectExpires: true,
}}

func (s *timeSuite) TestMacaroonsExpireTime(c *gc.C) {
	for i, test := range macaroonsExpireTimeTests {
		c.Logf("%d. %s", i, test.about)
		t, expires := checkers.MacaroonsExpiryTime(nil, test.macaroons)
		c.Assert(t.Equal(test.expectTime), gc.Equals, true, gc.Commentf("obtained: %s, expected: %s", t, test.expectTime))
		c.Assert(expires, gc.Equals, test.expectExpires)
	}
}

func mustNewMacaroon(cavs ...string) *macaroon.Macaroon {
	m, err := macaroon.New(nil, nil, "", macaroon.LatestVersion)
	if err != nil {
		panic(err)
	}
	for _, cav := range cavs {
		if err := m.AddFirstPartyCaveat([]byte(cav)); err != nil {
			panic(err)
		}
	}
	return m
}
