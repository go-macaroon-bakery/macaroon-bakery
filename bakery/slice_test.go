package bakery_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	qt "github.com/frankban/quicktest"
	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v2"

	"github.com/go-macaroon-bakery/macaroon-bakery/v3/bakery"
	"github.com/go-macaroon-bakery/macaroon-bakery/v3/bakery/checkers"
)

func TestAddMoreCaveats(t *testing.T) {
	c := qt.New(t)
	getDischarge := func(_ context.Context, cav macaroon.Caveat, payload []byte) (*bakery.Macaroon, error) {
		c.Check(payload, qt.IsNil)
		m, err := bakery.NewMacaroon([]byte("root key "+string(cav.Id)), cav.Id, "", bakery.LatestVersion, nil)
		c.Assert(err, qt.Equals, nil)
		return m, nil
	}

	rootKey := []byte("root key")
	m, err := bakery.NewMacaroon(rootKey, []byte("id0"), "loc0", bakery.LatestVersion, testChecker.Namespace())
	c.Assert(err, qt.Equals, nil)
	err = m.M().AddThirdPartyCaveat([]byte("root key id1"), []byte("id1"), "somewhere")
	c.Assert(err, qt.Equals, nil)

	ms, err := bakery.Slice{m}.DischargeAll(testContext, getDischarge, nil)
	c.Assert(err, qt.Equals, nil)
	c.Assert(ms, qt.HasLen, 2)

	mms := ms.Bind()
	c.Assert(mms, qt.HasLen, len(ms))
	err = mms[0].Verify(rootKey, alwaysOK, mms[1:])
	c.Assert(err, qt.Equals, nil)

	// Add another caveat and to the root macaroon and discharge it.
	err = ms[0].M().AddThirdPartyCaveat([]byte("root key id2"), []byte("id2"), "somewhere else")
	c.Assert(err, qt.Equals, nil)

	ms, err = ms.DischargeAll(testContext, getDischarge, nil)
	c.Assert(err, qt.Equals, nil)
	c.Assert(ms, qt.HasLen, 3)

	mms = ms.Bind()
	err = mms[0].Verify(rootKey, alwaysOK, mms[1:])
	c.Assert(err, qt.Equals, nil)

	// Check that we can remove the original discharge and still re-acquire it OK.
	ms = bakery.Slice{ms[0], ms[2]}

	ms, err = ms.DischargeAll(testContext, getDischarge, nil)
	c.Assert(err, qt.Equals, nil)
	c.Assert(ms, qt.HasLen, 3)

	mms = ms.Bind()
	err = mms[0].Verify(rootKey, alwaysOK, mms[1:])
	c.Assert(err, qt.Equals, nil)
}

