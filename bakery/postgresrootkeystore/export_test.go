package postgresrootkeystore

import "gopkg.in/macaroon-bakery.v3/bakery/dbrootkeystore"

var (
	Clock      = &clock
	NewBacking = &newBacking
)

func Backing(keys *RootKeys) dbrootkeystore.Backing {
	return backing{keys}
}
