package bakery

import (
	"crypto/rand"
	"encoding/base64"
	"strings"

	"golang.org/x/crypto/nacl/box"
	"golang.org/x/net/context"
	"gopkg.in/errgo.v1"
)

// KeyLen is the byte length of the Ed25519 public and private keys used for
// caveat id encryption.
const KeyLen = 32

// NonceLen is the byte length of the nonce values used for caveat id
// encryption.
const NonceLen = 24

// PublicKey is a 256-bit Ed25519 public key.
type PublicKey struct {
	Key
}

// PrivateKey is a 256-bit Ed25519 private key.
type PrivateKey struct {
	Key
}

// Key is a 256-bit Ed25519 key.
type Key [KeyLen]byte

// String returns the base64 representation of the key.
func (k Key) String() string {
	return base64.StdEncoding.EncodeToString(k[:])
}

// MarshalBinary implements encoding.BinaryMarshaler.MarshalBinary.
func (k Key) MarshalBinary() ([]byte, error) {
	return k[:], nil
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler.UnmarshalBinary.
func (k *Key) UnmarshalBinary(data []byte) error {
	if len(data) != len(k) {
		return errgo.Newf("wrong length for key, got %d want %d", len(data), len(k))
	}
	copy(k[:], data)
	return nil
}

// MarshalText implements encoding.TextMarshaler.MarshalText.
func (k Key) MarshalText() ([]byte, error) {
	data := make([]byte, base64.StdEncoding.EncodedLen(len(k)))
	base64.StdEncoding.Encode(data, k[:])
	return data, nil
}

// boxKey returns the box package's type for a key.
func (k Key) boxKey() *[KeyLen]byte {
	return (*[KeyLen]byte)(&k)
}

// UnmarshalText implements encoding.TextUnmarshaler.UnmarshalText.
func (k *Key) UnmarshalText(text []byte) error {
	// Note: we cannot decode directly into key because
	// DecodedLen can return more than the actual number
	// of bytes that will be required.
	data := make([]byte, base64.StdEncoding.DecodedLen(len(text)))
	n, err := base64.StdEncoding.Decode(data, text)
	if err != nil {
		return errgo.Notef(err, "cannot decode base64 key")
	}
	if n != len(k) {
		return errgo.Newf("wrong length for base64 key, got %d want %d", n, len(k))
	}
	copy(k[:], data[0:n])
	return nil
}

// ThirdPartyInfo holds information on a given third party
// discharge service.
type ThirdPartyInfo struct {
	// PublicKey holds the public key of the third party.
	PublicKey PublicKey

	// Version holds the latest bakery protocol version supported
	// by the discharger.
	Version Version
}

// ThirdPartyLocator is used to find information on third
// party discharge services.
type ThirdPartyLocator interface {
	// ThirdPartyInfo returns information on the third
	// party at the given location. It returns ErrNotFound if no match is found.
	ThirdPartyInfo(ctx context.Context, loc string) (ThirdPartyInfo, error)
}

// ThirdPartyLocatorMap implements a simple ThirdPartyLocator.
// A trailing slash on locations is ignored.
type ThirdPartyStore struct {
	m map[string]ThirdPartyInfo
}

// NewThirdPartyStore returns a new instance of ThirdPartyStore
// that stores locations in memory.
func NewThirdPartyStore() *ThirdPartyStore {
	return &ThirdPartyStore{
		m: make(map[string]ThirdPartyInfo),
	}
}

// AddInfo associates the given information with the
// given location, ignoring any trailing slash.
func (s *ThirdPartyStore) AddInfo(loc string, info ThirdPartyInfo) {
	s.m[canonicalLocation(loc)] = info
}

func canonicalLocation(loc string) string {
	return strings.TrimSuffix(loc, "/")
}

// ThirdPartyInfo implements the ThirdPartyLocator interface.
func (s *ThirdPartyStore) ThirdPartyInfo(ctx context.Context, loc string) (ThirdPartyInfo, error) {
	if info, ok := s.m[canonicalLocation(loc)]; ok {
		return info, nil
	}
	return ThirdPartyInfo{}, ErrNotFound
}

// KeyPair holds a public/private pair of keys.
type KeyPair struct {
	Public  PublicKey  `json:"public"`
	Private PrivateKey `json:"private"`
}

// GenerateKey generates a new key pair.
func GenerateKey() (*KeyPair, error) {
	var key KeyPair
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	key.Public = PublicKey{*pub}
	key.Private = PrivateKey{*priv}
	return &key, nil
}

// MustGenerateKey is like GenerateKey but panics if GenerateKey returns
// an error - useful in tests.
func MustGenerateKey() *KeyPair {
	key, err := GenerateKey()
	if err != nil {
		panic(errgo.Notef(err, "cannot generate key"))
	}
	return key
}

// String implements the fmt.Stringer interface
// by returning the base64 representation of the
// public key part of key.
func (key *KeyPair) String() string {
	return key.Public.String()
}

type emptyLocator struct{}

func (emptyLocator) ThirdPartyInfo(context.Context, string) (ThirdPartyInfo, error) {
	return ThirdPartyInfo{}, ErrNotFound
}
