package bakery

import (
	"fmt"

	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v1"
)

// DischargeAll gathers discharge macaroons for all the third party caveats
// in m (and any subsequent caveats required by those) using getDischarge to
// acquire each discharge macaroon.
func DischargeAll(
	m *macaroon.Macaroon,
	getDischarge func(firstPartyLocation string, cav macaroon.Caveat) (*macaroon.Macaroon, error),
) ([]*macaroon.Macaroon, error) {
	var discharges []*macaroon.Macaroon
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
		discharges = append(discharges, dm)
		addCaveats(dm)
	}
	return discharges, nil
}
