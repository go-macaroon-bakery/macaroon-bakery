package caveatid

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"code.google.com/p/go.crypto/nacl/box"

	"github.com/rogpeppe/macaroon/bakery"
)

// PublicKeyLocatorFunc defines a function which provides an Ed25519 (256-bit)
// public key for the given location string.
type PublicKeyLocatorFunc func(loc string) *[KeyLen]byte

// MapPublicKeyLocator creates a PublicKeyLocatorFunc which returns the public
// key matching the given location, according to the provided mapping.
func MapPublicKeyLocator(ref map[string]*[KeyLen]byte) PublicKeyLocatorFunc {
	m := make(map[string]*[KeyLen]byte)
	for k, vref := range ref {
		v := new([KeyLen]byte)
		copy(v[:], vref[:])
		m[k] = v
	}
	return func(loc string) *[KeyLen]byte {
		return m[loc]
	}
}

// CaveatIdRecord contains the third-party caveat root key and condition.  This
// must be encoded confidentially from the first-party adding the caveat, to
// the third-party service.
type CaveatIdRecord struct {
	RootKey   []byte
	Condition string
}

// CaveatId defines the format of a third party caveat id.  Id contains a
// serialized CaveatIdRecord, encrypted to ThirdPartyPublicKey and
// base64-encoded.
type CaveatId struct {
	ThirdPartyPublicKey []byte
	FirstPartyPublicKey []byte
	Nonce               []byte
	Id                  string
}

// BoxEncoder encodes caveat ids confidentially to a third-party service using
// authenticated public key encryption compatible with NaCl box.
type BoxEncoder struct {
	locatePublicKey PublicKeyLocatorFunc
	key             *KeyPair
}

// NewBoxEncoder creates a new BoxEncoder with the given public key pair and
// third-party public key locator function.
func NewBoxEncoder(locatorFunc PublicKeyLocatorFunc, key *KeyPair) *BoxEncoder {
	return &BoxEncoder{
		key:             key,
		locatePublicKey: locatorFunc,
	}
}

// EncodeCaveatId implements bakery.CaveatIdEncoder.EncodeCaveatId.
func (enc *BoxEncoder) EncodeCaveatId(cav bakery.Caveat, rootKey []byte) (string, error) {
	if cav.Location == "" {
		return "", fmt.Errorf("cannot make caveat id for first party caveat")
	}
	thirdPartyPub := enc.locatePublicKey(cav.Location)
	if thirdPartyPub == nil {
		return "", fmt.Errorf("public key not found for location %q", cav.Location)
	}
	id, err := enc.newCaveatId(cav, rootKey, thirdPartyPub)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(id)
	if err != nil {
		return "", fmt.Errorf("cannot marshal %#v: %v", id, err)
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

func (enc *BoxEncoder) newCaveatId(cav bakery.Caveat, rootKey []byte, thirdPartyPub *[KeyLen]byte) (*CaveatId, error) {
	var nonce [NonceLen]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("cannot generate random number for nonce: %v", err)
	}
	plain := CaveatIdRecord{
		RootKey:   rootKey,
		Condition: cav.Condition,
	}
	plainData, err := json.Marshal(&plain)
	if err != nil {
		return nil, fmt.Errorf("cannot marshal %#v: %v", &plain, err)
	}
	sealed := box.Seal(nil, plainData, &nonce, thirdPartyPub, enc.key.PrivateKey())
	return &CaveatId{
		ThirdPartyPublicKey: thirdPartyPub[:],
		FirstPartyPublicKey: enc.key.PublicKey()[:],
		Nonce:               nonce[:],
		Id:                  base64.StdEncoding.EncodeToString(sealed),
	}, nil
}

// BoxDecoder decodes caveat ids for third-party service that were encoded to
// the third-party with authenticated public key encryption compatible with
// NaCl box.
type BoxDecoder struct {
	key *KeyPair
}

// NewBoxDecoder creates a new BoxDecoder using the given key pair.
func NewBoxDecoder(key *KeyPair) bakery.CaveatIdDecoder {
	return &BoxDecoder{
		key: key,
	}
}

// DecodeCaveatId implements bakery.CaveatIdDecoder.DecodeCaveatId.
func (d *BoxDecoder) DecodeCaveatId(id string) (rootKey []byte, condition string, err error) {
	data, err := base64.StdEncoding.DecodeString(id)
	if err != nil {
		return nil, "", fmt.Errorf("cannot base64-decode caveat id: %v", err)
	}
	var tpid CaveatId
	if err := json.Unmarshal(data, &tpid); err != nil {
		return nil, "", fmt.Errorf("cannot unmarshal caveat id %q: %v", data, err)
	}
	var recordData []byte

	recordData, err = d.encryptedCaveatId(tpid)
	if err != nil {
		return nil, "", err
	}
	var record CaveatIdRecord
	if err := json.Unmarshal(recordData, &record); err != nil {
		return nil, "", fmt.Errorf("cannot decode third party caveat record: %v", err)
	}
	return record.RootKey, record.Condition, nil
}

func (d *BoxDecoder) encryptedCaveatId(id CaveatId) ([]byte, error) {
	if d.key == nil {
		return nil, fmt.Errorf("no public key for caveat id decryption")
	}
	if !bytes.Equal(d.key.PublicKey()[:], id.ThirdPartyPublicKey) {
		return nil, fmt.Errorf("public key mismatch")
	}
	var nonce [NonceLen]byte
	if len(id.Nonce) != len(nonce) {
		return nil, fmt.Errorf("bad nonce length")
	}
	copy(nonce[:], id.Nonce)

	var firstPartyPublicKey [KeyLen]byte
	if len(id.FirstPartyPublicKey) != len(firstPartyPublicKey) {
		return nil, fmt.Errorf("bad public key length")
	}
	copy(firstPartyPublicKey[:], id.FirstPartyPublicKey)

	sealed, err := base64.StdEncoding.DecodeString(id.Id)
	if err != nil {
		return nil, fmt.Errorf("cannot base64-decode encrypted caveat id", err)
	}
	out, ok := box.Open(nil, sealed, &nonce, &firstPartyPublicKey, d.key.PrivateKey())
	if !ok {
		return nil, fmt.Errorf("decryption of public-key encrypted caveat id %#v failed", id)
	}
	return out, nil
}
