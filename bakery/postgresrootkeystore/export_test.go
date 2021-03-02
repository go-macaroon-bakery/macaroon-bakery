package postgresrootkeystore

import "github.com/go-macaroon-bakery/macaroon-bakery/v3/bakery/dbrootkeystore"

var (
	Clock      = &clock
	NewBacking = &newBacking
)

func Backing(keys *RootKeys) dbrootkeystore.Backing {
	return backing{keys}
}
