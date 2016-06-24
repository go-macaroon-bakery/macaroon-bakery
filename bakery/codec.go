package bakery

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"

	"golang.org/x/crypto/nacl/box"

	"gopkg.in/errgo.v1"
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

// encodeCaveatId creates a third-party caveat with the given condtion
// and root key. The thirdPartyInfo key holds information about the
// third party we're encrypting the caveat for; the key is the
// public/private key pair of the party that's adding the caveat.
//
// The caveat id will be encoded according to the version information
// found in thirdPartyInfo.
func encodeCaveatId(
	condition string,
	rootKey []byte,
	thirdPartyInfo ThirdPartyInfo,
	key *KeyPair,
) ([]byte, error) {
	switch thirdPartyInfo.Version {
	case Version0, Version1:
		return encodeCaveatIdV1(condition, rootKey, &thirdPartyInfo.PublicKey, key)
	default:
		// It's V2 or later - use V2.
		return encodeCaveatIdV2(condition, rootKey, &thirdPartyInfo.PublicKey, key)
	}
}

// encodeCaveatIdV1 creates a JSON-encoded third-party caveat
// with the given condtion and root key. The thirdPartyPubKey key
// represents the public key of the third party we're encrypting
// the caveat for; the key is the public/private key pair of the party
// that's adding the caveat.
func encodeCaveatIdV1(
	condition string,
	rootKey []byte,
	thirdPartyPubKey *PublicKey,
	key *KeyPair,
) ([]byte, error) {
	var nonce [NonceLen]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, errgo.Notef(err, "cannot generate random number for nonce")
	}
	plain := caveatIdRecord{
		RootKey:   rootKey,
		Condition: condition,
	}
	plainData, err := json.Marshal(&plain)
	if err != nil {
		return nil, errgo.Notef(err, "cannot marshal %#v", &plain)
	}
	sealed := box.Seal(nil, plainData, &nonce, thirdPartyPubKey.boxKey(), key.Private.boxKey())
	id := caveatId{
		ThirdPartyPublicKey: thirdPartyPubKey,
		FirstPartyPublicKey: &key.Public,
		Nonce:               nonce[:],
		Id:                  base64.StdEncoding.EncodeToString(sealed),
	}
	data, err := json.Marshal(id)
	if err != nil {
		return nil, errgo.Notef(err, "cannot marshal %#v", id)
	}
	buf := make([]byte, base64.StdEncoding.EncodedLen(len(data)))
	base64.StdEncoding.Encode(buf, data)
	return buf, nil
}

const publicKeyPrefixLen = 4

// encodeCaveatIdV2 creates a version 2 third-party caveat.
//
// The v2 format has the following packed binary fields:
// version 2 [1 byte]
// first 4 bytes of third-party Curve25519 public key [4 bytes]
// first-party Curve25519 public key [32 bytes]
// nonce [24 bytes]
// encrypted secret part [rest of message]
func encodeCaveatIdV2(
	condition string,
	rootKey []byte,
	thirdPartyPubKey *PublicKey,
	key *KeyPair,
) ([]byte, error) {
	var nonce [NonceLen]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, errgo.Notef(err, "cannot generate random number for nonce")
	}
	data := make([]byte, 0, 1+publicKeyPrefixLen+KeyLen+NonceLen+1+binary.MaxVarintLen64+len(rootKey)+len(condition)+box.Overhead)
	data = append(data, byte(Version2)) //version
	data = append(data, thirdPartyPubKey.Key[:publicKeyPrefixLen]...)
	data = append(data, key.Public.Key[:]...)
	data = append(data, nonce[:]...)
	secret := encodeSecretPartV2(condition, rootKey)
	data = box.Seal(data, secret, &nonce, thirdPartyPubKey.boxKey(), key.Private.boxKey())
	return data, nil
}

// encodeSecretPartV2 creates a version 0 secret part of the third party
// caveat. The generated secret part is not encrypted.
//
// The v2 format has the following packed binary fields:
// version 2 [1 byte]
// root key [24 bytes]
// predicate [rest of message]
func encodeSecretPartV2(condition string, rootKey []byte) []byte {
	data := make([]byte, 0, 1+binary.MaxVarintLen64+len(rootKey)+len(condition))
	data = append(data, byte(Version2)) // version
	n := binary.PutUvarint(data[1:1+binary.MaxVarintLen64], uint64(len(rootKey)))
	data = data[0 : len(data)+n]
	data = append(data, rootKey...)
	data = append(data, condition...)
	return data
}

