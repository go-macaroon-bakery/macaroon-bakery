package bakery

import (
	"bytes"
	"testing"

	qt "github.com/frankban/quicktest"
	"golang.org/x/crypto/nacl/box"

	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
)

var (
	testFirstPartyKey = MustGenerateKey()
	testThirdPartyKey = MustGenerateKey()
)

func TestV1RoundTrip(t *testing.T) {
	c := qt.New(t)
	cid, err := encodeCaveatV1(
		"is-authenticated-user", []byte("a random string"), &testThirdPartyKey.Public, testFirstPartyKey)

	c.Assert(err, qt.IsNil)

	res, err := decodeCaveat(testThirdPartyKey, cid)
	c.Assert(err, qt.IsNil)
	c.Assert(res, qt.DeepEquals, &ThirdPartyCaveatInfo{
		FirstPartyPublicKey: testFirstPartyKey.Public,
		RootKey:             []byte("a random string"),
		Condition:           []byte("is-authenticated-user"),
		Caveat:              cid,
		ThirdPartyKeyPair:   *testThirdPartyKey,
		Version:             Version1,
		Namespace:           legacyNamespace(),
	})
}

func TestV2RoundTrip(t *testing.T) {
	c := qt.New(t)
	cid, err := encodeCaveatV2("is-authenticated-user", []byte("a random string"), &testThirdPartyKey.Public, testFirstPartyKey)

	c.Assert(err, qt.IsNil)

	res, err := decodeCaveat(testThirdPartyKey, cid)
	c.Assert(err, qt.IsNil)
	c.Assert(res, qt.DeepEquals, &ThirdPartyCaveatInfo{
		FirstPartyPublicKey: testFirstPartyKey.Public,
		RootKey:             []byte("a random string"),
		Condition:           []byte("is-authenticated-user"),
		Caveat:              cid,
		ThirdPartyKeyPair:   *testThirdPartyKey,
		Version:             Version2,
		Namespace:           legacyNamespace(),
	})
}

func TestV3RoundTrip(t *testing.T) {
	c := qt.New(t)
	ns := checkers.NewNamespace(nil)
	ns.Register("testns", "x")
	cid, err := encodeCaveatV3("is-authenticated-user", []byte("a random string"), &testThirdPartyKey.Public, testFirstPartyKey, ns)

	c.Assert(err, qt.IsNil)
	c.Logf("cid %x", cid)

	res, err := decodeCaveat(testThirdPartyKey, cid)
	c.Assert(err, qt.IsNil)
	c.Assert(res, qt.DeepEquals, &ThirdPartyCaveatInfo{
		FirstPartyPublicKey: testFirstPartyKey.Public,
		RootKey:             []byte("a random string"),
		Condition:           []byte("is-authenticated-user"),
		Caveat:              cid,
		ThirdPartyKeyPair:   *testThirdPartyKey,
		Version:             Version3,
		Namespace:           ns,
	})
}

func TestEmptyCaveatId(t *testing.T) {
	c := qt.New(t)
	_, err := decodeCaveat(testThirdPartyKey, []byte{})
	c.Assert(err, qt.ErrorMatches, "empty third party caveat")
}

func TestCaveatIdBadVersion(t *testing.T) {
	c := qt.New(t)
	_, err := decodeCaveat(testThirdPartyKey, []byte{1})
	c.Assert(err, qt.ErrorMatches, "caveat has unsupported version 1")
}

func TestV2TooShort(t *testing.T) {
	c := qt.New(t)
	_, err := decodeCaveat(testThirdPartyKey, []byte{2})
	c.Assert(err, qt.ErrorMatches, "caveat id too short")
}

func TestV2BadKey(t *testing.T) {
	c := qt.New(t)
	cid, err := encodeCaveatV2("is-authenticated-user", []byte("a random string"), &testThirdPartyKey.Public, testFirstPartyKey)

	c.Assert(err, qt.IsNil)
	cid[1] ^= 1

	_, err = decodeCaveat(testThirdPartyKey, cid)
	c.Assert(err, qt.ErrorMatches, "public key mismatch")
}

func TestV2DecryptionError(t *testing.T) {
	c := qt.New(t)
	cid, err := encodeCaveatV2("is-authenticated-user", []byte("a random string"), &testThirdPartyKey.Public, testFirstPartyKey)

	c.Assert(err, qt.IsNil)
	cid[5] ^= 1

	_, err = decodeCaveat(testThirdPartyKey, cid)
	c.Assert(err, qt.ErrorMatches, "cannot decrypt caveat id")
}

func TestV2EmptySecretPart(t *testing.T) {
	c := qt.New(t)
	cid, err := encodeCaveatV2("is-authenticated-user", []byte("a random string"), &testThirdPartyKey.Public, testFirstPartyKey)

	c.Assert(err, qt.IsNil)
	cid = replaceV2SecretPart(cid, []byte{})

	_, err = decodeCaveat(testThirdPartyKey, cid)
	c.Assert(err, qt.ErrorMatches, "invalid secret part: secret part too short")
}

func TestV2BadSecretPartVersion(t *testing.T) {
	c := qt.New(t)
	cid, err := encodeCaveatV2("is-authenticated-user", []byte("a random string"), &testThirdPartyKey.Public, testFirstPartyKey)
	c.Assert(err, qt.IsNil)
	cid = replaceV2SecretPart(cid, []byte{1})

	_, err = decodeCaveat(testThirdPartyKey, cid)
	c.Assert(err, qt.ErrorMatches, "invalid secret part: unexpected secret part version, got 1 want 2")
}

func TestV2EmptyRootKey(t *testing.T) {
	c := qt.New(t)
	cid, err := encodeCaveatV2("is-authenticated-user", []byte{}, &testThirdPartyKey.Public, testFirstPartyKey)
	c.Assert(err, qt.IsNil)

	res, err := decodeCaveat(testThirdPartyKey, cid)
	c.Assert(err, qt.IsNil)
	c.Assert(res, qt.DeepEquals, &ThirdPartyCaveatInfo{
		FirstPartyPublicKey: testFirstPartyKey.Public,
		RootKey:             []byte{},
		Condition:           []byte("is-authenticated-user"),
		Caveat:              cid,
		ThirdPartyKeyPair:   *testThirdPartyKey,
		Version:             Version2,
		Namespace:           legacyNamespace(),
	})
}

func TestV2LongRootKey(t *testing.T) {
	c := qt.New(t)
	cid, err := encodeCaveatV2("is-authenticated-user", bytes.Repeat([]byte{0}, 65536), &testThirdPartyKey.Public, testFirstPartyKey)
	c.Assert(err, qt.IsNil)

	res, err := decodeCaveat(testThirdPartyKey, cid)
	c.Assert(err, qt.IsNil)
	c.Assert(res, qt.DeepEquals, &ThirdPartyCaveatInfo{
		FirstPartyPublicKey: testFirstPartyKey.Public,
		RootKey:             bytes.Repeat([]byte{0}, 65536),
		Condition:           []byte("is-authenticated-user"),
		Caveat:              cid,
		ThirdPartyKeyPair:   *testThirdPartyKey,
		Version:             Version2,
		Namespace:           legacyNamespace(),
	})
}

func replaceV2SecretPart(cid, replacement []byte) []byte {
	cid = cid[:1+publicKeyPrefixLen+KeyLen+NonceLen]
	var nonce [NonceLen]byte
	copy(nonce[:], cid[1+publicKeyPrefixLen+KeyLen:])
	return box.Seal(cid, replacement, &nonce, testFirstPartyKey.Public.boxKey(), testThirdPartyKey.Private.boxKey())
}
