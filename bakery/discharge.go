package bakery

import (
	"fmt"

	"golang.org/x/net/context"
	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v2-unstable"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
)

// DischargeAll gathers discharge macaroons for all the third party
// caveats in m (and any subsequent caveats required by those) using
// getDischarge to acquire each discharge macaroon. It returns a slice
// with m as the first element, followed by all the discharge macaroons.
// All the discharge macaroons will be bound to the primary macaroon.
//
// The getDischarge function is passed the caveat to be discharged;
// encryptedCaveat will be passed the external caveat payload found
// in m, if any.
func DischargeAll(
	ctx context.Context,
	m *Macaroon,
	getDischarge func(ctx context.Context, cav macaroon.Caveat, encryptedCaveat []byte) (*Macaroon, error),
) (macaroon.Slice, error) {
	return DischargeAllWithKey(ctx, m, getDischarge, nil)
}

// DischargeAllWithKey is like DischargeAll except that the localKey
// parameter may optionally hold the key of the client, in which case it
// will be used to discharge any third party caveats with the special
// location "local". In this case, the caveat itself must be "true". This
// can be used be a server to ask a client to prove ownership of the
// private key.
//
// When localKey is nil, DischargeAllWithKey is exactly the same as
// DischargeAll.
func DischargeAllWithKey(
	ctx context.Context,
	m *Macaroon,
	getDischarge func(ctx context.Context, cav macaroon.Caveat, encodedCaveat []byte) (*Macaroon, error),
	localKey *KeyPair,
) (macaroon.Slice, error) {
	primary := m.M()
	discharges := macaroon.Slice{primary}

	type needCaveat struct {
		// cav holds the caveat that needs discharge.
		cav macaroon.Caveat
		// encryptedCaveat holds encrypted caveat
		// if it was held externally.
		encryptedCaveat []byte
	}
	var need []needCaveat
	addCaveats := func(m *Macaroon) {
		for _, cav := range m.M().Caveats() {
			if cav.Location == "" {
				continue
			}
			need = append(need, needCaveat{
				cav:             cav,
				encryptedCaveat: m.caveatData[string(cav.Id)],
			})
		}
	}
	sig := primary.Signature()
	addCaveats(m)
	for len(need) > 0 {
		cav := need[0]
		need = need[1:]
		var dm *Macaroon
		var err error
		if localKey != nil && cav.cav.Location == "local" {
			// TODO use a small caveat id.
			dm, err = Discharge(ctx, DischargeParams{
				Key:     localKey,
				Checker: localDischargeChecker,
				Caveat:  cav.encryptedCaveat,
				Id:      cav.cav.Id,
				Locator: emptyLocator{},
			})
		} else {
			dm, err = getDischarge(ctx, cav.cav, cav.encryptedCaveat)
		}
		if err != nil {
			return nil, errgo.NoteMask(err, fmt.Sprintf("cannot get discharge from %q", cav.cav.Location), errgo.Any)
		}
		// It doesn't matter that we're invalidating dm here because we're
		// about to throw it away.
		discharge := dm.M()
		discharge.Bind(sig)
		discharges = append(discharges, discharge)
		addCaveats(dm)
	}
	return discharges, nil
}

var localDischargeChecker = ThirdPartyCaveatCheckerFunc(func(_ context.Context, info *ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
	if string(info.Condition) != "true" {
		return nil, checkers.ErrCaveatNotRecognized
	}
	return nil, nil
})
