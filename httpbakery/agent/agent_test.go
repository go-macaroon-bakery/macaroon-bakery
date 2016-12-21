package agent_test

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/juju/httprequest"
	jc "github.com/juju/testing/checkers"
	"golang.org/x/net/context"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2-unstable/bakerytest"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery/agent"
)

var _ httpbakery.Visitor = (*agent.Visitor)(nil)

type agentSuite struct {
	agentBakery *bakery.Bakery
	bakery      *bakery.Bakery
	discharger  *bakerytest.InteractiveDischarger
	handle      func(ctx context.Context, w http.ResponseWriter, req *http.Request)
}

var _ = gc.Suite(&agentSuite{})

func (s *agentSuite) SetUpTest(c *gc.C) {
	s.discharger = bakerytest.NewInteractiveDischarger(nil, s)

	key, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	s.agentBakery = bakery.New(bakery.BakeryParams{
		IdentityClient: idmClient{s.discharger.Location()},
		Key:            key,
	})

	key, err = bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	s.bakery = bakery.New(bakery.BakeryParams{
		Locator:        s.discharger,
		IdentityClient: idmClient{s.discharger.Location()},
		Key:            key,
	})
	s.handle = nil
}

func (s *agentSuite) TearDownTest(c *gc.C) {
	s.discharger.Close()
}

