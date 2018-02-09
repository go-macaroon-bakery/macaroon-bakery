package identchecker

import (
	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
)

type Bakery struct {
	Oven    *bakery.Oven
	Checker *Checker
}

// BakeryParams holds a selection of parameters for the Oven
// and the Checker created by New.
//
// For more fine-grained control of parameters, create the
// Oven or Checker directly.
//
// The zero value is OK to use, but won't allow any authentication
// or third party caveats to be added.
type BakeryParams struct {
	// Checker holds the checker used to check first party caveats.
	// If this is nil, New will use checkers.New(nil).
	Checker bakery.FirstPartyCaveatChecker

	// RootKeyStore holds the root key store to use. If you need to
	// use a different root key store for different operations,
	// you'll need to pass a RootKeyStoreForOps value to NewOven
	// directly.
	//
	// If this is nil, New will use NewMemRootKeyStore().
	// Note that that is almost certain insufficient for production services
	// that are spread across multiple instances or that need
	// to persist keys across restarts.
	RootKeyStore bakery.RootKeyStore

	// Locator is used to find out information on third parties when
	// adding third party caveats. If this is nil, no non-local third
	// party caveats can be added.
	Locator bakery.ThirdPartyLocator

	// Key holds the private key of the oven. If this is nil,
	// no third party caveats may be added.
	Key *bakery.KeyPair

	// IdentityClient holds the identity implementation to use for
	// authentication. If this is nil, no authentication will be possible.
	IdentityClient IdentityClient

	// Authorizer is used to check whether an authenticated user is
	// allowed to perform operations. If it is nil, New will
	// use ClosedAuthorizer.
	//
	// The identity parameter passed to Authorizer.Allow will
	// always have been obtained from a call to
	// IdentityClient.DeclaredIdentity.
	Authorizer Authorizer

	// Location holds the location to use when creating new macaroons.
	Location string

	// Logger is used to log checker operations. If it is nil,
	// DefaultLogger("bakery.identchecker") will be used.
	Logger bakery.Logger
}

// NewBakery returns a new Bakery instance which combines an Oven with a
// Checker for the convenience of callers that wish to use both
// together.
func NewBakery(p BakeryParams) *Bakery {
	if p.Checker == nil {
		p.Checker = checkers.New(nil)
	}
	ovenParams := bakery.OvenParams{
		Key:              p.Key,
		Namespace:        p.Checker.Namespace(),
		Location:         p.Location,
		Locator:          p.Locator,
		LegacyMacaroonOp: LoginOp,
	}
	if p.RootKeyStore != nil {
		ovenParams.RootKeyStoreForOps = func(ops []bakery.Op) bakery.RootKeyStore {
			return p.RootKeyStore
		}
	}
	oven := bakery.NewOven(ovenParams)

	checker := NewChecker(CheckerParams{
		Checker:          p.Checker,
		MacaroonVerifier: oven,
		IdentityClient:   p.IdentityClient,
		Authorizer:       p.Authorizer,
		Logger:           p.Logger,
	})
	return &Bakery{
		Oven:    oven,
		Checker: checker,
	}
}
