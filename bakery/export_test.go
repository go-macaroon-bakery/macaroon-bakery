package bakery

func SetMacaroonCaveatIdPrefix(m *Macaroon, prefix []byte) {
	m.caveatIdPrefix = prefix
}

func MacaroonCaveatData(m *Macaroon) map[string][]byte {
	return m.caveatData
}

var LegacyNamespace = legacyNamespace

type MacaroonJSON macaroonJSON
