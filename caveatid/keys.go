// Package caveatid supports encoding and decoding third-party caveatids.
package caveatid

import (
	"crypto/rand"

	"code.google.com/p/go.crypto/nacl/box"
)

// KeyLen is the byte length of the Ed25519 public and private keys used for
// caveat id encryption.
const KeyLen = 32

// NonceLen is the byte length of the nonce values used for caveat id
// encryption.
const NonceLen = 24

// KeyPair holds a public/private pair of keys.
// TODO(rog) marshal/unmarshal functions for KeyPair
type KeyPair struct {
	public  [KeyLen]byte
	private [KeyLen]byte
}

// GenerateKey generates a new key pair.
func GenerateKey() (*KeyPair, error) {
	var key KeyPair
	pub, priv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	key.public = *pub
	key.private = *priv
	return &key, nil
}

// PublicKey returns the public part of the key pair.
func (key *KeyPair) PublicKey() *[KeyLen]byte {
	return &key.public
}

// PrivateKey returns the private part of the key pair.
func (key *KeyPair) PrivateKey() *[KeyLen]byte {
	return &key.private
}

// Zeroize sets the private key material to all zeros.
func (key *KeyPair) Zeroize() {
	for i := range key.private {
		key.private[i] = 0
	}
}
