package httpbakery

import (
	"strings"
	"sync"

	"github.com/rogpeppe/macaroon/caveatid"
)

type publicKeyRecord struct {
	location string
	prefix   bool
	key      [caveatid.KeyLen]byte
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

func (kr *PublicKeyRing) addPublicKeyForLocation(loc string, prefix bool, key *[caveatid.KeyLen]byte) {
	kr.mu.Lock()
	defer kr.mu.Unlock()
	kr.publicKeys = append(kr.publicKeys, publicKeyRecord{
		location: loc,
		prefix:   prefix,
		key:      *key,
	})
}

// PublicKeyForLocation returns the public key for the given location, if
// found. This method is an instance of caveatid.PublicKeyLocatorFunc, and is
// suitable for use with caveatid.BoxEncoder.
func (kr *PublicKeyRing) PublicKeyForLocation(loc string) *[caveatid.KeyLen]byte {
	kr.mu.Lock()
	defer kr.mu.Unlock()
	var (
		longestPrefix    string
		longestPrefixKey *[caveatid.KeyLen]byte // public key associated with longest prefix
	)
	for i := len(kr.publicKeys) - 1; i >= 0; i-- {
		k := kr.publicKeys[i]
		if k.location == loc && !k.prefix {
			return &k.key
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
		return nil
	}
	return longestPrefixKey
}
