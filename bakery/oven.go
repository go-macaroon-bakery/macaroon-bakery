package bakery

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"sort"
	"strings"
	"time"

	"github.com/rogpeppe/fastuuid"
	"golang.org/x/net/context"
	errgo "gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v2-unstable"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2-unstable/bakery/internal/macaroonpb"
)

var uuidGen = fastuuid.MustNewGenerator()

// Oven bakes macaroons. They emerge sweet and delicious
// and ready for use in a Checker.
//
// All macaroons are associated with one or more operations (see
// the Op type) which define the capabilities of the macaroon.
//
// There is one special operation, "login" (defined by LoginOp)
// which grants the capability to speak for a particular user.
// The login capability will never be mixed with other capabilities.
//
// It is up to the caller to decide on semantics for other operations.
type Oven struct {
	p OvenParams
}

type OvenParams struct {
	// Namespace holds the namespace to use when adding first party caveats.
	// If this is nil, checkers.New(nil).Namespace will be used.
	Namespace *checkers.Namespace

	// RootKeyStoreForEntity returns the macaroon storage to be
	// used for root keys associated with macaroons created
	// wth NewMacaroon.
	//
	// If this is nil, NewMemRootKeyStore will be used to create
	// a new store to be used for all entities.
	RootKeyStoreForOps func(ops []Op) RootKeyStore

	// OpsStore is used to persistently store the association of
	// multi-op entities with their associated operations
	// when NewMacaroon is called with multiple operations.
	//
	// If this is nil, embed the operations will be stored directly in the macaroon id.
	// Note that this can make the macaroons large.
	//
	// When this is in use, operation entities with the prefix "multi-" are
	// reserved - a "multi-"-prefixed entity represents a set of operations
	// stored in the OpsStore.
	OpsStore OpsStore

	// Key holds the private key pair used to encrypt third party caveats.
	// If it is nil, no third party caveats can be created.
	Key *KeyPair

	// Location holds the location that will be associated with new macaroons
	// (as returned by Macaroon.Location).
	Location string

	// Locator is used to find out information on third parties when
	// adding third party caveats. If this is nil, no non-local third
	// party caveats can be added.
	Locator ThirdPartyLocator

	// TODO max macaroon or macaroon id size?
}

// NewOven returns a new oven using the given parameters.
func NewOven(p OvenParams) *Oven {
	if p.Locator == nil {
		p.Locator = emptyLocator{}
	}
	if p.RootKeyStoreForOps == nil {
		store := NewMemRootKeyStore()
		p.RootKeyStoreForOps = func(ops []Op) RootKeyStore {
			return store
		}
	}
	if p.Namespace == nil {
		p.Namespace = checkers.New(nil).Namespace()
	}
	return &Oven{
		p: p,
	}
}

// MacaroonOps implements MacaroonOpStore.MacaroonOps, making Oven
// an instance of MacaroonOpStore.
//
// For macaroons minted with previous bakery versions, it always
// returns a single LoginOp operation.
func (o *Oven) MacaroonOps(ctx context.Context, ms macaroon.Slice) (ops []Op, conditions []string, err error) {
	if len(ms) == 0 {
		return nil, nil, errgo.Newf("no macaroons in slice")
	}
	storageId, ops, err := decodeMacaroonId(ms[0].Id())
	if err != nil {
		return nil, nil, errgo.Mask(err)
	}
	rootKey, err := o.p.RootKeyStoreForOps(ops).Get(ctx, storageId)
	if err != nil {
		if errgo.Cause(err) != ErrNotFound {
			return nil, nil, errgo.Notef(err, "cannot get macaroon")
		}
		// If the macaroon was not found, it is probably
		// because it's been removed after time-expiry,
		// so return a verification error.
		return nil, nil, &VerificationError{
			Reason: errgo.Newf("macaroon not found in storage"),
		}
	}
	conditions, err = ms[0].VerifySignature(rootKey, ms[1:])
	if err != nil {
		return nil, nil, &VerificationError{
			Reason: errgo.Mask(err),
		}
	}
	if o.p.OpsStore != nil && len(ops) == 1 && strings.HasPrefix(ops[0].Entity, "multi-") {
		// It's a multi-op entity, so retrieve the actual operations it's associated with.
		ops, err = o.p.OpsStore.GetOps(ctx, ops[0].Entity)
		if err != nil {
			return nil, nil, errgo.Notef(err, "cannot get operations for %q", ops[0].Entity)
		}
	}
	return ops, conditions, nil
}

