package bakery_test

import (
	"encoding/json"

	"golang.org/x/net/context"
	errgo "gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v2"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
)

type macaroonStore struct {
	rootKeyStore bakery.RootKeyStore

	key *bakery.KeyPair

	locator bakery.ThirdPartyLocator
}

// newMacaroonStore returns a MacaroonVerifier implementation
// that stores root keys in memory and puts all operations
// in the macaroon id.
func newMacaroonStore(locator bakery.ThirdPartyLocator) *macaroonStore {
	return &macaroonStore{
		rootKeyStore: bakery.NewMemRootKeyStore(),
		key:          mustGenerateKey(),
		locator:      locator,
	}
}

type macaroonId struct {
	Id  []byte
	Ops []bakery.Op
}

func (s *macaroonStore) NewMacaroon(ctx context.Context, ops []bakery.Op, caveats []checkers.Caveat, ns *checkers.Namespace) (*bakery.Macaroon, error) {
	rootKey, id, err := s.rootKeyStore.RootKey(ctx)
	if err != nil {
		return nil, errgo.Mask(err)
	}

	mid := macaroonId{
		Id:  id,
		Ops: ops,
	}
	data, _ := json.Marshal(mid)
	m, err := bakery.NewMacaroon(rootKey, data, "", bakery.LatestVersion, ns)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	if err := m.AddCaveats(ctx, caveats, s.key, s.locator); err != nil {
		return nil, errgo.Mask(err)
	}
	return m, nil
}

func (s *macaroonStore) VerifyMacaroon(ctx context.Context, ms macaroon.Slice) (ops []bakery.Op, conditions []string, err error) {
	if len(ms) == 0 {
		return nil, nil, &bakery.VerificationError{
			Reason: errgo.Newf("no macaroons in slice"),
		}
	}
	id := ms[0].Id()
	var mid macaroonId
	if err := json.Unmarshal(id, &mid); err != nil {
		return nil, nil, &bakery.VerificationError{
			Reason: errgo.Notef(err, "bad macaroon id"),
		}
	}
	rootKey, err := s.rootKeyStore.Get(ctx, mid.Id)
	if err != nil {
		if errgo.Cause(err) == bakery.ErrNotFound {
			return nil, nil, &bakery.VerificationError{
				Reason: errgo.Notef(err, "cannot find root key"),
			}
		}
		return nil, nil, errgo.Notef(err, "cannot find root key")
	}
	conditions, err = ms[0].VerifySignature(rootKey, ms[1:])
	if err != nil {
		return nil, nil, &bakery.VerificationError{
			Reason: errgo.Mask(err),
		}
	}
	return mid.Ops, conditions, nil
}

// macaroonVerifierWithError is an implementation of MacaroonVerifier that
// returns the given error on all store operations.
type macaroonVerifierWithError struct {
	err error
}

func (s macaroonVerifierWithError) VerifyMacaroon(ctx context.Context, ms macaroon.Slice) (ops []bakery.Op, conditions []string, err error) {
	return nil, nil, errgo.Mask(s.err, errgo.Any)
}
