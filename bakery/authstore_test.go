package bakery_test

import (
	"encoding/json"

	"golang.org/x/net/context"
	errgo "gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v2-unstable"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
)

type macaroonStore struct {
	rootKeyStore bakery.RootKeyStore

	key *bakery.KeyPair

	locator bakery.ThirdPartyLocator
}

// newMacaroonStore returns a MacaroonOpStore implementation
// that stores root keys in memory and puts all operations
// in the macaroon id.
func newMacaroonStore(key *bakery.KeyPair, locator bakery.ThirdPartyLocator) *macaroonStore {
	return &macaroonStore{
		rootKeyStore: bakery.NewMemRootKeyStore(),
		key:          key,
		locator:      locator,
	}
}

type macaroonId struct {
	Id  []byte
	Ops []bakery.Op
}

func (s *macaroonStore) NewMacaroon(ctxt context.Context, ops []bakery.Op, caveats []checkers.Caveat, ns *checkers.Namespace) (*macaroon.Macaroon, error) {
	rootKey, id, err := s.rootKeyStore.RootKey(ctxt)
	if err != nil {
		return nil, errgo.Mask(err)
	}

	mid := macaroonId{
		Id:  id,
		Ops: ops,
	}
	data, _ := json.Marshal(mid)
	m, err := macaroon.New(rootKey, data, "", macaroon.LatestVersion)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	for _, cav := range caveats {
		if err := bakery.AddCaveat(ctxt, s.key, s.locator, m, cav, ns); err != nil {
			return nil, errgo.Notef(err, "cannot add caveat")
		}
	}
	return m, nil
}

func (s *macaroonStore) MacaroonOps(ctxt context.Context, ms macaroon.Slice) (ops []bakery.Op, conditions []string, err error) {
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
	rootKey, err := s.rootKeyStore.Get(ctxt, mid.Id)
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

// macaroonStoreWithError is an implementation of MacaroonOpStore that
// returns the given error on all store operations.
type macaroonStoreWithError struct {
	err error
}

func (s macaroonStoreWithError) NewMacaroon(ctxt context.Context, ops []bakery.Op, caveats []checkers.Caveat, ns *checkers.Namespace) (*macaroon.Macaroon, error) {
	return nil, errgo.Mask(s.err, errgo.Any)
}

func (s macaroonStoreWithError) MacaroonOps(ctxt context.Context, ms macaroon.Slice) (ops []bakery.Op, conditions []string, err error) {
	return nil, nil, errgo.Mask(s.err, errgo.Any)
}

func withoutLoginOp(ops []bakery.Op) []bakery.Op {
	// Remove LoginOp from the operations associated with the new macaroon.
	hasLoginOp := false
	for _, op := range ops {
		if op == bakery.LoginOp {
			hasLoginOp = true
			break
		}
	}
	if !hasLoginOp {
		return ops
	}
	newOps := make([]bakery.Op, 0, len(ops))
	for _, op := range ops {
		if op != bakery.LoginOp {
			newOps = append(newOps, op)
		}
	}
	return newOps
}
