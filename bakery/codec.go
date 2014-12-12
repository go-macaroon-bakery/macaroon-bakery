package bakery

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"code.google.com/p/go.crypto/nacl/box"

	"gopkg.in/macaroon-bakery.v0/bakery/checkers"
)

type caveatIdRecord struct {
	RootKey   []byte
	Condition string
}

// caveatId defines the format of a third party caveat id.
type caveatId struct {
	ThirdPartyPublicKey *PublicKey
	FirstPartyPublicKey *PublicKey
	Nonce               []byte
	Id                  string
}

// boxEncoder encodes caveat ids confidentially to a third-party service using
// authenticated public key encryption compatible with NaCl box.
type boxEncoder struct {
	locator PublicKeyLocator
	key     *KeyPair
}

// newBoxEncoder creates a new boxEncoder with the given public key pair and
// third-party public key locator function.
func newBoxEncoder(locator PublicKeyLocator, key *KeyPair) *boxEncoder {
	return &boxEncoder{
		key:     key,
		locator: locator,
	}
}

func (enc *boxEncoder) encodeCaveatId(cav checkers.Caveat, rootKey []byte) (string, error) {
	if cav.Location == "" {
		return "", fmt.Errorf("cannot make caveat id for first party caveat")
	}
	thirdPartyPub, err := enc.locator.PublicKeyForLocation(cav.Location)
	if err != nil {
		return "", err
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

func (enc *boxEncoder) newCaveatId(cav checkers.Caveat, rootKey []byte, thirdPartyPub *PublicKey) (*caveatId, error) {
	var nonce [NonceLen]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("cannot generate random number for nonce: %v", err)
	}
	plain := caveatIdRecord{
		RootKey:   rootKey,
		Condition: cav.Condition,
	}
	plainData, err := json.Marshal(&plain)
	if err != nil {
		return nil, fmt.Errorf("cannot marshal %#v: %v", &plain, err)
	}
	sealed := box.Seal(nil, plainData, &nonce, thirdPartyPub.boxKey(), enc.key.Private.boxKey())
	return &caveatId{
		ThirdPartyPublicKey: thirdPartyPub,
		FirstPartyPublicKey: &enc.key.Public,
		Nonce:               nonce[:],
		Id:                  base64.StdEncoding.EncodeToString(sealed),
	}, nil
}

// boxDecoder decodes caveat ids for third-party service that were encoded to
// the third-party with authenticated public key encryption compatible with
// NaCl box.
type boxDecoder struct {
	key *KeyPair
}

// newBoxDecoder creates a new BoxDecoder using the given key pair.
func newBoxDecoder(key *KeyPair) *boxDecoder {
	return &boxDecoder{
		key: key,
	}
}

func (d *boxDecoder) decodeCaveatId(id string) (rootKey []byte, condition string, err error) {
	data, err := base64.StdEncoding.DecodeString(id)
	if err != nil {
		return nil, "", fmt.Errorf("cannot base64-decode caveat id: %v", err)
	}
	var tpid caveatId
	if err := json.Unmarshal(data, &tpid); err != nil {
		return nil, "", fmt.Errorf("cannot unmarshal caveat id %q: %v", data, err)
	}
	var recordData []byte

	recordData, err = d.encryptedCaveatId(tpid)
	if err != nil {
		return nil, "", err
	}
	var record caveatIdRecord
	if err := json.Unmarshal(recordData, &record); err != nil {
		return nil, "", fmt.Errorf("cannot decode third party caveat record: %v", err)
	}
	return record.RootKey, record.Condition, nil
}

func (d *boxDecoder) encryptedCaveatId(id caveatId) ([]byte, error) {
	if d.key == nil {
		return nil, fmt.Errorf("no public key for caveat id decryption")
	}
	if !bytes.Equal(d.key.Public.Key[:], id.ThirdPartyPublicKey.Key[:]) {
		return nil, fmt.Errorf("public key mismatch")
	}
	var nonce [NonceLen]byte
	if len(id.Nonce) != len(nonce) {
		return nil, fmt.Errorf("bad nonce length")
	}
	copy(nonce[:], id.Nonce)

	sealed, err := base64.StdEncoding.DecodeString(id.Id)
	if err != nil {
		return nil, fmt.Errorf("cannot base64-decode encrypted caveat id: %v", err)
	}
	out, ok := box.Open(nil, sealed, &nonce, id.FirstPartyPublicKey.boxKey(), d.key.Private.boxKey())
	if !ok {
		return nil, fmt.Errorf("decryption of public-key encrypted caveat id %#v failed", id)
	}
	return out, nil
}