func decodeMacaroonId(id []byte) (storageId []byte, ops []Op, err error) {
	base64Decoded := false
	if id[0] == 'A' {
		// The first byte is not a version number and it's 'A', which is the
		// base64 encoding of the top 6 bits (all zero) of the version number 2 or 3,
		// so we assume that it's the base64 encoding of a new-style
		// macaroon id, so we base64 decode it.
		//
		// Note that old-style ids always start with an ASCII character >= 4
		// (> 32 in fact) so this logic won't be triggered for those.
		dec := make([]byte, base64.RawURLEncoding.DecodedLen(len(id)))
		n, err := base64.RawURLEncoding.Decode(dec, id)
		if err == nil {
			// Set the id only on success - if it's a bad encoding, we'll get a not-found error
			// which is fine because "not found" is a correct description of the issue - we
			// can't find the root key for the given id.
			id = dec[0:n]
			base64Decoded = true
		}
	}
	// Trim any extraneous information from the id before retrieving
	// it from storage, including the UUID that's added when
	// creating macaroons to make all macaroons unique even if
	// they're using the same root key.
	switch id[0] {
	case byte(Version2):
		// Skip the UUID at the start of the id.
		storageId = id[1+16:]
	case byte(Version3):
		var id1 macaroonpb.MacaroonId
		if err := id1.UnmarshalBinary(id[1:]); err != nil {
			return nil, nil, errgo.Notef(err, "cannot unmarshal macaroon id")
		}
		if len(id1.Ops) == 0 || len(id1.Ops[0].Actions) == 0 {
			return nil, nil, errgo.Newf("no operations found in macaroon")
		}
		ops = make([]Op, 0, len(id1.Ops))
		for _, op := range id1.Ops {
			for _, action := range op.Actions {
				ops = append(ops, Op{
					Entity: op.Entity,
					Action: action,
				})
			}
		}
		return id1.StorageId, ops, nil
	}
	if !base64Decoded && isLowerCaseHexChar(id[0]) {
		// It's an old-style id, probably with a hyphenated UUID.
		// so trim that off.
		if i := bytes.LastIndexByte(id, '-'); i >= 0 {
			storageId = id[0:i]
		}
	}
	return storageId, []Op{LoginOp}, nil
}

// NewMacaroon takes a macaroon with the given version from the oven, associates it with the given operations
// and attaches the given caveats. There must be at least one operation specified.
//
// The macaroon will expire at the given time - a TimeBefore first party caveat will be added with
// that time.
func (o *Oven) NewMacaroon(ctx context.Context, version Version, expiry time.Time, caveats []checkers.Caveat, ops ...Op) (*Macaroon, error) {
	if len(ops) == 0 {
		return nil, errgo.Newf("cannot mint a macaroon associated with no operations")
	}
	ops = CanonicalOps(ops)
	rootKey, storageId, err := o.p.RootKeyStoreForOps(ops).RootKey(ctx)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	id, err := o.newMacaroonId(ctx, ops, storageId, expiry)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	idBytesNoVersion, err := id.MarshalBinary()
	if err != nil {
		return nil, errgo.Mask(err)
	}
	idBytes := make([]byte, len(idBytesNoVersion)+1)
	idBytes[0] = byte(LatestVersion)
	// TODO We could use a proto.Buffer to avoid this copy.
	copy(idBytes[1:], idBytesNoVersion)

	if MacaroonVersion(version) < macaroon.V2 {
		// The old macaroon format required valid text for the macaroon id,
		// so base64-encode it.
		b64data := make([]byte, base64.RawURLEncoding.EncodedLen(len(idBytes)))
		base64.RawURLEncoding.Encode(b64data, idBytes)
		idBytes = b64data
	}
	m, err := NewMacaroon(rootKey, idBytes, o.p.Location, version, o.p.Namespace)
	if err != nil {
		return nil, errgo.Notef(err, "cannot create macaroon with version %v", version)
	}
	if err := o.AddCaveat(ctx, m, checkers.TimeBeforeCaveat(expiry)); err != nil {
		return nil, errgo.Mask(err)
	}
	if err := o.AddCaveats(ctx, m, caveats); err != nil {
		return nil, errgo.Mask(err)
	}
	return m, nil
}

// AddCaveat adds a caveat to the given macaroon.
func (o *Oven) AddCaveat(ctx context.Context, m *Macaroon, cav checkers.Caveat) error {
	return m.AddCaveat(ctx, cav, o.p.Key, o.p.Locator)
}

// AddCaveats adds all the caveats to the given macaroon.
func (o *Oven) AddCaveats(ctx context.Context, m *Macaroon, caveats []checkers.Caveat) error {
	return m.AddCaveats(ctx, caveats, o.p.Key, o.p.Locator)
}

// Key returns the oven's private/public key par.
func (o *Oven) Key() *KeyPair {
	return o.p.Key
}

