package bakery_test

import (
	"encoding/json"

	jujutesting "github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/macaroon.v2"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
)

type macaroonSuite struct {
	jujutesting.LoggingSuite
}

var _ = gc.Suite(&macaroonSuite{})

func (*macaroonSuite) TestNewMacaroon(c *gc.C) {
	ns := checkers.NewNamespace(nil)
	m, err := bakery.NewMacaroon([]byte("rootkey"), []byte("some id"), "here", bakery.LatestVersion, ns)
	c.Assert(err, gc.IsNil)
	c.Assert(m.Namespace(), gc.Equals, ns)
	c.Assert(m.Version(), gc.Equals, bakery.LatestVersion)
	c.Assert(string(m.M().Id()), gc.Equals, "some id")
	c.Assert(m.M().Location(), gc.Equals, "here")
	c.Assert(m.M().Version(), gc.Equals, bakery.MacaroonVersion(bakery.LatestVersion))
}

func (*macaroonSuite) TestAddFirstPartyCaveat(c *gc.C) {
	ns := checkers.NewNamespace(nil)
	ns.Register("someuri", "x")
	m, err := bakery.NewMacaroon([]byte("rootkey"), []byte("some id"), "here", bakery.LatestVersion, ns)
	c.Assert(err, gc.IsNil)
	err = m.AddCaveat(testContext, checkers.Caveat{
		Condition: "something",
		Namespace: "someuri",
	}, nil, nil)
	c.Assert(err, gc.IsNil)
	c.Assert(m.M().Caveats(), jc.DeepEquals, []macaroon.Caveat{{
		Id: []byte("x:something"),
	}})
}

// lbv holds the latest bakery version as used in the
// third party caveat id.
var lbv = byte(bakery.LatestVersion)

var addThirdPartyCaveatTests = []struct {
	about             string
	baseId            []byte
	existingCaveatIds [][]byte
	expectId          []byte
}{{
	about:    "no existing id",
	expectId: []byte{lbv, 0},
}, {
	about: "several existing ids",
	existingCaveatIds: [][]byte{
		{lbv, 0},
		{lbv, 1},
		{lbv, 2},
	},
	expectId: []byte{lbv, 3},
}, {
	about: "with base id",
	existingCaveatIds: [][]byte{
		{lbv, 0},
	},
	baseId:   []byte{lbv, 0},
	expectId: []byte{lbv, 0, 0},
}, {
	about: "with base id and existing id",
	existingCaveatIds: [][]byte{
		{lbv, 0, 0},
	},
	baseId:   []byte{lbv, 0},
	expectId: []byte{lbv, 0, 1},
}}

func (*macaroonSuite) TestAddThirdPartyCaveat(c *gc.C) {
	locator := bakery.NewThirdPartyStore()
	as := newBakery("as-loc", locator)

	for i, test := range addThirdPartyCaveatTests {
		c.Logf("test %d: %v", i, test.about)
		m, err := bakery.NewMacaroon([]byte("root key"), []byte("id"), "location", bakery.LatestVersion, nil)
		c.Assert(err, gc.IsNil)
		for _, id := range test.existingCaveatIds {
			err := m.M().AddThirdPartyCaveat(nil, id, "")
			c.Assert(err, gc.IsNil)
		}
		bakery.SetMacaroonCaveatIdPrefix(m, test.baseId)
		err = m.AddCaveat(testContext, checkers.Caveat{
			Location:  "as-loc",
			Condition: "something",
		}, as.Oven.Key(), locator)
		c.Assert(err, gc.IsNil)
		c.Assert(m.M().Caveats()[len(test.existingCaveatIds)].Id, jc.DeepEquals, test.expectId)
	}
}

func (*macaroonSuite) TestMarshalJSONLatestVersion(c *gc.C) {
	locator := bakery.NewThirdPartyStore()
	as := newBakery("as-loc", locator)

	ns := checkers.NewNamespace(map[string]string{
		"testns":  "x",
		"otherns": "y",
	})
	m, err := bakery.NewMacaroon([]byte("root key"), []byte("id"), "location", bakery.LatestVersion, ns)
	c.Assert(err, gc.IsNil)
	err = m.AddCaveat(testContext, checkers.Caveat{
		Location:  "as-loc",
		Condition: "something",
	}, as.Oven.Key(), locator)
	c.Assert(err, gc.IsNil)

	data, err := json.Marshal(m)
	c.Assert(err, gc.IsNil)

	var m1 *bakery.Macaroon
	err = json.Unmarshal(data, &m1)
	c.Assert(err, gc.IsNil)
	// Just check the signature and version - we're not interested in fully
	// checking the macaroon marshaling here.
	c.Assert(m1.M().Signature(), jc.DeepEquals, m.M().Signature())
	c.Assert(m1.M().Version(), gc.Equals, m.M().Version())
	c.Assert(m1.M().Caveats(), gc.HasLen, 1)

	c.Assert(m1.Namespace(), jc.DeepEquals, m.Namespace())

	c.Assert(bakery.MacaroonCaveatData(m1), jc.DeepEquals, bakery.MacaroonCaveatData(m))
}

func (s *macaroonSuite) TestMarshalJSONVersion1(c *gc.C) {
	s.testMarshalJSONWithVersion(c, bakery.Version1)
}