// decodeCaveatId attempts to decode id decrypting the encrypted part
// using key.
func decodeCaveatId(key *KeyPair, id []byte) (*ThirdPartyCaveatInfo, error) {
	if len(id) == 0 {
		return nil, errgo.New("caveat id empty")
	}
	switch id[0] {
	case byte(Version2):
		return decodeCaveatIdV2(key, []byte(id))
	case 'e':
		// 'e' will be the first byte if the caveatid is a base64 encoded JSON object.
		return decodeJSONCaveatId(key, id)
	default:
		return nil, errgo.Newf("caveat id has unsupported version %d", id[0])
	}
}

// decodeJSONCaveatId attempts to decode a base64 encoded JSON id. This
// encoding is nominally version -1.
func decodeJSONCaveatId(key *KeyPair, id []byte) (*ThirdPartyCaveatInfo, error) {
	data := make([]byte, (3*len(id)+3)/4)
	n, err := base64.StdEncoding.Decode(data, id)
	if err != nil {
		return nil, errgo.Notef(err, "cannot base64-decode caveat id")
	}
	data = data[:n]
	var tpid caveatId
	if err := json.Unmarshal(data, &tpid); err != nil {
		return nil, errgo.Notef(err, "cannot unmarshal caveat id %q", data)
	}
	if !bytes.Equal(key.Public.Key[:], tpid.ThirdPartyPublicKey.Key[:]) {
		return nil, errgo.New("public key mismatch")
	}
	if tpid.FirstPartyPublicKey == nil {
		return nil, errgo.New("target service public key not specified")
	}
	// The encrypted string is base64 encoded in the JSON representation.
	secret, err := base64.StdEncoding.DecodeString(tpid.Id)
	if err != nil {
		return nil, errgo.Notef(err, "cannot base64-decode encrypted data")
	}
	var nonce [NonceLen]byte
	if copy(nonce[:], tpid.Nonce) < NonceLen {
		return nil, errgo.Newf("nonce too short %x", tpid.Nonce)
	}
	cid, ok := box.Open(nil, secret, &nonce, tpid.FirstPartyPublicKey.boxKey(), key.Private.boxKey())
	if !ok {
		return nil, errgo.Newf("cannot decrypt caveat id %#v", tpid)
	}
	var record caveatIdRecord
	if err := json.Unmarshal(cid, &record); err != nil {
		return nil, errgo.Notef(err, "cannot decode third party caveat record")
	}
	return &ThirdPartyCaveatInfo{
		Condition:           record.Condition,
		FirstPartyPublicKey: *tpid.FirstPartyPublicKey,
		ThirdPartyKeyPair:   *key,
		RootKey:             record.RootKey,
		CaveatId:            id,
		MacaroonId:          id,
		Version:             Version1,
	}, nil
}

// decodeCaveatIdV2 decodes a version 0 caveat id.
func decodeCaveatIdV2(key *KeyPair, id []byte) (*ThirdPartyCaveatInfo, error) {
	origId := id
	if len(id) < 1+publicKeyPrefixLen+KeyLen+NonceLen+box.Overhead {
		return nil, errgo.New("caveat id too short")
	}
	id = id[1:] // skip version (already checked)

	publicKeyPrefix, id := id[:publicKeyPrefixLen], id[publicKeyPrefixLen:]
	if !bytes.Equal(key.Public.Key[:publicKeyPrefixLen], publicKeyPrefix) {
		return nil, errgo.New("public key mismatch")
	}

	var firstPartyPub PublicKey
	copy(firstPartyPub.Key[:], id[:KeyLen])
	id = id[KeyLen:]

	var nonce [NonceLen]byte
	copy(nonce[:], id[:NonceLen])
	id = id[NonceLen:]

	data, ok := box.Open(nil, id, &nonce, firstPartyPub.boxKey(), key.Private.boxKey())
	if !ok {
		return nil, errgo.Newf("cannot decrypt caveat id")
	}
	rootKey, condition, err := decodeSecretPartV2(data)
	if err != nil {
		return nil, errgo.Notef(err, "invalid secret part")
	}
	return &ThirdPartyCaveatInfo{
		Condition:           condition,
		FirstPartyPublicKey: firstPartyPub,
		ThirdPartyKeyPair:   *key,
		RootKey:             rootKey,
		CaveatId:            origId,
		MacaroonId:          origId,
		Version:             Version2,
	}, nil
}

func decodeSecretPartV2(data []byte) (rootKey []byte, condition string, err error) {
	if len(data) < 1 {
		return nil, "", errgo.New("secret part too short")
	}

	version, data := data[0], data[1:]
	if version != byte(Version2) {
		return nil, "", errgo.Newf("unsupported secret part version %d", version)
	}

	l, n := binary.Uvarint(data)
	if n <= 0 || uint64(n)+l > uint64(len(data)) {
		return nil, "", errgo.Newf("invalid root key length")
	}
	data = data[n:]

	rootKey, condition = data[:l], string(data[l:])
	return rootKey, condition, nil
}
