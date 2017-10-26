package bakery

import (
	"bytes"

	jc "github.com/juju/testing/checkers"
	"golang.org/x/crypto/nacl/box"
	gc "gopkg.in/check.v1"

	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
)

type codecSuite struct {
	firstPartyKey *KeyPair
	thirdPartyKey *KeyPair
}

var _ = gc.Suite(&codecSuite{})

func (s *codecSuite) SetUpTest(c *gc.C) {
	var err error
	s.firstPartyKey, err = GenerateKey()
	c.Assert(err, gc.IsNil)
	s.thirdPartyKey, err = GenerateKey()
	c.Assert(err, gc.IsNil)
}

func (s *codecSuite) TestV1RoundTrip(c *gc.C) {
	cid, err := encodeCaveatV1(
		"is-authenticated-user", []byte("a random string"), &s.thirdPartyKey.Public, s.firstPartyKey)

	c.Assert(err, gc.IsNil)

	res, err := decodeCaveat(s.thirdPartyKey, cid)
	c.Assert(err, gc.IsNil)
	c.Assert(res, jc.DeepEquals, &ThirdPartyCaveatInfo{
		FirstPartyPublicKey: s.firstPartyKey.Public,
		RootKey:             []byte("a random string"),
		Condition:           []byte("is-authenticated-user"),
		Caveat:              cid,
		ThirdPartyKeyPair:   *s.thirdPartyKey,
		Version:             Version1,
		Namespace:           legacyNamespace(),
	})
}

func (s *codecSuite) TestV2RoundTrip(c *gc.C) {
	cid, err := encodeCaveatV2("is-authenticated-user", []byte("a random string"), &s.thirdPartyKey.Public, s.firstPartyKey)

	c.Assert(err, gc.IsNil)

	res, err := decodeCaveat(s.thirdPartyKey, cid)
	c.Assert(err, gc.IsNil)
	c.Assert(res, jc.DeepEquals, &ThirdPartyCaveatInfo{
		FirstPartyPublicKey: s.firstPartyKey.Public,
		RootKey:             []byte("a random string"),
		Condition:           []byte("is-authenticated-user"),
		Caveat:              cid,
		ThirdPartyKeyPair:   *s.thirdPartyKey,
		Version:             Version2,
		Namespace:           legacyNamespace(),
	})
}

func (s *codecSuite) TestV3RoundTrip(c *gc.C) {
	ns := checkers.NewNamespace(nil)
	ns.Register("testns", "x")
	cid, err := encodeCaveatV3("is-authenticated-user", []byte("a random string"), &s.thirdPartyKey.Public, s.firstPartyKey, ns)

	c.Assert(err, gc.IsNil)
	c.Logf("cid %x", cid)

	res, err := decodeCaveat(s.thirdPartyKey, cid)
	c.Assert(err, gc.IsNil)
	c.Assert(res, jc.DeepEquals, &ThirdPartyCaveatInfo{
		FirstPartyPublicKey: s.firstPartyKey.Public,
		RootKey:             []byte("a random string"),
		Condition:           []byte("is-authenticated-user"),
		Caveat:              cid,
		ThirdPartyKeyPair:   *s.thirdPartyKey,
		Version:             Version3,
		Namespace:           ns,
	})
}

func (s *codecSuite) TestEmptyCaveatId(c *gc.C) {
	_, err := decodeCaveat(s.thirdPartyKey, []byte{})
	c.Assert(err, gc.ErrorMatches, "empty third party caveat")
}

func (s *codecSuite) TestCaveatIdBadVersion(c *gc.C) {
	_, err := decodeCaveat(s.thirdPartyKey, []byte{1})
	c.Assert(err, gc.ErrorMatches, "caveat has unsupported version 1")
}

func (s *codecSuite) TestV2TooShort(c *gc.C) {
	_, err := decodeCaveat(s.thirdPartyKey, []byte{2})
	c.Assert(err, gc.ErrorMatches, "caveat id too short")
}

