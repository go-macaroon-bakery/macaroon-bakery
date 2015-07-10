package agent_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"

	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v1/bakery"
	"gopkg.in/macaroon-bakery.v1/bakery/checkers"
	"gopkg.in/macaroon-bakery.v1/httpbakery"
	"gopkg.in/macaroon-bakery.v1/httpbakery/agent"
)

type agentSuite struct {
	bakery       *bakery.Service
	dischargeKey *bakery.PublicKey
	discharger   *Discharger
	server       *httptest.Server
}

var _ = gc.Suite(&agentSuite{})

func (s *agentSuite) SetUpSuite(c *gc.C) {
	key, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	s.dischargeKey = &key.Public
	c.Assert(err, gc.IsNil)
	bak, err := bakery.NewService(bakery.NewServiceParams{
		Key: key,
	})
	c.Assert(err, gc.IsNil)
	s.discharger = &Discharger{
		Bakery: bak,
	}
	s.server = s.discharger.Serve()
	s.bakery, err = bakery.NewService(bakery.NewServiceParams{
		Locator: bakery.PublicKeyLocatorMap{
			s.discharger.URL: &key.Public,
		},
	})
}

func (s *agentSuite) TearDownSuite(c *gc.C) {
	s.server.Close()
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
		d.WriteJSON(w, http.StatusBadRequest, httpbakery.Error{
			Code:    "bad request",
			Message: "test error",
		})
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: test error`,
}, {
	about: "unexpected response",
	loginHandler: func(d *Discharger, w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("OK"))
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: unexpected response to non-interactive web page visit .* \(content type text/plain; charset=utf-8\)`,
}, {
	about: "unexpected error response",
	loginHandler: func(d *Discharger, w http.ResponseWriter, _ *http.Request) {
		d.WriteJSON(w, http.StatusBadRequest, httpbakery.Error{})
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: unexpected response to non-interactive web page visit .* \(content type application/json\)`,
}, {
	about: "incorrect JSON",
	loginHandler: func(d *Discharger, w http.ResponseWriter, _ *http.Request) {
		d.WriteJSON(w, http.StatusOK, httpbakery.Error{
			Code:    "bad request",
			Message: "test error",
		})
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: unexpected response to non-interactive web page visit .* \(content type application/json\)`,
}}

func (s *agentSuite) TestAgentLogin(c *gc.C) {
	u, err := url.Parse(s.discharger.URL)
	c.Assert(err, gc.IsNil)
	for i, test := range agentLoginTests {
		c.Logf("%d. %s", i, test.about)
		s.discharger.LoginHandler = test.loginHandler
		client := httpbakery.NewClient()
		client.Key, err = bakery.GenerateKey()
		c.Assert(err, gc.IsNil)
		err = agent.SetUpAuth(client, u, "test-user")
		c.Assert(err, gc.IsNil)
		m, err := s.bakery.NewMacaroon("", nil, []checkers.Caveat{{
			Location:  s.discharger.URL,
			Condition: "test condition",
		}})
		c.Assert(err, gc.IsNil)
		ms, err := client.DischargeAll(m)
		if test.expectError != "" {
			c.Assert(err, gc.ErrorMatches, test.expectError)
			continue
		}
		c.Assert(err, gc.IsNil)
		err = s.bakery.Check(ms, bakery.FirstPartyCheckerFunc(
			func(caveat string) error {
				return nil
			},
		))
		c.Assert(err, gc.IsNil)
	}
}

func (s *agentSuite) TestSetUpAuthError(c *gc.C) {
	client := httpbakery.NewClient()
	err := agent.SetUpAuth(client, nil, "test-user")
	c.Assert(err, gc.ErrorMatches, "cannot set-up authentication: client key not configured")
}

func (s *agentSuite) TestNoCookieError(c *gc.C) {
	client := httpbakery.NewClient()
	client.VisitWebPage = agent.VisitWebPage(client)
	m, err := s.bakery.NewMacaroon("", nil, []checkers.Caveat{{
		Location:  s.discharger.URL,
		Condition: "test condition",
	}})
	c.Assert(err, gc.IsNil)
	_, err = client.DischargeAll(m)
	c.Assert(err, gc.ErrorMatches, "cannot get discharge from .*: cannot start interactive session: cannot perform agent login: http: named cookie not present")
	ierr := errgo.Cause(err).(*httpbakery.InteractionError)
	c.Assert(errgo.Cause(ierr.Reason), gc.Equals, http.ErrNoCookie)
}

func ExampleVisitWebPage() {
	var key *bakery.KeyPair
	var u *url.URL

	client := httpbakery.NewClient()
	client.Key = key
	agent.SetCookie(client.Jar, u, "agent-username", &client.Key.Public)
	client.VisitWebPage = agent.VisitWebPage(client)
}