// Locator returns the third party locator that the
// oven was created with.
func (o *Oven) Locator() ThirdPartyLocator {
	return o.p.Locator
}

// CanonicalOps returns the given operations slice sorted
// with duplicates removed.
func CanonicalOps(ops []Op) []Op {
	canonOps := opsByValue(ops)
	needNewSlice := false
	for i := 1; i < len(ops); i++ {
		if !canonOps.Less(i-1, i) {
			needNewSlice = true
			break
		}
	}
	if !needNewSlice {
		return ops
	}
	canonOps = make([]Op, len(ops))
	copy(canonOps, ops)
	sort.Sort(canonOps)

	// Note we know that there's at least one operation here
	// because we'd have returned earlier if the slice was empty.
	j := 0
	for _, op := range canonOps[1:] {
		if op != canonOps[j] {
			j++
			canonOps[j] = op
		}
	}
	return canonOps[0 : j+1]
}

func (o *Oven) newMacaroonId(ctx context.Context, ops []Op, storageId []byte, expiry time.Time) (*macaroonpb.MacaroonId, error) {
	uuid := uuidGen.Next()
	nonce := uuid[0:16]
	if len(ops) == 1 || o.p.OpsStore == nil {
		return &macaroonpb.MacaroonId{
			Nonce:     nonce,
			StorageId: storageId,
			Ops:       macaroonIdOps(ops),
		}, nil
	}
	// We've got several operations and a multi-op store, so use the store.
	// TODO use the store only if the encoded macaroon id exceeds some size?
	entity := newOpsEntity(ops)
	if err := o.p.OpsStore.PutOps(ctx, entity, ops, expiry); err != nil {
		return nil, errgo.Notef(err, "cannot store ops")
	}
	return &macaroonpb.MacaroonId{
		Nonce:     nonce,
		StorageId: storageId,
		Ops: []*macaroonpb.Op{{
			Entity:  entity,
			Actions: []string{"*"},
		}},
	}, nil
}

// newOpsEntity returns a new multi-op entity name that represents
// all the given operations and caveats. It returns the same value regardless
// of the ordering of the operations. It assumes that the operations
// have been canonicalized and that there's at least one operation.
func newOpsEntity(ops []Op) string {
	// Hash the operations, removing duplicates as we go.
	h := sha256.New()
	data := make([]byte, len(ops[0].Action)+len(ops[0].Entity)+2)
	for _, op := range ops {
		data = data[:0]
		data = append(data, op.Action...)
		data = append(data, '\n')
		data = append(data, op.Entity...)
		data = append(data, '\n')
		h.Write(data)
	}
	entity := make([]byte, len("multi-")+base64.RawURLEncoding.EncodedLen(sha256.Size))
	copy(entity, "multi-")
	base64.RawURLEncoding.Encode(entity[len("multi-"):], h.Sum(data[:0]))
	return string(entity)
}

// macaroonIdOps returns operations suitable for serializing
// as part of an *macaroonpb.MacaroonId. It assumes that
// ops has been canonicalized and that there's at least
// one operation.
func macaroonIdOps(ops []Op) []*macaroonpb.Op {
	idOps := make([]macaroonpb.Op, 0, len(ops))
	idOps = append(idOps, macaroonpb.Op{
		Entity:  ops[0].Entity,
		Actions: []string{ops[0].Action},
	})
	i := 0
	idOp := &idOps[0]
	for _, op := range ops[1:] {
		if op.Entity != idOp.Entity {
			idOps = append(idOps, macaroonpb.Op{
				Entity:  op.Entity,
				Actions: []string{op.Action},
			})
			i++
			idOp = &idOps[i]
			continue
		}
		if op.Action != idOp.Actions[len(idOp.Actions)-1] {
			idOp.Actions = append(idOp.Actions, op.Action)
		}
	}
	idOpPtrs := make([]*macaroonpb.Op, len(idOps))
	for i := range idOps {
		idOpPtrs[i] = &idOps[i]
	}
	return idOpPtrs
}

type opsByValue []Op

func (o opsByValue) Less(i, j int) bool {
	o0, o1 := o[i], o[j]
	if o0.Entity != o1.Entity {
		return o0.Entity < o1.Entity
	}
	return o0.Action < o1.Action
}

func (o opsByValue) Swap(i, j int) {
	o[i], o[j] = o[j], o[i]
}

func (o opsByValue) Len() int {
	return len(o)
}

func isLowerCaseHexChar(c byte) bool {
	switch {
	case '0' <= c && c <= '9':
		return true
	case 'a' <= c && c <= 'f':
		return true
	}
	return false
}
