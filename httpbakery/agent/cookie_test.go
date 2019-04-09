package agent_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"

	qt "github.com/frankban/quicktest"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/httpbakery/agent"
)

var loginCookieTests = []struct {
	about       string
	addCookie   func(*http.Request, *bakery.PublicKey)
	expectUser  string
	expectError string
	expectCause error
}{{
	about: "success",
	addCookie: func(req *http.Request, key *bakery.PublicKey) {
		addCookie(req, "bob", key)
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
		addCookie(req, "", key)
	},
	expectError: "agent login has no user name",
}, {
	about: "no public key",
	addCookie: func(req *http.Request, key *bakery.PublicKey) {
		addCookie(req, "bob", nil)
	},
	expectError: "agent login has no public key",
}}

func TestLoginCookie(t *testing.T) {
	c := qt.New(t)
	key, err := bakery.GenerateKey()
	c.Assert(err, qt.IsNil)

	for i, test := range loginCookieTests {
		c.Logf("test %d: %s", i, test.about)

		req, err := http.NewRequest("GET", "", nil)
		c.Assert(err, qt.IsNil)
		test.addCookie(req, &key.Public)
		gotUsername, gotKey, err := agent.LoginCookie(req)

		if test.expectError != "" {
			c.Assert(err, qt.ErrorMatches, test.expectError)
			if test.expectCause != nil {
				c.Assert(errgo.Cause(err), qt.Equals, test.expectCause)
			}
			continue
		}
		c.Assert(gotUsername, qt.Equals, test.expectUser)
		c.Assert(gotKey, qt.DeepEquals, &key.Public)
	}
}

// addCookie adds an agent-login cookie with the specified parameters to
// the given request.
func addCookie(req *http.Request, username string, key *bakery.PublicKey) {
	al := agent.AgentLogin{
		Username:  username,
		PublicKey: key,
	}
	data, err := json.Marshal(al)
	if err != nil {
		// This should be impossible as the agentLogin structure
		// has to be marshalable. It is certainly a bug if it
		// isn't.
		panic(errgo.Notef(err, "cannot marshal %s cookie", agent.CookieName))
	}
	req.AddCookie(&http.Cookie{
		Name:  agent.CookieName,
		Value: base64.StdEncoding.EncodeToString(data),
	})
}
