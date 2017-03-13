package agent_test

import (
	"encoding/base64"
	"net/http"

	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery/agent"
)

type cookieSuite struct{}

var _ = gc.Suite(&cookieSuite{})

var loginCookieTests = []struct {
	about       string
	addCookie   func(*http.Request, *bakery.PublicKey)
	expectUser  string
	expectError string
	expectCause error
}{{
	about: "success",
	addCookie: func(req *http.Request, key *bakery.PublicKey) {
		agent.AddCookie(req, "bob", key)
	},
	expectUser: "bob",
}, {
	about:       "no cookie",
	addCookie:   func(req *http.Request, key *bakery.PublicKey) {},
	expectError: "no agent-login cookie found",
	expectCause: agent.ErrNoAgentLoginCookie,
}, {
	about: "invalid base64 encoding",
	addCookie: func(req *http.Request, key *bakery.PublicKey) {
		req.AddCookie(&http.Cookie{
			Name:  "agent-login",
			Value: "x",
		})
	},
	expectError: "cannot decode cookie value: illegal base64 data at input byte 0",
}, {
	about: "invalid JSON",
	addCookie: func(req *http.Request, key *bakery.PublicKey) {
		req.AddCookie(&http.Cookie{
			Name:  "agent-login",
			Value: base64.StdEncoding.EncodeToString([]byte("}")),
		})
	},
	expectError: "cannot unmarshal agent login: invalid character '}' looking for beginning of value",
}, {
	about: "no username",
	addCookie: func(req *http.Request, key *bakery.PublicKey) {
		agent.AddCookie(req, "", key)
	},
	expectError: "agent login has no user name",
}, {
	about: "no public key",
	addCookie: func(req *http.Request, key *bakery.PublicKey) {
		agent.AddCookie(req, "bob", nil)
	},
	expectError: "agent login has no public key",
}}

func (s *cookieSuite) TestLoginCookie(c *gc.C) {
	key, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)

	for i, test := range loginCookieTests {
		c.Logf("test %d: %s", i, test.about)

		req, err := http.NewRequest("GET", "", nil)
		c.Assert(err, gc.IsNil)
		test.addCookie(req, &key.Public)
		gotUsername, gotKey, err := agent.LoginCookie(req)

		if test.expectError != "" {
			c.Assert(err, gc.ErrorMatches, test.expectError)
			if test.expectCause != nil {
				c.Assert(errgo.Cause(err), gc.Equals, test.expectCause)
			}
			continue
		}
		c.Assert(gotUsername, gc.Equals, test.expectUser)
		c.Assert(gotKey, gc.DeepEquals, &key.Public)
	}
}
