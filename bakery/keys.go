package bakery

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"

	"code.google.com/p/go.crypto/nacl/box"
)

// KeyLen is the byte length of the Ed25519 public and private keys used for
// caveat id encryption.
const KeyLen = 32

// NonceLen is the byte length of the nonce values used for caveat id
// encryption.
const NonceLen = 24

// PublicKey is a 256-bit Ed25519 public key.
type PublicKey [KeyLen]byte

// Key is a 256-bit Ed25519 private key.
type Key [KeyLen]byte

// PublicKeyLocator is used to find the public key for a given
// caveat or macaroon location.
type PublicKeyLocator interface {
	// PublicKeyForLocation returns the public key matching the caveat or
	// macaroon location. It returns ErrNotFound if no match is found.
	PublicKeyForLocation(loc string) (*PublicKey, error)
}

// PublicKeyLocatorMap implements PublicKeyLocator for a map.
// Each entry in the map holds a public key value for
// a location named by the map key.
type PublicKeyLocatorMap map[string]*PublicKey

// PublicKeyForLocation implements the PublicKeyLocator interface.
func (m PublicKeyLocatorMap) PublicKeyForLocation(loc string) (*PublicKey, error) {
	if pk, ok := m[loc]; ok {
		return pk, nil
	}
	return nil, ErrNotFound
}

// KeyPair holds a public/private pair of keys.
// TODO(rog) marshal/unmarshal functions for KeyPair
type KeyPair struct {
	Public  PublicKey
	Private Key
}

// GenerateKey generates a new key pair.
func GenerateKey() (*KeyPair, error) {
	var key KeyPair
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	key.Public = PublicKey(*pub)
	key.Private = *priv
	return &key, nil
}

// String implements the fmt.Stringer interface.
func (key *KeyPair) String() string {
	return hex.EncodeToString(key.Public[:])
}

type publicKeyRecord struct {
	location string
	prefix   bool
	key      PublicKey
}

// PublicKeyRing stores public keys for third-party services, accessible by
// location string.
type PublicKeyRing struct {
	// mu guards the fields following it.
	mu sync.Mutex

	// TODO(rog) use a more efficient data structure
	publicKeys []publicKeyRecord
}

// NewPublicKeyRing returns a new PublicKeyRing instance.
func NewPublicKeyRing() *PublicKeyRing {
	return &PublicKeyRing{}
}

// AddPublicKeyForLocation adds a public key to the keyring for the given
// location or location prefix.
// It is safe to call methods concurrently on this type.
func (kr *PublicKeyRing) AddPublicKeyForLocation(loc string, prefix bool, key *PublicKey) {
	kr.mu.Lock()
	defer kr.mu.Unlock()
	kr.publicKeys = append(kr.publicKeys, publicKeyRecord{
		location: loc,
		prefix:   prefix,
		key:      *key,
	})
}

// PublicKeyForLocation implements the PublicKeyLocator interface.
func (kr *PublicKeyRing) PublicKeyForLocation(loc string) (*PublicKey, error) {
	kr.mu.Lock()
	defer kr.mu.Unlock()
	var (
		longestPrefix    string
		longestPrefixKey *PublicKey // public key associated with longest prefix
	)
	for i := len(kr.publicKeys) - 1; i >= 0; i-- {
		k := kr.publicKeys[i]
		if k.location == loc && !k.prefix {
			return &k.key, nil
		}
		if !k.prefix {
			continue
		}
		if strings.HasPrefix(loc, k.location) && len(k.location) > len(longestPrefix) {
			longestPrefix = k.location
			longestPrefixKey = &k.key
		}
	}
	if len(longestPrefix) == 0 {
		return nil, ErrNotFound
	}
	return longestPrefixKey, nil
}
