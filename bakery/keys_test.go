package bakery_test

import (
	"encoding/base64"
	"encoding/json"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"

	"gopkg.in/macaroon-bakery.v1/bakery"
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

func newTestKey(n byte) bakery.Key {
	var k bakery.Key
	for i := range k {
		k[i] = n + byte(i)
	}
	return k
}

type addPublicKeyArgs struct {
	loc    string
	prefix bool
	key    bakery.Key
}

var publicKeyRingTests = []struct {
	about          string
	add            []addPublicKeyArgs
	loc            string
	expectKey      bakery.Key
	expectNotFound bool
}{{
	about:          "empty keyring",
	add:            []addPublicKeyArgs{},
	loc:            "something",
	expectNotFound: true,
}, {
	about: "single non-prefix key",
	add: []addPublicKeyArgs{{
		loc: "http://foo.com/x",
		key: testKey,
	}},
	loc:       "http://foo.com/x",
	expectKey: testKey,
}, {
	about: "single prefix key",
	add: []addPublicKeyArgs{{
		loc:    "http://foo.com/x",
		key:    testKey,
		prefix: true,
	}},
	loc:       "http://foo.com/x",
	expectKey: testKey,
}, {
	about: "pattern longer than url",
	add: []addPublicKeyArgs{{
		loc:    "http://foo.com/x",
		key:    testKey,
		prefix: true,
	}},
	loc:            "http://foo.com/",
	expectNotFound: true,
}, {
	about: "pattern not ending in /",
	add: []addPublicKeyArgs{{
		loc:    "http://foo.com/x",
		key:    testKey,
		prefix: true,
	}},
	loc:            "http://foo.com/x/y",
	expectNotFound: true,
}, {
	about: "mismatched host",
	add: []addPublicKeyArgs{{
		loc:    "http://foo.com/x",
		key:    testKey,
		prefix: true,
	}},
	loc:            "http://bar.com/x/y",
	expectNotFound: true,
}, {
	about: "http vs https",
	add: []addPublicKeyArgs{{
		loc: "http://foo.com/x",
		key: testKey,
	}},
	loc:       "https://foo.com/x",
	expectKey: testKey,
}, {
	about: "naked pattern url with prefix",
	add: []addPublicKeyArgs{{
		loc:    "http://foo.com",
		key:    testKey,
		prefix: true,
	}},
	loc:       "http://foo.com/arble",
	expectKey: testKey,
}, {
	about: "naked pattern url with prefix with naked match url",
	add: []addPublicKeyArgs{{
		loc:    "http://foo.com",
		key:    testKey,
		prefix: true,
	}},
	loc:       "http://foo.com",
	expectKey: testKey,
}, {
	about: "naked pattern url, no prefix",
	add: []addPublicKeyArgs{{
		loc: "http://foo.com",
		key: testKey,
	}},
	loc:       "http://foo.com",
	expectKey: testKey,
}, {
	about: "naked pattern url, no prefix, match with no slash",
	add: []addPublicKeyArgs{{
		loc: "http://foo.com",
		key: testKey,
	}},
	loc:       "http://foo.com/",
	expectKey: testKey,
}, {
	about: "port mismatch",
	add: []addPublicKeyArgs{{
		loc: "http://foo.com:8080/x",
		key: testKey,
	}},
	loc:            "https://foo.com/x",
	expectNotFound: true,
}, {
	about: "url longer than pattern",
	add: []addPublicKeyArgs{{
		loc:    "http://foo.com/x/",
		key:    testKey,
		prefix: true,
	}},
	loc:       "https://foo.com/x/y/z",
	expectKey: testKey,
}, {
	about: "longer match preferred",
	add: []addPublicKeyArgs{{
		loc:    "http://foo.com/x/",
		key:    newTestKey(0),
		prefix: true,
	}, {
		loc:    "http://foo.com/x/y/",
		key:    newTestKey(1),
		prefix: true,
	}},
	loc:       "https://foo.com/x/y/z",
	expectKey: newTestKey(1),
}, {
	about: "longer match preferred, with other matches",
	add: []addPublicKeyArgs{{
		loc:    "http://foo.com/foo/arble",
		key:    newTestKey(0),
		prefix: true,
	}, {
		loc:    "http://foo.com/foo/arble/blah/",
		key:    newTestKey(1),
		prefix: true,
	}, {
		loc:    "http://foo.com/foo/",
		key:    newTestKey(2),
		prefix: true,
	}, {
		loc:    "http://foo.com/foobieblahbletcharbl",
		key:    newTestKey(3),
		prefix: true,
	}},
	loc:       "https://foo.com/foo/arble/blah/x",
	expectKey: newTestKey(1),
}}

func (*KeysSuite) TestPublicKeyRing(c *gc.C) {
	for i, test := range publicKeyRingTests {
		c.Logf("test %d: %s", i, test.about)
		kr := bakery.NewPublicKeyRing()
		for _, add := range test.add {
			err := kr.AddPublicKeyForLocation(add.loc, add.prefix, &bakery.PublicKey{add.key})
			c.Assert(err, gc.IsNil)
		}
		key, err := kr.PublicKeyForLocation(test.loc)
		if test.expectNotFound {
			c.Assert(err, gc.Equals, bakery.ErrNotFound)
			c.Assert(key, gc.IsNil)
			continue
		}
		c.Assert(err, gc.IsNil)
		c.Assert(*key, gc.Equals, bakery.PublicKey{test.expectKey})
	}
}
