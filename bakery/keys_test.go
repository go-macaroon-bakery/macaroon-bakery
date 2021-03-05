package bakery_test

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	qt "github.com/frankban/quicktest"
	"gopkg.in/yaml.v2"

	"gopkg.in/macaroon-bakery.v3/bakery"
)

var testKey = newTestKey(0)

func TestMarshalBinary(t *testing.T) {
	c := qt.New(t)
	data, err := testKey.MarshalBinary()
	c.Assert(err, qt.IsNil)
	c.Assert(data, qt.DeepEquals, []byte(testKey[:]))

	var key1 bakery.Key
	err = key1.UnmarshalBinary(data)
	c.Assert(err, qt.IsNil)
	c.Assert(key1, qt.DeepEquals, testKey)
}

func TestMarshalText(t *testing.T) {
	c := qt.New(t)
	data, err := testKey.MarshalText()
	c.Assert(err, qt.IsNil)
	c.Assert(string(data), qt.Equals, base64.StdEncoding.EncodeToString([]byte(testKey[:])))

	var key1 bakery.Key
	err = key1.UnmarshalText(data)
	c.Assert(err, qt.IsNil)
	c.Assert(key1, qt.Equals, testKey)
}

func TestUnmarshalTextWrongKeyLength(t *testing.T) {
	c := qt.New(t)
	var key bakery.Key
	err := key.UnmarshalText([]byte("aGVsbG8K"))
	c.Assert(err, qt.ErrorMatches, `wrong length for key, got 6 want 32`)
}

func TestKeyPairMarshalJSON(t *testing.T) {
	c := qt.New(t)
	kp := bakery.KeyPair{
		Public:  bakery.PublicKey{testKey},
		Private: bakery.PrivateKey{testKey},
	}
	kp.Private.Key[0] = 99
	data, err := json.Marshal(kp)
	c.Assert(err, qt.IsNil)
	var x map[string]interface{}
	err = json.Unmarshal(data, &x)
	c.Assert(err, qt.IsNil)

	// Check that the fields have marshaled as strings.
	_, ok := x["private"].(string)
	c.Assert(ok, qt.Equals, true)
	_, ok = x["public"].(string)
	c.Assert(ok, qt.Equals, true)

	var kp1 bakery.KeyPair
	err = json.Unmarshal(data, &kp1)
	c.Assert(err, qt.IsNil)
	c.Assert(kp1, qt.DeepEquals, kp)
}

func TestKeyPairMarshalYAML(t *testing.T) {
	c := qt.New(t)
	kp := bakery.KeyPair{
		Public:  bakery.PublicKey{testKey},
		Private: bakery.PrivateKey{testKey},
	}
	kp.Private.Key[0] = 99
	data, err := yaml.Marshal(kp)
	c.Assert(err, qt.IsNil)
	var x map[string]interface{}
	err = yaml.Unmarshal(data, &x)
	c.Assert(err, qt.IsNil)

	// Check that the fields have marshaled as strings.
	_, ok := x["private"].(string)
	c.Assert(ok, qt.Equals, true)
	_, ok = x["public"].(string)
	c.Assert(ok, qt.Equals, true)

	var kp1 bakery.KeyPair
	err = yaml.Unmarshal(data, &kp1)
	c.Assert(err, qt.IsNil)
	c.Assert(kp1, qt.DeepEquals, kp)
}

func TestKeyPairUnmarshalJSONMissingPublicKey(t *testing.T) {
	c := qt.New(t)
	data := `{"private": "7ZcOvDAW9opAIPzJ7FdSbz2i2qL8bFZapDlmNLpMzpU="}`
	var k bakery.KeyPair
	err := json.Unmarshal([]byte(data), &k)
	c.Assert(err, qt.ErrorMatches, `missing public key`)
}

func TestKeyPairUnmarshalJSONMissingPrivateKey(t *testing.T) {
	c := qt.New(t)
	data := `{"public": "7ZcOvDAW9opAIPzJ7FdSbz2i2qL8bFZapDlmNLpMzpU="}`
	var k bakery.KeyPair
	err := json.Unmarshal([]byte(data), &k)
	c.Assert(err, qt.ErrorMatches, `missing private key`)
}

func TestKeyPairUnmarshalJSONEmptyKeys(t *testing.T) {
	c := qt.New(t)
	data := `{"private": "", "public": ""}`
	var k bakery.KeyPair
	err := json.Unmarshal([]byte(data), &k)
	c.Assert(err, qt.ErrorMatches, `wrong length for key, got 0 want 32`)
}

func TestKeyPairUnmarshalJSONNoKeys(t *testing.T) {
	c := qt.New(t)
	data := `{}`
	var k bakery.KeyPair
	err := json.Unmarshal([]byte(data), &k)
	c.Assert(err, qt.ErrorMatches, `missing public key`)
}

func TestKeyPairUnmarshalYAMLMissingPublicKey(t *testing.T) {
	c := qt.New(t)
	data := `
private: 7ZcOvDAW9opAIPzJ7FdSbz2i2qL8bFZapDlmNLpMzpU=
`
	var k bakery.KeyPair
	err := yaml.Unmarshal([]byte(data), &k)
	c.Assert(err, qt.ErrorMatches, `missing public key`)
}

func TestKeyPairUnmarshalYAMLMissingPrivateKey(t *testing.T) {
	c := qt.New(t)
	data := `
public: 7ZcOvDAW9opAIPzJ7FdSbz2i2qL8bFZapDlmNLpMzpU=
`
	var k bakery.KeyPair
	err := yaml.Unmarshal([]byte(data), &k)
	c.Assert(err, qt.ErrorMatches, `missing private key`)
}

func TestDerivePublicFromPrivate(t *testing.T) {
	c := qt.New(t)
	k := mustGenerateKey()
	c.Assert(k.Private.Public(), qt.Equals, k.Public)
}

func newTestKey(n byte) bakery.Key {
	var k bakery.Key
	for i := range k {
		k[i] = n + byte(i)
	}
	return k
}
