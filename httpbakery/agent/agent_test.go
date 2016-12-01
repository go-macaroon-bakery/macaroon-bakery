package agent_test

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"time"

	jc "github.com/juju/testing/checkers"
	"golang.org/x/net/context"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v2-unstable"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery/agent"
)

var _ httpbakery.Visitor = (*agent.Visitor)(nil)

type agentSuite struct {
	bakery       *bakery.Bakery
	dischargeKey *bakery.PublicKey
	discharger   *Discharger
	server       *httptest.Server
}

var _ = gc.Suite(&agentSuite{})

func (s *agentSuite) SetUpSuite(c *gc.C) {
	locator := bakery.NewThirdPartyStore()
	s.discharger = newDischarger(c, locator)

	key, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	s.bakery = bakery.New(bakery.BakeryParams{
		Locator:        locator,
		IdentityClient: idmClient{s.discharger.URL},
		Key:            key,
	})
}

func (s *agentSuite) TearDownSuite(c *gc.C) {
	s.discharger.Close()
}

var agentLoginTests = []struct {
	about        string
	loginHandler func(*Discharger, http.ResponseWriter, *http.Request)
	expectError  string
}{{
	about: "success",
}, {
	about: "error response",
	loginHandler: func(d *Discharger, w http.ResponseWriter, _ *http.Request) {
		d.writeJSON(w, http.StatusBadRequest, httpbakery.Error{
			Code:    "bad request",
			Message: "test error",
		})
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: Get http://.*: test error`,
}, {
	about: "unexpected response",
	loginHandler: func(d *Discharger, w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("OK"))
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: Get http://.*: unexpected content type text/plain; want application/json; content: OK`,
}, {
	about: "unexpected error response",
	loginHandler: func(d *Discharger, w http.ResponseWriter, _ *http.Request) {
		d.writeJSON(w, http.StatusBadRequest, httpbakery.Error{})
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: Get http://.*: no error message found`,
}, {
	about: "incorrect JSON",
	loginHandler: func(d *Discharger, w http.ResponseWriter, _ *http.Request) {
		d.writeJSON(w, http.StatusOK, httpbakery.Error{
			Code:    "bad request",
			Message: "test error",
		})
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: agent login failed`,
}}

func (s *agentSuite) TestAgentLogin(c *gc.C) {
	for i, test := range agentLoginTests {
		c.Logf("%d. %s", i, test.about)
		s.discharger.LoginHandler = test.loginHandler
		key, err := bakery.GenerateKey()
		c.Assert(err, gc.IsNil)
		visitor := new(agent.Visitor)
		err = visitor.AddAgent(agent.Agent{URL: s.discharger.URL, Username: "test-user", Key: key})
		c.Assert(err, gc.IsNil)
		client := httpbakery.NewClient()
		client.WebPageVisitor = visitor
		m, err := s.bakery.Oven.NewMacaroon(
			context.Background(),
			macaroon.LatestVersion,
			time.Now().Add(time.Minute),
			identityCaveats(s.discharger.URL),
			bakery.LoginOp,
		)
		c.Assert(err, gc.IsNil)
		ms, err := client.DischargeAll(context.Background(), m)
		if test.expectError != "" {
			c.Assert(err, gc.ErrorMatches, test.expectError)
			continue
		}
		c.Assert(err, gc.IsNil)
		authInfo, err := s.bakery.Checker.Auth(ms).Allow(context.Background(), bakery.LoginOp)
		c.Assert(err, gc.IsNil)
		c.Assert(authInfo.Identity, gc.Equals, simpleIdentity("test-user"))
	}
}

func (s *agentSuite) TestNoCookieError(c *gc.C) {
	client := httpbakery.NewClient()
	client.WebPageVisitor = new(agent.Visitor)
	m, err := s.bakery.Oven.NewMacaroon(
		context.Background(),
		macaroon.LatestVersion,
		time.Now().Add(time.Minute),
		identityCaveats(s.discharger.URL),
		bakery.LoginOp,
	)

	c.Assert(err, gc.IsNil)
	_, err = client.DischargeAll(context.Background(), m)
	c.Assert(err, gc.ErrorMatches, "cannot get discharge from .*: cannot start interactive session: no suitable agent found")
	_, ok := errgo.Cause(err).(*httpbakery.InteractionError)
	c.Assert(ok, gc.Equals, true)
}

func (s *agentSuite) TestMultipleAgents(c *gc.C) {
	u := s.discharger.URL

	visitor := new(agent.Visitor)
	key1, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	visitor.AddAgent(agent.Agent{URL: u, Username: "test-user-1", Key: key1})
	key2, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	visitor.AddAgent(agent.Agent{URL: u + "/login", Username: "test-user-2", Key: key2})
	key3, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	visitor.AddAgent(agent.Agent{URL: u + "/login", Username: "test-user-3", Key: key3})
	key4, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	visitor.AddAgent(agent.Agent{URL: u + "/login/login", Username: "test-user-4", Key: key4})
	key5, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	visitor.AddAgent(agent.Agent{URL: u + "/discharge", Username: "test-user-5", Key: key5})
	s.discharger.LoginHandler = func(d *Discharger, w http.ResponseWriter, req *http.Request) {
		al, err := d.GetAgentLogin(req)
		if err != nil {
			d.writeJSON(w, http.StatusBadRequest, httpbakery.Error{
				Code:    "bad request",
				Message: err.Error(),
			})
			return
		}
		if al.Username != "test-user-3" {
			d.writeJSON(w, http.StatusBadRequest, httpbakery.Error{
				Code:    "bad request",
				Message: fmt.Sprintf(`got unexpected user %q, expected "test-user-3"`, al.Username),
			})
			return
		}
		if *al.PublicKey != key3.Public {
			d.writeJSON(w, http.StatusBadRequest, httpbakery.Error{
				Code:    "bad request",
				Message: `got unexpected public key`,
			})
			return
		}
		d.LoginHandler = nil
		d.login(w, req)
	}
	client := httpbakery.NewClient()
	client.WebPageVisitor = visitor
	m, err := s.bakery.Oven.NewMacaroon(
		context.Background(),
		macaroon.LatestVersion,
		time.Now().Add(time.Minute),
		identityCaveats(s.discharger.URL),
		bakery.LoginOp,
	)
	c.Assert(err, gc.IsNil)
	ms, err := client.DischargeAll(context.Background(), m)
	c.Assert(err, gc.IsNil)
	authInfo, err := s.bakery.Checker.Auth(ms).Allow(context.Background(), bakery.LoginOp)
	c.Assert(err, gc.IsNil)
	c.Assert(authInfo.Identity, gc.Equals, simpleIdentity("test-user-3"))
}

func (s *agentSuite) TestLoginCookie(c *gc.C) {
	key, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)

	tests := []struct {
		about       string
		setCookie   func(*http.Request)
		expectUser  string
		expectKey   *bakery.PublicKey
		expectError string
		expectCause error
	}{{
		about: "success",
		setCookie: func(req *http.Request) {
			agent.SetCookie(req, "bob", &key.Public)
		},
		expectUser: "bob",
		expectKey:  &key.Public,
	}, {
		about:       "no cookie",
		setCookie:   func(req *http.Request) {},
		expectError: "no agent-login cookie found",
		expectCause: agent.ErrNoAgentLoginCookie,
	}, {
		about: "invalid base64 encoding",
		setCookie: func(req *http.Request) {
			req.AddCookie(&http.Cookie{
				Name:  "agent-login",
				Value: "x",
			})
		},
		expectError: "cannot decode cookie value: illegal base64 data at input byte 0",
	}, {
		about: "invalid JSON",
		setCookie: func(req *http.Request) {
			req.AddCookie(&http.Cookie{
				Name:  "agent-login",
				Value: base64.StdEncoding.EncodeToString([]byte("}")),
			})
		},
		expectError: "cannot unmarshal agent login: invalid character '}' looking for beginning of value",
	}, {
		about: "no username",
		setCookie: func(req *http.Request) {
			agent.SetCookie(req, "", &key.Public)
		},
		expectError: "agent login has no user name",
	}, {
		about: "no public key",
		setCookie: func(req *http.Request) {
			agent.SetCookie(req, "bob", nil)
		},
		expectError: "agent login has no public key",
	}}

	for i, test := range tests {
		c.Logf("test %d: %s", i, test.about)

		req, err := http.NewRequest("GET", "", nil)
		c.Assert(err, gc.IsNil)
		test.setCookie(req)
		username, key, err := agent.LoginCookie(req)

		if test.expectError != "" {
			c.Assert(err, gc.ErrorMatches, test.expectError)
			if test.expectCause != nil {
				c.Assert(errgo.Cause(err), gc.Equals, test.expectCause)
			}
			continue
		}
		c.Assert(username, gc.Equals, test.expectUser)
		c.Assert(key, gc.DeepEquals, test.expectKey)
	}
}

var findAgentTests = []struct {
	about          string
	agents         []agent.Agent
	url            string
	expectUsername string
}{{
	about: "no agents",
	url:   "http://foo.com/",
}, {
	about: "one agent, empty paths",
	agents: []agent.Agent{{
		Username: "bob",
		URL:      "http://foo.com",
	}},
	url:            "http://foo.com",
	expectUsername: "bob",
}, {
	about: "one agent, agent URL ends with slash, request URL does not",
	agents: []agent.Agent{{
		Username: "bob",
		URL:      "http://foo.com/",
	}},
	url:            "http://foo.com",
	expectUsername: "bob",
}, {
	about: "one agent, agent URL does not end with slash, request URL does",
	agents: []agent.Agent{{
		Username: "bob",
		URL:      "http://foo.com",
	}},
	url:            "http://foo.com/",
	expectUsername: "bob",
}, {
	about: "one agent, longer path, match",
	agents: []agent.Agent{{
		Username: "bob",
		URL:      "http://foo.com/foo",
	}},
	url:            "http://foo.com/foo",
	expectUsername: "bob",
}, {
	about: "one agent, path with trailing slash, match",
	agents: []agent.Agent{{
		Username: "bob",
		URL:      "http://foo.com/foo/",
	}},
	url:            "http://foo.com/foo",
	expectUsername: "bob",
}, {
	about: "one agent, should not match matching prefix with non-separated element",
	agents: []agent.Agent{{
		Username: "bob",
		URL:      "http://foo.com/foo",
	}},
	url: "http://foo.com/foobar",
}, {
	about: "two matching agents, should match longer URL",
	agents: []agent.Agent{{
		Username: "bob",
		URL:      "http://foo.com/foo/bar",
	}, {
		Username: "alice",
		URL:      "http://foo.com/foo",
	}},
	url:            "http://foo.com/foo/bar/something",
	expectUsername: "bob",
}, {
	about: "two matching agents with different hosts",
	agents: []agent.Agent{{
		Username: "bob",
		URL:      "http://foo.com/foo/bar",
	}, {
		Username: "alice",
		URL:      "http://bar.com/foo",
	}},
	url:            "http://bar.com/foo/bar/something",
	expectUsername: "alice",
}, {
	about: "matching URL is replaced",
	agents: []agent.Agent{{
		Username: "bob",
		URL:      "http://foo.com/foo",
	}, {
		Username: "alice",
		URL:      "http://foo.com/foo",
	}},
	url:            "http://foo.com/foo/bar/something",
	expectUsername: "alice",
}}

func (s *agentSuite) TestFindAgent(c *gc.C) {
	for i, test := range findAgentTests {
		c.Logf("test %d: %s", i, test.about)
		var v agent.Visitor
		for _, a := range test.agents {
			a.Key = testKey
			err := v.AddAgent(a)
			c.Assert(err, gc.IsNil)
		}
		u, err := url.Parse(test.url)
		c.Assert(err, gc.IsNil)
		found, ok := agent.FindAgent(&v, u)
		if test.expectUsername == "" {
			c.Assert(ok, gc.Equals, false)
			continue
		}
		c.Assert(found.Username, gc.Equals, test.expectUsername)
	}
}

func (s *agentSuite) TestAgents(c *gc.C) {
	agents := []agent.Agent{{
		URL:      "http://bar.com/x",
		Username: "alice",
		Key:      testKey,
	}, {
		URL:      "http://foo.com",
		Username: "bob",
		Key:      testKey,
	}, {
		URL:      "http://foo.com/x",
		Username: "charlie",
		Key:      testKey,
	}}
	var v agent.Visitor
	for _, a := range agents {
		err := v.AddAgent(a)
		c.Assert(err, gc.IsNil)
	}
	c.Assert(v.Agents(), jc.DeepEquals, agents)
}

func ExampleVisitWebPage() {
	var key *bakery.KeyPair

	visitor := new(agent.Visitor)
	err := visitor.AddAgent(agent.Agent{
		URL:      "http://foo.com",
		Username: "agent-username",
		Key:      key,
	})
	if err != nil {
		// handle error
	}
	client := httpbakery.NewClient()
	client.WebPageVisitor = visitor
}

type idmClient struct {
	dischargerURL string
}

func (c idmClient) IdentityFromContext(ctxt context.Context) (bakery.Identity, []checkers.Caveat, error) {
	return nil, identityCaveats(c.dischargerURL), nil
}

func identityCaveats(dischargerURL string) []checkers.Caveat {
	return []checkers.Caveat{{
		Location:  dischargerURL,
		Condition: "test condition",
	}}
}

func (c idmClient) DeclaredIdentity(declared map[string]string) (bakery.Identity, error) {
	return simpleIdentity(declared["username"]), nil
}

type simpleIdentity string

func (simpleIdentity) Domain() string {
	return ""
}

func (id simpleIdentity) Id() string {
	return string(id)
}

func mustParseURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

var testKey = func() *bakery.KeyPair {
	key, err := bakery.GenerateKey()
	if err != nil {
		panic(err)
	}
	return key
}()