func (s *macaroonSuite) TestMarshalJSONVersion2(c *gc.C) {
	s.testMarshalJSONWithVersion(c, bakery.Version2)
}

func (*macaroonSuite) testMarshalJSONWithVersion(c *gc.C, version bakery.Version) {
	locator := bakery.NewThirdPartyStore()
	as := newBakery("as-loc", locator)

	ns := checkers.NewNamespace(map[string]string{
		"testns": "x",
	})

	m, err := bakery.NewMacaroon([]byte("root key"), []byte("id"), "location", version, ns)
	c.Assert(err, gc.IsNil)
	err = m.AddCaveat(testContext, checkers.Caveat{
		Location:  "as-loc",
		Condition: "something",
	}, as.Oven.Key(), locator)
	c.Assert(err, gc.IsNil)

	// Sanity check that no external caveat data has been added.
	c.Assert(bakery.MacaroonCaveatData(m), gc.HasLen, 0)

	data, err := json.Marshal(m)
	c.Assert(err, gc.IsNil)

	var m1 *bakery.Macaroon
	err = json.Unmarshal(data, &m1)
	c.Assert(err, gc.IsNil)
	// Just check the signature and version - we're not interested in fully
	// checking the macaroon marshaling here.
	c.Assert(m1.M().Signature(), jc.DeepEquals, m.M().Signature())
	c.Assert(m1.M().Version(), gc.Equals, bakery.MacaroonVersion(version))
	c.Assert(m1.M().Caveats(), gc.HasLen, 1)

	// Namespace information has been thrown away.
	c.Assert(m1.Namespace(), jc.DeepEquals, bakery.LegacyNamespace())

	c.Assert(bakery.MacaroonCaveatData(m1), gc.HasLen, 0)

	// Check that we can unmarshal it directly as a V2 macaroon
	var m2 *macaroon.Macaroon
	err = json.Unmarshal(data, &m2)
	c.Assert(err, gc.IsNil)

	c.Assert(m2.Signature(), jc.DeepEquals, m.M().Signature())
	c.Assert(m2.Version(), gc.Equals, bakery.MacaroonVersion(version))
	c.Assert(m2.Caveats(), gc.HasLen, 1)
}

func (*macaroonSuite) TestUnmarshalJSONUnknownVersion(c *gc.C) {
	m, err := macaroon.New(nil, nil, "", macaroon.V2)
	c.Assert(err, gc.IsNil)
	data, err := json.Marshal(bakery.MacaroonJSON{
		Macaroon: m,
		Version:  bakery.LatestVersion + 1,
	})
	c.Assert(err, gc.IsNil)
	var m1 *bakery.Macaroon
	err = json.Unmarshal([]byte(data), &m1)
	c.Assert(err, gc.ErrorMatches, `unexpected bakery macaroon version; got 4 want 3`)
}

func (*macaroonSuite) TestUnmarshalJSONInconsistentVersion(c *gc.C) {
	m, err := macaroon.New(nil, nil, "", macaroon.V1)
	c.Assert(err, gc.IsNil)
	data, err := json.Marshal(bakery.MacaroonJSON{
		Macaroon: m,
		Version:  bakery.LatestVersion,
	})
	c.Assert(err, gc.IsNil)
	var m1 *bakery.Macaroon
	err = json.Unmarshal([]byte(data), &m1)
	c.Assert(err, gc.ErrorMatches, `underlying macaroon has inconsistent version; got 1 want 2`)
}

func (*macaroonSuite) TestClone(c *gc.C) {
	locator := bakery.NewThirdPartyStore()
	as := newBakery("as-loc", locator)

	ns := checkers.NewNamespace(map[string]string{
		"testns": "x",
	})

	m, err := bakery.NewMacaroon([]byte("root key"), []byte("id"), "location", bakery.LatestVersion, ns)
	c.Assert(err, gc.IsNil)
	err = m.AddCaveat(testContext, checkers.Caveat{
		Location:  "as-loc",
		Condition: "something",
	}, as.Oven.Key(), locator)
	c.Assert(err, gc.IsNil)

	m1 := m.Clone()

	c.Assert(m.M().Caveats(), gc.HasLen, 1)
	c.Assert(m1.M().Caveats(), gc.HasLen, 1)
	c.Assert(bakery.MacaroonCaveatData(m), gc.DeepEquals, bakery.MacaroonCaveatData(m1))

	err = m.AddCaveat(testContext, checkers.Caveat{
		Location:  "as-loc",
		Condition: "something",
	}, as.Oven.Key(), locator)
	c.Assert(err, gc.IsNil)

	c.Assert(m.M().Caveats(), gc.HasLen, 2)
	c.Assert(m1.M().Caveats(), gc.HasLen, 1)

	c.Assert(bakery.MacaroonCaveatData(m), gc.Not(gc.DeepEquals), bakery.MacaroonCaveatData(m1))
}

func (*macaroonSuite) TestUnmarshalBadData(c *gc.C) {
	var m1 *bakery.Macaroon
	err := json.Unmarshal([]byte(`{"m": []}`), &m1)
	c.Assert(err, gc.ErrorMatches, `json: cannot unmarshal array .*`)
}
