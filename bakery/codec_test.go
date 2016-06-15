package bakery

import (
	"bytes"

	jc "github.com/juju/testing/checkers"
	"golang.org/x/crypto/nacl/box"

	gc "gopkg.in/check.v1"
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

func (s *codecSuite) TestJSONRoundTrip(c *gc.C) {
	cid, err := encodeJSONCaveatId(
		"is-authenticated-user", []byte("a random string"), &s.thirdPartyKey.Public, s.firstPartyKey)

	c.Assert(err, gc.IsNil)

	res, err := decodeCaveatId(s.thirdPartyKey, cid)
	c.Assert(err, gc.IsNil)
	c.Assert(res, jc.DeepEquals, &ThirdPartyCaveatInfo{
		FirstPartyPublicKey: s.firstPartyKey.Public,
		RootKey:             []byte("a random string"),
		Condition:           "is-authenticated-user",
		CaveatId:            cid,
		MacaroonId:          cid,
		ThirdPartyKeyPair:   *s.thirdPartyKey,
	})
}

func (s *codecSuite) TestV0RoundTrip(c *gc.C) {
	cid, err := encodeCaveatIdV0("is-authenticated-user", []byte("a random string"), &s.thirdPartyKey.Public, s.firstPartyKey)

	c.Assert(err, gc.IsNil)

	res, err := decodeCaveatId(s.thirdPartyKey, cid)
	c.Assert(err, gc.IsNil)
	c.Assert(res, jc.DeepEquals, &ThirdPartyCaveatInfo{
		FirstPartyPublicKey: s.firstPartyKey.Public,
		RootKey:             []byte("a random string"),
		Condition:           "is-authenticated-user",
		CaveatId:            cid,
		MacaroonId:          cid,
		ThirdPartyKeyPair:   *s.thirdPartyKey,
	})
}

func (s *codecSuite) TestEmptyCaveatId(c *gc.C) {
	_, err := decodeCaveatId(s.thirdPartyKey, []byte{})
	c.Assert(err, gc.ErrorMatches, "caveat id empty")
}

func (s *codecSuite) TestCaveatIdBadVersion(c *gc.C) {
	_, err := decodeCaveatId(s.thirdPartyKey, []byte{1})
	c.Assert(err, gc.ErrorMatches, "caveat id has unsupported version 1")
}

func (s *codecSuite) TestV0TooShort(c *gc.C) {
	_, err := decodeCaveatId(s.thirdPartyKey, []byte{0})
	c.Assert(err, gc.ErrorMatches, "caveat id too short")
}

func (s *codecSuite) TestV0BadKey(c *gc.C) {
	cid, err := encodeCaveatIdV0("is-authenticated-user", []byte("a random string"), &s.thirdPartyKey.Public, s.firstPartyKey)

	c.Assert(err, gc.IsNil)
	cid[1] ^= 1

	_, err = decodeCaveatId(s.thirdPartyKey, cid)
	c.Assert(err, gc.ErrorMatches, "public key mismatch")
}

func (s *codecSuite) TestV0DecryptionError(c *gc.C) {
	cid, err := encodeCaveatIdV0("is-authenticated-user", []byte("a random string"), &s.thirdPartyKey.Public, s.firstPartyKey)

	c.Assert(err, gc.IsNil)
	cid[5] ^= 1

	_, err = decodeCaveatId(s.thirdPartyKey, cid)
	c.Assert(err, gc.ErrorMatches, "cannot decrypt caveat id")
}

func (s *codecSuite) TestV0EmptySecretPart(c *gc.C) {
	cid, err := encodeCaveatIdV0("is-authenticated-user", []byte("a random string"), &s.thirdPartyKey.Public, s.firstPartyKey)

	c.Assert(err, gc.IsNil)
	cid = s.replaceV0SecretPart(cid, []byte{})

	_, err = decodeCaveatId(s.thirdPartyKey, cid)
	c.Assert(err, gc.ErrorMatches, "invalid secret part: secret part too short")
}

func (s *codecSuite) TestV0BadSecretPartVersion(c *gc.C) {
	cid, err := encodeCaveatIdV0("is-authenticated-user", []byte("a random string"), &s.thirdPartyKey.Public, s.firstPartyKey)
	c.Assert(err, gc.IsNil)
	cid = s.replaceV0SecretPart(cid, []byte{1})

	_, err = decodeCaveatId(s.thirdPartyKey, cid)
	c.Assert(err, gc.ErrorMatches, "invalid secret part: unsupported secret part version 1")
}

func (s *codecSuite) TestV0EmptyRootKey(c *gc.C) {
	cid, err := encodeCaveatIdV0("is-authenticated-user", []byte{}, &s.thirdPartyKey.Public, s.firstPartyKey)
	c.Assert(err, gc.IsNil)

	res, err := decodeCaveatId(s.thirdPartyKey, cid)
	c.Assert(err, gc.IsNil)
	c.Assert(res, jc.DeepEquals, &ThirdPartyCaveatInfo{
		FirstPartyPublicKey: s.firstPartyKey.Public,
		RootKey:             []byte{},
		Condition:           "is-authenticated-user",
		CaveatId:            cid,
		MacaroonId:          cid,
		ThirdPartyKeyPair:   *s.thirdPartyKey,
	})
}

func (s *codecSuite) TestV0LongRootKey(c *gc.C) {
	cid, err := encodeCaveatIdV0("is-authenticated-user", bytes.Repeat([]byte{0}, 65536), &s.thirdPartyKey.Public, s.firstPartyKey)
	c.Assert(err, gc.IsNil)

	res, err := decodeCaveatId(s.thirdPartyKey, cid)
	c.Assert(err, gc.IsNil)
	c.Assert(res, jc.DeepEquals, &ThirdPartyCaveatInfo{
		FirstPartyPublicKey: s.firstPartyKey.Public,
		RootKey:             bytes.Repeat([]byte{0}, 65536),
		Condition:           "is-authenticated-user",
		CaveatId:            cid,
		MacaroonId:          cid,
		ThirdPartyKeyPair:   *s.thirdPartyKey,
	})
}

func (s *codecSuite) replaceV0SecretPart(cid, replacement []byte) []byte {
	cid = cid[:1+publicKeyPrefixLen+KeyLen+NonceLen]
	var nonce [NonceLen]byte
	copy(nonce[:], cid[1+publicKeyPrefixLen+KeyLen:])
	return box.Seal(cid, replacement, &nonce, s.firstPartyKey.Public.boxKey(), s.thirdPartyKey.Private.boxKey())
}
