package bakery_test

import (
	"encoding/base64"
	"encoding/json"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"gopkg.in/macaroon-bakery.v0/bakery"
)

type KeysSuite struct{}

var _ = gc.Suite(&KeysSuite{})

var testKey = bakery.Key{
	0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
	0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
}

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

func (*KeysSuite) TestKeyPairMarshalJSON(c *gc.C) {
	kp := bakery.KeyPair{
		Public:  bakery.PublicKey{testKey},
		Private: bakery.PrivateKey{testKey},
	}
	kp.Private.Key[0] = 99
	data, err := json.Marshal(kp)
	c.Assert(err, gc.IsNil)
	var x interface{}
	err = json.Unmarshal(data, &x)
	c.Assert(err, gc.IsNil)

	// Check that the fields have marshaled as strings.
	c.Assert(x.(map[string]interface{})["private"], gc.FitsTypeOf, "")
	c.Assert(x.(map[string]interface{})["public"], gc.FitsTypeOf, "")

	var kp1 bakery.KeyPair
	err = json.Unmarshal(data, &kp1)
	c.Assert(err, gc.IsNil)
	c.Assert(kp1, jc.DeepEquals, kp)
}
