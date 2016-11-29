package agent_test

import (
	"encoding/json"
	"strings"

	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/yaml.v2"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery/agent"
)

var _ httpbakery.Visitor = (*agent.Visitor)(nil)

type serializeSuite struct {
}

var _ = gc.Suite(&serializeSuite{})

var unmarshalJSONTests = []struct {
	about            string
	data             string
	expectError      string
	expectDefaultKey *bakery.KeyPair
	expectAgents     []agent.Agent
}{{
	about: "with default",
	data: `
{
	"key": {
		"public": "en/IpDvPYUOSOA71WnIVNVQ5N+1vLOVQtvgIa9UUyhQ=",
		"private": "gyeCwDRCGVpFaqJVnu2VPalW4IRJQ9hqxo0LPTYHyUU="
	},
	"agents": [{
		"url": "http://example.com/",
		"username": "bob"
	}, {
		"url": "http://other.example/foo",
		"username": "otherbob"
	}]
}`,
	expectDefaultKey: parseKeyPair("en/IpDvPYUOSOA71WnIVNVQ5N+1vLOVQtvgIa9UUyhQ= gyeCwDRCGVpFaqJVnu2VPalW4IRJQ9hqxo0LPTYHyUU="),
	expectAgents: []agent.Agent{{
		URL:      "http://example.com/",
		Username: "bob",
		Key:      parseKeyPair("en/IpDvPYUOSOA71WnIVNVQ5N+1vLOVQtvgIa9UUyhQ= gyeCwDRCGVpFaqJVnu2VPalW4IRJQ9hqxo0LPTYHyUU="),
	}, {
		URL:      "http://other.example/foo",
		Username: "otherbob",
		Key:      parseKeyPair("en/IpDvPYUOSOA71WnIVNVQ5N+1vLOVQtvgIa9UUyhQ= gyeCwDRCGVpFaqJVnu2VPalW4IRJQ9hqxo0LPTYHyUU="),
	}},
}, {
	about: "no default",
	data: `
{
	"agents": [{
		"url": "http://example.com/",
		"username": "bob",
		"key": {
			"public": "en/IpDvPYUOSOA71WnIVNVQ5N+1vLOVQtvgIa9UUyhQ=",
			"private": "gyeCwDRCGVpFaqJVnu2VPalW4IRJQ9hqxo0LPTYHyUU="
		}
	}, {
		"url": "http://other.example/foo",
		"username": "otherbob",
		"key": {
			"public": "nvNhxAaq4f8Ug9cO5hf0sRFGZbr+aLqAQsEeMgWYhTY=",
			"private": "7ZcOvDAW9opAIPzJ7FdSbz2i2qL8bFZapDlmNLpMzpU="
		}
	}]
}`,
	expectAgents: []agent.Agent{{
		URL:      "http://example.com/",
		Username: "bob",
		Key:      parseKeyPair("en/IpDvPYUOSOA71WnIVNVQ5N+1vLOVQtvgIa9UUyhQ= gyeCwDRCGVpFaqJVnu2VPalW4IRJQ9hqxo0LPTYHyUU="),
	}, {
		URL:      "http://other.example/foo",
		Username: "otherbob",
		Key:      parseKeyPair("nvNhxAaq4f8Ug9cO5hf0sRFGZbr+aLqAQsEeMgWYhTY= 7ZcOvDAW9opAIPzJ7FdSbz2i2qL8bFZapDlmNLpMzpU="),
	}},
}, {
	about: "agent with no key",
	data: `
{
	"agents": [{
		"url": "http://example.com/",
		"username": "bob"
	}]
}`,
	expectError: `cannot add agent at URL "http://example.com/": no key for agent`,
}, {
	about: "agent with missing private key",
	data: `
{
	"agents": [{
		"url": "http://example.com/",
		"username": "bob",
		"key": {
			"public": "nvNhxAaq4f8Ug9cO5hf0sRFGZbr+aLqAQsEeMgWYhTY="
		}
	}]
}`,
	// TODO it would be nice if encoding/json could provide more contextual
	// information for errors like this.
	expectError: `missing private key`,
}}

func (s *serializeSuite) TestUnmarshalJSON(c *gc.C) {
	for i, test := range unmarshalJSONTests {
		c.Logf("test %d: %v", i, test.about)
		var v agent.Visitor
		err := json.Unmarshal([]byte(test.data), &v)
		if test.expectError != "" {
			c.Assert(err, gc.ErrorMatches, test.expectError)
			continue
		}
		c.Assert(err, gc.IsNil)
		c.Assert(v.DefaultKey(), jc.DeepEquals, test.expectDefaultKey)
		c.Assert(v.Agents(), jc.DeepEquals, test.expectAgents)
	}
}

func (s *serializeSuite) TestUnmarshalYAML(c *gc.C) {
	for i, test := range unmarshalJSONTests {
		c.Logf("test %d: %v", i, test.about)
		var x interface{}
		err := json.Unmarshal([]byte(test.data), &x)
		c.Assert(err, gc.IsNil)
		ydata, err := yaml.Marshal(x)
		c.Assert(err, gc.IsNil)

		var v agent.Visitor
		err = yaml.Unmarshal(ydata, &v)
		if test.expectError != "" {
			c.Assert(err, gc.ErrorMatches, test.expectError)
			continue
		}
		c.Assert(err, gc.IsNil)
		c.Assert(v.DefaultKey(), jc.DeepEquals, test.expectDefaultKey)
		c.Assert(v.Agents(), jc.DeepEquals, test.expectAgents)
	}
}

func parseKeyPair(s string) *bakery.KeyPair {
	parts := strings.Split(s, " ")
	var k bakery.KeyPair
	err := k.Public.UnmarshalText([]byte(parts[0]))
	if err != nil {
		panic(err)
	}
	err = k.Private.UnmarshalText([]byte(parts[1]))
	if err != nil {
		panic(err)
	}
	return &k
}