func TestPurge(t *testing.T) {
	c := qt.New(t)
	t0 := time.Date(2000, time.October, 1, 12, 0, 0, 0, time.UTC)
	clock := &stoppedClock{
		t: t0,
	}
	ctx := checkers.ContextWithClock(testContext, clock)
	checkCond := func(cond string) error {
		return testChecker.CheckFirstPartyCaveat(ctx, cond)
	}

	rootKey := []byte("root key")
	m, err := bakery.NewMacaroon(rootKey, []byte("id0"), "loc0", bakery.LatestVersion, testChecker.Namespace())
	c.Assert(err, qt.Equals, nil)
	err = m.AddCaveat(ctx, checkers.TimeBeforeCaveat(t0.Add(time.Hour)), nil, nil)
	c.Assert(err, qt.Equals, nil)
	err = m.M().AddThirdPartyCaveat([]byte("root key id1"), []byte("id1"), "somewhere")
	c.Assert(err, qt.Equals, nil)
	ms := bakery.Slice{m}

	getDischarge := func(_ context.Context, cav macaroon.Caveat, payload []byte) (*bakery.Macaroon, error) {
		c.Check(payload, qt.IsNil)
		m, err := bakery.NewMacaroon([]byte("root key "+string(cav.Id)), cav.Id, "", bakery.LatestVersion, testChecker.Namespace())
		c.Assert(err, qt.Equals, nil)
		err = m.AddCaveat(ctx, checkers.TimeBeforeCaveat(clock.t.Add(time.Minute)), nil, nil)
		c.Assert(err, qt.Equals, nil)
		return m, nil
	}
	ms, err = ms.DischargeAll(testContext, getDischarge, nil)
	c.Assert(err, qt.Equals, nil)
	c.Assert(ms, qt.HasLen, 2)

	mms := ms.Bind()
	err = mms[0].Verify(rootKey, checkCond, mms[1:])
	c.Assert(err, qt.Equals, nil)

	// Sanity check that verification fails when the discharge time has expired.
	clock.t = t0.Add(2 * time.Minute)

	err = mms[0].Verify(rootKey, checkCond, mms[1:])
	c.Assert(err, qt.ErrorMatches, `.*: macaroon has expired`)

	// Purge removes the discharge macaroon when it's out of date.
	ms = ms.Purge(clock.t)
	c.Assert(ms, qt.HasLen, 1)

	// Reacquire a discharge macaroon.
	ms, err = ms.DischargeAll(testContext, getDischarge, nil)
	c.Assert(err, qt.Equals, nil)
	c.Assert(ms, qt.HasLen, 2)

	// The macaroons should now be valid again.
	mms = ms.Bind()
	err = mms[0].Verify(rootKey, checkCond, mms[1:])
	c.Assert(err, qt.Equals, nil)

	// Check that when the time has gone beyond the primary
	// macaroon's expiry time, Purge removes all the macaroons.

	// Reacquire a discharge macaroon just before the primary
	// macaroon's expiry time.
	clock.t = t0.Add(time.Hour - time.Second)

	ms = ms.Purge(clock.t)
	c.Assert(ms, qt.HasLen, 1)
	ms, err = ms.DischargeAll(testContext, getDischarge, nil)
	c.Assert(err, qt.Equals, nil)
	c.Assert(ms, qt.HasLen, 2)

	// The macaroons should now be valid again.
	mms = ms.Bind()
	err = mms[0].Verify(rootKey, checkCond, mms[1:])
	c.Assert(err, qt.Equals, nil)

	// But once we've passed the hour, the primary expires
	// even though the discharge is valid, and purging
	// removes both primary and discharge.

	ms = ms.Purge(t0.Add(time.Hour + time.Millisecond))
	c.Assert(ms, qt.HasLen, 0)
}

func TestDischargeAllAcquiresManyMacaroonsAsPossible(t *testing.T) {
	c := qt.New(t)
	failIds := map[string]bool{
		"id1": true,
		"id3": true,
	}

	getDischarge := func(_ context.Context, cav macaroon.Caveat, payload []byte) (*bakery.Macaroon, error) {
		if failIds[string(cav.Id)] {
			return nil, errgo.Newf("discharge failure on %q", cav.Id)
		}
		m, err := bakery.NewMacaroon([]byte("root key "+string(cav.Id)), cav.Id, "", bakery.LatestVersion, nil)
		c.Assert(err, qt.Equals, nil)
		return m, nil
	}

	rootKey := []byte("root key")
	m, err := bakery.NewMacaroon(rootKey, []byte("id-root"), "", bakery.LatestVersion, testChecker.Namespace())
	c.Assert(err, qt.Equals, nil)
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("id%d", i)
		err = m.M().AddThirdPartyCaveat([]byte("root key "+id), []byte(id), "somewhere")
		c.Assert(err, qt.Equals, nil)
	}
	ms := bakery.Slice{m}

	ms, err = ms.DischargeAll(testContext, getDischarge, nil)
	c.Check(err, qt.ErrorMatches, `cannot get discharge from "somewhere": discharge failure on "id1"`)
	c.Assert(ms, qt.HasLen, 4)

	// Try again without id1 failing - we should acquire one more discharge.
	// Mark the other ones as failing because we shouldn't be trying to acquire
	// them because they're already in the slice.
	failIds = map[string]bool{
		"id0": true,
		"id3": true,
		"id4": true,
	}

	ms, err = ms.DischargeAll(testContext, getDischarge, nil)
	c.Check(err, qt.ErrorMatches, `cannot get discharge from "somewhere": discharge failure on "id3"`)
	c.Assert(ms, qt.HasLen, 5)

	failIds["id3"] = false

	ms, err = ms.DischargeAll(testContext, getDischarge, nil)
	c.Check(err, qt.Equals, nil)
	c.Assert(ms, qt.HasLen, 6)

	mms := ms.Bind()
	err = mms[0].Verify(rootKey, alwaysOK, mms[1:])
	c.Assert(err, qt.Equals, nil)
}
