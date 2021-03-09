package bakery_test

import (
	"encoding/json"
	"testing"

	qt "github.com/frankban/quicktest"
	"gopkg.in/macaroon.v2"

	"github.com/go-macaroon-bakery/macaroon-bakery/v3/bakery"
	"github.com/go-macaroon-bakery/macaroon-bakery/v3/bakery/checkers"
)

func TestNewMacaroon(t *testing.T) {
	c := qt.New(t)
	ns := checkers.NewNamespace(nil)
	m, err := bakery.NewMacaroon([]byte("rootkey"), []byte("some id"), "here", bakery.LatestVersion, ns)
	c.Assert(err, qt.IsNil)
	c.Assert(m.Namespace(), qt.Equals, ns)
	c.Assert(m.Version(), qt.Equals, bakery.LatestVersion)
	c.Assert(string(m.M().Id()), qt.Equals, "some id")
	c.Assert(m.M().Location(), qt.Equals, "here")
	c.Assert(m.M().Version(), qt.Equals, bakery.MacaroonVersion(bakery.LatestVersion))
}

func TestAddFirstPartyCaveat(t *testing.T) {
	c := qt.New(t)
	ns := checkers.NewNamespace(nil)
	ns.Register("someuri", "x")
	m, err := bakery.NewMacaroon([]byte("rootkey"), []byte("some id"), "here", bakery.LatestVersion, ns)
	c.Assert(err, qt.IsNil)
	err = m.AddCaveat(testContext, checkers.Caveat{
		Condition: "something",
		Namespace: "someuri",
	}, nil, nil)
	c.Assert(err, qt.IsNil)
	c.Assert(m.M().Caveats(), qt.DeepEquals, []macaroon.Caveat{{
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

func TestAddThirdPartyCaveat(t *testing.T) {
	c := qt.New(t)
	locator := bakery.NewThirdPartyStore()
	as := newBakery("as-loc", locator)

	for i, test := range addThirdPartyCaveatTests {
		c.Logf("test %d: %v", i, test.about)
		m, err := bakery.NewMacaroon([]byte("root key"), []byte("id"), "location", bakery.LatestVersion, nil)
		c.Assert(err, qt.IsNil)
		for _, id := range test.existingCaveatIds {
			err := m.M().AddThirdPartyCaveat(nil, id, "")
			c.Assert(err, qt.IsNil)
		}
		bakery.SetMacaroonCaveatIdPrefix(m, test.baseId)
		err = m.AddCaveat(testContext, checkers.Caveat{
			Location:  "as-loc",
			Condition: "something",
		}, as.Oven.Key(), locator)
		c.Assert(err, qt.IsNil)
		c.Assert(m.M().Caveats()[len(test.existingCaveatIds)].Id, qt.DeepEquals, test.expectId)
	}
}

func TestMarshalJSONLatestVersion(t *testing.T) {
	c := qt.New(t)
	locator := bakery.NewThirdPartyStore()
	as := newBakery("as-loc", locator)

	ns := checkers.NewNamespace(map[string]string{
		"testns":  "x",
		"otherns": "y",
	})
	m, err := bakery.NewMacaroon([]byte("root key"), []byte("id"), "location", bakery.LatestVersion, ns)
	c.Assert(err, qt.IsNil)
	err = m.AddCaveat(testContext, checkers.Caveat{
		Location:  "as-loc",
		Condition: "something",
	}, as.Oven.Key(), locator)
	c.Assert(err, qt.IsNil)

	data, err := json.Marshal(m)
	c.Assert(err, qt.IsNil)

	var m1 *bakery.Macaroon
	err = json.Unmarshal(data, &m1)
	c.Assert(err, qt.IsNil)
	// Just check the signature and version - we're not interested in fully
	// checking the macaroon marshaling here.
	c.Assert(m1.M().Signature(), qt.DeepEquals, m.M().Signature())
	c.Assert(m1.M().Version(), qt.Equals, m.M().Version())
	c.Assert(m1.M().Caveats(), qt.HasLen, 1)

	c.Assert(m1.Namespace(), qt.DeepEquals, m.Namespace())

	c.Assert(bakery.MacaroonCaveatData(m1), qt.DeepEquals, bakery.MacaroonCaveatData(m))
}

func TestMarshalJSONVersion1(t *testing.T) {
	c := qt.New(t)
	testMarshalJSONWithVersion(c, bakery.Version1)
}

func TestMarshalJSONVersion2(t *testing.T) {
	c := qt.New(t)
	testMarshalJSONWithVersion(c, bakery.Version2)
}

func testMarshalJSONWithVersion(c *qt.C, version bakery.Version) {
	locator := bakery.NewThirdPartyStore()
	as := newBakery("as-loc", locator)

	ns := checkers.NewNamespace(map[string]string{
		"testns": "x",
	})

	m, err := bakery.NewMacaroon([]byte("root key"), []byte("id"), "location", version, ns)
	c.Assert(err, qt.IsNil)
	err = m.AddCaveat(testContext, checkers.Caveat{
		Location:  "as-loc",
		Condition: "something",
	}, as.Oven.Key(), locator)
	c.Assert(err, qt.IsNil)

	// Sanity check that no external caveat data has been added.
	c.Assert(bakery.MacaroonCaveatData(m), qt.HasLen, 0)

	data, err := json.Marshal(m)
	c.Assert(err, qt.IsNil)

	var m1 *bakery.Macaroon
	err = json.Unmarshal(data, &m1)
	c.Assert(err, qt.IsNil)
	// Just check the signature and version - we're not interested in fully
	// checking the macaroon marshaling here.
	c.Assert(m1.M().Signature(), qt.DeepEquals, m.M().Signature())
	c.Assert(m1.M().Version(), qt.Equals, bakery.MacaroonVersion(version))
	c.Assert(m1.M().Caveats(), qt.HasLen, 1)

	// Namespace information has been thrown away.
	c.Assert(m1.Namespace(), qt.DeepEquals, bakery.LegacyNamespace())

	c.Assert(bakery.MacaroonCaveatData(m1), qt.HasLen, 0)

	// Check that we can unmarshal it directly as a V2 macaroon
	var m2 *macaroon.Macaroon
	err = json.Unmarshal(data, &m2)
	c.Assert(err, qt.IsNil)

	c.Assert(m2.Signature(), qt.DeepEquals, m.M().Signature())
	c.Assert(m2.Version(), qt.Equals, bakery.MacaroonVersion(version))
	c.Assert(m2.Caveats(), qt.HasLen, 1)
}

func TestUnmarshalJSONUnknownVersion(t *testing.T) {
	c := qt.New(t)
	m, err := macaroon.New(nil, nil, "", macaroon.V2)
	c.Assert(err, qt.IsNil)
	data, err := json.Marshal(bakery.MacaroonJSON{
		Macaroon: m,
		Version:  bakery.LatestVersion + 1,
	})
	c.Assert(err, qt.IsNil)
	var m1 *bakery.Macaroon
	err = json.Unmarshal([]byte(data), &m1)
	c.Assert(err, qt.ErrorMatches, `unexpected bakery macaroon version; got 4 want 3`)
}

func TestUnmarshalJSONInconsistentVersion(t *testing.T) {
	c := qt.New(t)
	m, err := macaroon.New(nil, nil, "", macaroon.V1)
	c.Assert(err, qt.IsNil)
	data, err := json.Marshal(bakery.MacaroonJSON{
		Macaroon: m,
		Version:  bakery.LatestVersion,
	})
	c.Assert(err, qt.IsNil)
	var m1 *bakery.Macaroon
	err = json.Unmarshal([]byte(data), &m1)
	c.Assert(err, qt.ErrorMatches, `underlying macaroon has inconsistent version; got 1 want 2`)
}

func TestClone(t *testing.T) {
	c := qt.New(t)
	locator := bakery.NewThirdPartyStore()
	as := newBakery("as-loc", locator)

	ns := checkers.NewNamespace(map[string]string{
		"testns": "x",
	})

	m, err := bakery.NewMacaroon([]byte("root key"), []byte("id"), "location", bakery.LatestVersion, ns)
	c.Assert(err, qt.IsNil)
	err = m.AddCaveat(testContext, checkers.Caveat{
		Location:  "as-loc",
		Condition: "something",
	}, as.Oven.Key(), locator)
	c.Assert(err, qt.IsNil)

	m1 := m.Clone()

	c.Assert(m.M().Caveats(), qt.HasLen, 1)
	c.Assert(m1.M().Caveats(), qt.HasLen, 1)
	c.Assert(bakery.MacaroonCaveatData(m), qt.DeepEquals, bakery.MacaroonCaveatData(m1))

	err = m.AddCaveat(testContext, checkers.Caveat{
		Location:  "as-loc",
		Condition: "something",
	}, as.Oven.Key(), locator)
	c.Assert(err, qt.IsNil)

	c.Assert(m.M().Caveats(), qt.HasLen, 2)
	c.Assert(m1.M().Caveats(), qt.HasLen, 1)

	c.Assert(bakery.MacaroonCaveatData(m), qt.Not(qt.DeepEquals), bakery.MacaroonCaveatData(m1))
}

func TestUnmarshalBadData(t *testing.T) {
	c := qt.New(t)
	var m1 *bakery.Macaroon
	err := json.Unmarshal([]byte(`{"m": []}`), &m1)
	c.Assert(err, qt.ErrorMatches, `json: cannot unmarshal array .*`)
}
