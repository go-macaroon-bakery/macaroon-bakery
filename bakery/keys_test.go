package bakery_test

import (
	"encoding/base64"
	"encoding/json"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/yaml.v2"

	"gopkg.in/macaroon-bakery.v2/bakery"
)

type KeysSuite struct{}

var _ = gc.Suite(&KeysSuite{})

var testKey = newTestKey(0)

func (*KeysSuite) TestMarshalBinary(c *gc.C) {
	data, err := testKey.MarshalBinary()
	c.Assert(err, gc.IsNil)
	c.Assert(data, jc.DeepEquals, []byte(testKey[:]))

	var key1 bakery.Key
	err = key1.UnmarshalBinary(data)
	c.Assert(err, gc.IsNil)
	c.Assert(key1, gc.DeepEquals, testKey)
}

func (*KeysSuite) TestMarshalText(c *gc.C) {
	data, err := testKey.MarshalText()
	c.Assert(err, gc.IsNil)
	c.Assert(string(data), gc.Equals, base64.StdEncoding.EncodeToString([]byte(testKey[:])))

	var key1 bakery.Key
	err = key1.UnmarshalText(data)
	c.Assert(err, gc.IsNil)
	c.Assert(key1, gc.Equals, testKey)
}

func (*KeysSuite) TestUnmarshalTextWrongKeyLength(c *gc.C) {
	var key bakery.Key
	err := key.UnmarshalText([]byte("aGVsbG8K"))
	c.Assert(err, gc.ErrorMatches, `wrong length for key, got 6 want 32`)
}

func (*KeysSuite) TestKeyPairMarshalJSON(c *gc.C) {
	kp := bakery.KeyPair{
		Public:  bakery.PublicKey{testKey},
		Private: bakery.PrivateKey{testKey},
	}
	kp.Private.Key[0] = 99
	data, err := json.Marshal(kp)
	c.Assert(err, gc.IsNil)
	var x map[string]interface{}
	err = json.Unmarshal(data, &x)
	c.Assert(err, gc.IsNil)

	// Check that the fields have marshaled as strings.
	c.Assert(x["private"], gc.FitsTypeOf, "")
	c.Assert(x["public"], gc.FitsTypeOf, "")

	var kp1 bakery.KeyPair
	err = json.Unmarshal(data, &kp1)
	c.Assert(err, gc.IsNil)
	c.Assert(kp1, jc.DeepEquals, kp)
}

func (*KeysSuite) TestKeyPairMarshalYAML(c *gc.C) {
	kp := bakery.KeyPair{
		Public:  bakery.PublicKey{testKey},
		Private: bakery.PrivateKey{testKey},
	}
	kp.Private.Key[0] = 99
	data, err := yaml.Marshal(kp)
	c.Assert(err, gc.IsNil)
	var x map[string]interface{}
	err = yaml.Unmarshal(data, &x)
	c.Assert(err, gc.IsNil)

	// Check that the fields have marshaled as strings.
	c.Assert(x["private"], gc.FitsTypeOf, "")
	c.Assert(x["public"], gc.FitsTypeOf, "")

	var kp1 bakery.KeyPair
	err = yaml.Unmarshal(data, &kp1)
	c.Assert(err, gc.IsNil)
	c.Assert(kp1, jc.DeepEquals, kp)
}

func (*KeysSuite) TestKeyPairUnmarshalJSONMissingPublicKey(c *gc.C) {
	data := `{"private": "7ZcOvDAW9opAIPzJ7FdSbz2i2qL8bFZapDlmNLpMzpU="}`
	var k bakery.KeyPair
	err := json.Unmarshal([]byte(data), &k)
	c.Assert(err, gc.ErrorMatches, `missing public key`)
}

func (*KeysSuite) TestKeyPairUnmarshalJSONMissingPrivateKey(c *gc.C) {
	data := `{"public": "7ZcOvDAW9opAIPzJ7FdSbz2i2qL8bFZapDlmNLpMzpU="}`
	var k bakery.KeyPair
	err := json.Unmarshal([]byte(data), &k)
	c.Assert(err, gc.ErrorMatches, `missing private key`)
}

func (*KeysSuite) TestKeyPairUnmarshalJSONEmptyKeys(c *gc.C) {
	data := `{"private": "", "public": ""}`
	var k bakery.KeyPair
	err := json.Unmarshal([]byte(data), &k)
	c.Assert(err, gc.ErrorMatches, `wrong length for key, got 0 want 32`)
}

func (*KeysSuite) TestKeyPairUnmarshalJSONNoKeys(c *gc.C) {
	data := `{}`
	var k bakery.KeyPair
	err := json.Unmarshal([]byte(data), &k)
	c.Assert(err, gc.ErrorMatches, `missing public key`)
}

func (*KeysSuite) TestKeyPairUnmarshalYAMLMissingPublicKey(c *gc.C) {
	data := `
private: 7ZcOvDAW9opAIPzJ7FdSbz2i2qL8bFZapDlmNLpMzpU=
`
	var k bakery.KeyPair
	err := yaml.Unmarshal([]byte(data), &k)
	c.Assert(err, gc.ErrorMatches, `missing public key`)
}

func (*KeysSuite) TestKeyPairUnmarshalYAMLMissingPrivateKey(c *gc.C) {
	data := `
public: 7ZcOvDAW9opAIPzJ7FdSbz2i2qL8bFZapDlmNLpMzpU=
`
	var k bakery.KeyPair
	err := yaml.Unmarshal([]byte(data), &k)
	c.Assert(err, gc.ErrorMatches, `missing private key`)
}

func (*KeysSuite) TestDerivePublicFromPrivate(c *gc.C) {
	k := mustGenerateKey()
	c.Assert(k.Private.Public(), gc.Equals, k.Public)
}

func newTestKey(n byte) bakery.Key {
	var k bakery.Key
	for i := range k {
		k[i] = n + byte(i)
	}
	return k
}