func (s *codecSuite) TestV2BadKey(c *gc.C) {
	cid, err := encodeCaveatV2("is-authenticated-user", []byte("a random string"), &s.thirdPartyKey.Public, s.firstPartyKey)

	c.Assert(err, gc.IsNil)
	cid[1] ^= 1

	_, err = decodeCaveat(s.thirdPartyKey, cid)
	c.Assert(err, gc.ErrorMatches, "public key mismatch")
}

func (s *codecSuite) TestV2DecryptionError(c *gc.C) {
	cid, err := encodeCaveatV2("is-authenticated-user", []byte("a random string"), &s.thirdPartyKey.Public, s.firstPartyKey)

	c.Assert(err, gc.IsNil)
	cid[5] ^= 1

	_, err = decodeCaveat(s.thirdPartyKey, cid)
	c.Assert(err, gc.ErrorMatches, "cannot decrypt caveat id")
}

func (s *codecSuite) TestV2EmptySecretPart(c *gc.C) {
	cid, err := encodeCaveatV2("is-authenticated-user", []byte("a random string"), &s.thirdPartyKey.Public, s.firstPartyKey)

	c.Assert(err, gc.IsNil)
	cid = s.replaceV2SecretPart(cid, []byte{})

	_, err = decodeCaveat(s.thirdPartyKey, cid)
	c.Assert(err, gc.ErrorMatches, "invalid secret part: secret part too short")
}

func (s *codecSuite) TestV2BadSecretPartVersion(c *gc.C) {
	cid, err := encodeCaveatV2("is-authenticated-user", []byte("a random string"), &s.thirdPartyKey.Public, s.firstPartyKey)
	c.Assert(err, gc.IsNil)
	cid = s.replaceV2SecretPart(cid, []byte{1})

	_, err = decodeCaveat(s.thirdPartyKey, cid)
	c.Assert(err, gc.ErrorMatches, "invalid secret part: unexpected secret part version, got 1 want 2")
}

func (s *codecSuite) TestV2EmptyRootKey(c *gc.C) {
	cid, err := encodeCaveatV2("is-authenticated-user", []byte{}, &s.thirdPartyKey.Public, s.firstPartyKey)
	c.Assert(err, gc.IsNil)

	res, err := decodeCaveat(s.thirdPartyKey, cid)
	c.Assert(err, gc.IsNil)
	c.Assert(res, jc.DeepEquals, &ThirdPartyCaveatInfo{
		FirstPartyPublicKey: s.firstPartyKey.Public,
		RootKey:             []byte{},
		Condition:           []byte("is-authenticated-user"),
		Caveat:              cid,
		ThirdPartyKeyPair:   *s.thirdPartyKey,
		Version:             Version2,
		Namespace:           legacyNamespace(),
	})
}

func (s *codecSuite) TestV2LongRootKey(c *gc.C) {
	cid, err := encodeCaveatV2("is-authenticated-user", bytes.Repeat([]byte{0}, 65536), &s.thirdPartyKey.Public, s.firstPartyKey)
	c.Assert(err, gc.IsNil)

	res, err := decodeCaveat(s.thirdPartyKey, cid)
	c.Assert(err, gc.IsNil)
	c.Assert(res, jc.DeepEquals, &ThirdPartyCaveatInfo{
		FirstPartyPublicKey: s.firstPartyKey.Public,
		RootKey:             bytes.Repeat([]byte{0}, 65536),
		Condition:           []byte("is-authenticated-user"),
		Caveat:              cid,
		ThirdPartyKeyPair:   *s.thirdPartyKey,
		Version:             Version2,
		Namespace:           legacyNamespace(),
	})
}

func (s *codecSuite) replaceV2SecretPart(cid, replacement []byte) []byte {
	cid = cid[:1+publicKeyPrefixLen+KeyLen+NonceLen]
	var nonce [NonceLen]byte
	copy(nonce[:], cid[1+publicKeyPrefixLen+KeyLen:])
	return box.Seal(cid, replacement, &nonce, s.firstPartyKey.Public.boxKey(), s.thirdPartyKey.Private.boxKey())
}
