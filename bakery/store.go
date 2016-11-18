package bakery

import (
	"sync"
	"time"

	"golang.org/x/net/context"
	"gopkg.in/macaroon.v2-unstable"
)

// RootKeyStore defines store for macaroon root keys.
type RootKeyStore interface {
	// Get returns the root key for the given id.
	// If the item is not there, it returns ErrNotFound.
	Get(ctxt context.Context, id []byte) ([]byte, error)

	// RootKey returns the root key to be used for making a new
	// macaroon, and an id that can be used to look it up later with
	// the Get method.
	//
	// Note that the root keys should remain available for as long
	// as the macaroons using them are valid.
	//
	// Note that there is no need for it to return a new root key
	// for every call - keys may be reused, although some key
	// cycling is over time is advisable.
	RootKey(ctxt context.Context) (rootKey []byte, id []byte, err error)
}

// NewMemRootKeyStore returns an implementation of
// Store that generates a single key and always
// returns that from RootKey. The same id ("0") is always
// used.
func NewMemRootKeyStore() RootKeyStore {
	return new(memRootKeyStore)
}

type memRootKeyStore struct {
	mu  sync.Mutex
	key []byte
}

// Get implements Store.Get.
func (s *memRootKeyStore) Get(_ context.Context, id []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(id) != 1 || id[0] != '0' || s.key == nil {
		return nil, ErrNotFound
	}
	return s.key, nil
}

// RootKey implements Store.RootKey by always returning the same root
// key.
func (s *memRootKeyStore) RootKey(context.Context) (rootKey, id []byte, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key == nil {
		newKey, err := randomBytes(24)
		if err != nil {
			return nil, nil, err
		}
		s.key = newKey
	}
	return s.key, []byte("0"), nil
}

// MacaroonOpStore provides a mapping from a set of macaroons to their
// associated operations and caveats.
type MacaroonOpStore interface {
	// MacaroonOps verifies the signature of the given macaroon and returns
	// information on its associated operations, and all the first party
	// caveat conditions that need to be checked.
	//
	// This method should not check first party caveats itself.
	//
	// It should return a *VerificationError if the error occurred
	// because the macaroon signature failed or the root key
	// was not found - any other error will be treated as fatal
	// by Checker and cause authorization to terminate.
	MacaroonOps(ctxt context.Context, ms macaroon.Slice) ([]Op, []string, error)
}

// OpsStore defines a persistent store for operation sets
// stored by the Oven.
//
// Implementations must be suitable for concurrent use.
type OpsStore interface {
	// PutOps creates an entry in the store associated with the given
	// key. A subsequent Get of the same key should result in the
	// same set of entities. Multiple puts of the same key should be
	// idempotent. The value associated with a given key will always
	// be the same.
	//
	// The context is derived from the context provided to Authorize
	// or Capability.
	//
	// The key must persist at least until the given expiry time.
	//
	// Implementations may assume that the operations
	// are in canonical order and contain no duplicates.
	PutOps(ctxt context.Context, key string, ops []Op, expiry time.Time) error

	// GetOps returns the set of operations for a given key.
	// If the key was not found, it should return an error with an
	// ErrNotFound cause.
	//
	// The context is derived from the context provided to Authorize or Capability.
	//
	// TODO Perhaps this should return an interface that
	// can be used to check membership rather than the
	// whole set of operations. Then an implementation
	// might be able to scale more easily to large sets of
	// operations, for example by using a bloom filter.
	GetOps(ctxt context.Context, key string) ([]Op, error)
}

type memOpsStore struct {
	mu  sync.Mutex
	ops map[string][]Op
}

// NewMemOpsStore returns a new multi-op store that stores
// the operations in memory.
func NewMemOpsStore() OpsStore {
	return &memOpsStore{
		ops: make(map[string][]Op),
	}
}

// PutOps implements OpsStore.PutOps.
// TODO implement garbage collection of old operations.
func (s *memOpsStore) PutOps(_ context.Context, key string, ops []Op, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ops[key] = append([]Op(nil), ops...)
	return nil
}

// GetOps implements OpsStore.GetOps.
func (s *memOpsStore) GetOps(_ context.Context, key string) ([]Op, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ops, ok := s.ops[key]
	if !ok {
		return nil, ErrNotFound
	}
	return ops, nil
}