var agentLoginTests = []struct {
	about        string
	loginHandler func(context.Context, http.ResponseWriter, *http.Request)
	expectError  string
}{{
	about: "success",
}, {
	about: "error response",
	loginHandler: func(_ context.Context, w http.ResponseWriter, _ *http.Request) {
		httprequest.WriteJSON(w, http.StatusBadRequest, httpbakery.Error{
			Code:    "bad request",
			Message: "test error",
		})
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: Get http(s)?://.*: test error`,
}, {
	about: "unexpected response",
	loginHandler: func(_ context.Context, w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("OK"))
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: Get http(s)?://.*: unexpected content type text/plain; want application/json; content: OK`,
}, {
	about: "unexpected error response",
	loginHandler: func(_ context.Context, w http.ResponseWriter, _ *http.Request) {
		httprequest.WriteJSON(w, http.StatusBadRequest, httpbakery.Error{})
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: Get http(s)?://.*: no error message found`,
}, {
	about: "incorrect JSON",
	loginHandler: func(_ context.Context, w http.ResponseWriter, _ *http.Request) {
		httprequest.WriteJSON(w, http.StatusOK, httpbakery.Error{
			Code:    "bad request",
			Message: "test error",
		})
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: agent login failed`,
}}

func (s *agentSuite) TestAgentLogin(c *gc.C) {
	for i, test := range agentLoginTests {
		c.Logf("%d. %s", i, test.about)
		s.handle = test.loginHandler
		key, err := bakery.GenerateKey()
		c.Assert(err, gc.IsNil)
		visitor := new(agent.Visitor)
		err = visitor.AddAgent(agent.Agent{URL: s.discharger.Location(), Username: "test-user", Key: key})
		c.Assert(err, gc.IsNil)
		client := httpbakery.NewClient()
		client.WebPageVisitor = visitor
		m, err := s.bakery.Oven.NewMacaroon(
			context.Background(),
			bakery.LatestVersion,
			time.Now().Add(time.Minute),
			identityCaveats(s.discharger.Location()),
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
		c.Assert(authInfo.Identity, gc.Equals, bakery.SimpleIdentity("test-user"))
	}
}

func (s *agentSuite) TestSetUpAuth(c *gc.C) {
	client := httpbakery.NewClient()
	var err error
	client.Key, err = bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	err = agent.SetUpAuth(client, s.discharger.Location(), "test-user")
	c.Assert(err, gc.IsNil)
	m, err := s.bakery.Oven.NewMacaroon(
		context.Background(),
		bakery.LatestVersion,
		time.Now().Add(time.Minute),
		identityCaveats(s.discharger.Location()),
		bakery.LoginOp,
	)
	c.Assert(err, gc.IsNil)
	ms, err := client.DischargeAll(context.Background(), m)
	c.Assert(err, gc.IsNil)
	authInfo, err := s.bakery.Checker.Auth(ms).Allow(context.Background(), bakery.LoginOp)
	c.Assert(err, gc.IsNil)
	c.Assert(authInfo.Identity, gc.Equals, bakery.SimpleIdentity("test-user"))
}

func (s *agentSuite) TestNoCookieError(c *gc.C) {
	client := httpbakery.NewClient()
	client.WebPageVisitor = new(agent.Visitor)
	m, err := s.bakery.Oven.NewMacaroon(
		context.Background(), bakery.LatestVersion,

		time.Now().Add(time.Minute),
		identityCaveats(s.discharger.Location()),
		bakery.LoginOp,
	)

	c.Assert(err, gc.IsNil)
	_, err = client.DischargeAll(context.Background(), m)
	c.Assert(err, gc.ErrorMatches, "cannot get discharge from .*: cannot start interactive session: no suitable agent found")
	_, ok := errgo.Cause(err).(*httpbakery.InteractionError)
	c.Assert(ok, gc.Equals, true)
}

func (s *agentSuite) TestMultipleAgents(c *gc.C) {
	u := s.discharger.Location()

	visitor := new(agent.Visitor)
	key1, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	visitor.AddAgent(agent.Agent{URL: u, Username: "test-user-1", Key: key1})
	key2, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	visitor.AddAgent(agent.Agent{URL: u + "/visit", Username: "test-user-2", Key: key2})
	key3, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	visitor.AddAgent(agent.Agent{URL: u + "/visit", Username: "test-user-3", Key: key3})
	key4, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	visitor.AddAgent(agent.Agent{URL: u + "/visit/visit", Username: "test-user-4", Key: key4})
	key5, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	visitor.AddAgent(agent.Agent{URL: u + "/discharge", Username: "test-user-5", Key: key5})
	s.handle = func(ctx context.Context, w http.ResponseWriter, req *http.Request) {
		username, userPublicKey, err := agent.LoginCookie(req)
		if err != nil {
			httprequest.WriteJSON(w, http.StatusBadRequest, httpbakery.Error{
				Code:    "bad request",
				Message: err.Error(),
			})
			return
		}
		if username != "test-user-3" {
			httprequest.WriteJSON(w, http.StatusBadRequest, httpbakery.Error{
				Code:    "bad request",
				Message: fmt.Sprintf(`got unexpected user %q, expected "test-user-3"`, username),
			})
			return
		}
		if *userPublicKey != key3.Public {
			httprequest.WriteJSON(w, http.StatusBadRequest, httpbakery.Error{
				Code:    "bad request",
				Message: `got unexpected public key`,
			})
			return
		}
		s.defaultHandle(ctx, w, req)
	}
	client := httpbakery.NewClient()
	client.WebPageVisitor = visitor
	m, err := s.bakery.Oven.NewMacaroon(
		context.Background(),
		bakery.LatestVersion,
		time.Now().Add(time.Minute),
		identityCaveats(s.discharger.Location()),
		bakery.LoginOp,
	)
	c.Assert(err, gc.IsNil)
	ms, err := client.DischargeAll(context.Background(), m)
	c.Assert(err, gc.IsNil)
	authInfo, err := s.bakery.Checker.Auth(ms).Allow(context.Background(), bakery.LoginOp)
	c.Assert(err, gc.IsNil)
	c.Assert(authInfo.Identity, gc.Equals, bakery.SimpleIdentity("test-user-3"))
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
	return bakery.SimpleIdentity(declared["username"]), nil
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

var ages = time.Now().Add(time.Hour)

// Serve HTTP implements the default login handler for the agent login
// tests. This is overrided by s.handle if it is non-nil.
func (s *agentSuite) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// TODO take context from request.
	ctx := httpbakery.ContextWithRequest(context.TODO(), req)
	req.ParseForm()
	if s.handle != nil {
		s.handle(ctx, w, req)
		return
	}
	s.defaultHandle(ctx, w, req)
}

func (s *agentSuite) defaultHandle(ctx context.Context, w http.ResponseWriter, req *http.Request) {
	username, userPublicKey, err := agent.LoginCookie(req)
	if err != nil {
		httprequest.WriteJSON(w, http.StatusBadRequest, httpbakery.Error{
			Message: fmt.Sprintf("cannot read agent login: %s", err),
		})
		return
	}
	_, authErr := s.agentBakery.Checker.Auth(httpbakery.RequestMacaroons(req)...).Allow(ctx, bakery.LoginOp)
	if authErr == nil {
		cavs := []checkers.Caveat{
			checkers.DeclaredCaveat("username", username),
		}
		s.discharger.FinishInteraction(ctx, w, req, cavs, nil)
		httprequest.WriteJSON(w, http.StatusOK, agent.AgentResponse{
			AgentLogin: true,
		})
		return
	}
	version := httpbakery.RequestVersion(req)
	m, err := s.agentBakery.Oven.NewMacaroon(ctx, version, ages, []checkers.Caveat{
		bakery.LocalThirdPartyCaveat(userPublicKey, version),
		checkers.DeclaredCaveat("username", username),
	}, bakery.LoginOp)

	if err != nil {
		httprequest.WriteJSON(w, http.StatusInternalServerError, httpbakery.Error{
			Message: fmt.Sprintf("cannot create macaroon: %s", err),
		})
		return
	}
	httpbakery.WriteDischargeRequiredError(w, m, "", authErr)
}
