package bakery

import (
	"fmt"

	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v1"

	"gopkg.in/macaroon-bakery.v0/bakery/checkers"
)

// DischargeAll gathers discharge macaroons for all the third party caveats
// in m (and any subsequent caveats required by those) using getDischarge to
// acquire each discharge macaroon.
// It returns a slice with m as the first element, followed by
// all the discharge macaroons. All the discharge macaroons
// will be bound to the primary macaroon.
//
// The selfKey parameter may optionally hold the
// key of the client, in which case it will be used
// to discharge any third party caveats with the
// special location "self". In this case, the caveat
// itself must be "true". This can be used be
// a server to ask a client to prove ownership
// of the private key.
func DischargeAll(
	m *macaroon.Macaroon,
	getDischarge func(firstPartyLocation string, cav macaroon.Caveat) (*macaroon.Macaroon, error),
	selfKey *KeyPair,
) (macaroon.Slice, error) {
	sig := m.Signature()
	discharges := macaroon.Slice{m}
	var need []macaroon.Caveat
	addCaveats := func(m *macaroon.Macaroon) {
		for _, cav := range m.Caveats() {
			if cav.Location == "" {
				continue
			}
			need = append(need, cav)
		}
	}
	addCaveats(m)
	firstPartyLocation := m.Location()
	for len(need) > 0 {
		cav := need[0]
		need = need[1:]
		var dm *macaroon.Macaroon
		var err error
		if selfKey != nil && cav.Location == "self" {
			dm, _, err = Discharge(selfKey, selfDischargeChecker, cav.Id)
		} else {
			dm, err = getDischarge(firstPartyLocation, cav)
		}
		if err != nil {
			return nil, errgo.NoteMask(err, fmt.Sprintf("cannot get discharge from %q", cav.Location), errgo.Any)
		}
		dm.Bind(sig)
		discharges = append(discharges, dm)
		addCaveats(dm)
	}
	return discharges, nil
}

var selfDischargeChecker = ThirdPartyCheckerFunc(func(caveatId, caveat string) ([]checkers.Caveat, *PublicKey, error) {
	if caveat != "true" {
		return nil, nil, checkers.ErrCaveatNotRecognized
	}
	return nil, nil, nil
})
