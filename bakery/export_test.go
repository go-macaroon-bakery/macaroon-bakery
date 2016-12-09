package bakery

func MemOpsStoreLen(store OpsStore) int {
	return len(store.(*memOpsStore).ops)
}

func SetMacaroonCaveatIdPrefix(m *Macaroon, prefix []byte) {
	m.caveatIdPrefix = prefix
}

func MacaroonCaveatData(m *Macaroon) map[string][]byte {
	return m.caveatData
}

var LegacyNamespace = legacyNamespace

type MacaroonJSON macaroonJSON
