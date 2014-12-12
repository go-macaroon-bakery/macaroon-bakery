package bakery

import (
	"fmt"

	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v1"
)

// DischargeAll gathers discharge macaroons for all the third party caveats
// in m (and any subsequent caveats required by those) using getDischarge to
// acquire each discharge macaroon.
// It returns a slice with m as the first element, followed by
// all the discharge macaroons. All the discharge macaroons
// will be bound to the primary macaroon.
func DischargeAll(
	m *macaroon.Macaroon,
	getDischarge func(firstPartyLocation string, cav macaroon.Caveat) (*macaroon.Macaroon, error),
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
		dm, err := getDischarge(firstPartyLocation, cav)
		if err != nil {
			return nil, errgo.NoteMask(err, fmt.Sprintf("cannot get discharge from %q", cav.Location), errgo.Any)
		}
		dm.Bind(sig)
		discharges = append(discharges, dm)
		addCaveats(dm)
	}
	return discharges, nil
}
