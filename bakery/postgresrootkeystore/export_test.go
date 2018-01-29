package postgresrootkeystore

import "gopkg.in/macaroon-bakery.v2/bakery/dbrootkeystore"

var (
	Clock      = &clock
	NewBacking = &newBacking
)

func Backing(keys *RootKeys) dbrootkeystore.Backing {
	return backing{keys}
}
