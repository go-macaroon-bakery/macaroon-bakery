package agent_test

import (
	"encoding/base64"
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
	c.Assert(err, gc.ErrorMatches, "cannot get discharge from .*: cannot start interactive session: cannot perform agent login: no agent-login cookie found")
	ierr := errgo.Cause(err).(*httpbakery.InteractionError)
	c.Assert(errgo.Cause(ierr.Reason), gc.Equals, http.ErrNoCookie)
}

func (s *agentSuite) TestLoginCookie(c *gc.C) {
	key, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)

	tests := []struct {
		about       string
		setCookie   func(*httpbakery.Client, *url.URL)
		expectUser  string
		expectKey   *bakery.PublicKey
		expectError string
		expectCause error
	}{{
		about: "success",
		setCookie: func(client *httpbakery.Client, u *url.URL) {
			agent.SetUpAuth(client, u, "bob")
		},
		expectUser: "bob",
		expectKey:  &key.Public,
	}, {
		about:       "no cookie",
		setCookie:   func(client *httpbakery.Client, u *url.URL) {},
		expectError: "no agent-login cookie found",
		expectCause: agent.ErrNoAgentLoginCookie,
	}, {
		about: "invalid base64 encoding",
		setCookie: func(client *httpbakery.Client, u *url.URL) {
			client.Jar.SetCookies(u, []*http.Cookie{{
				Name:  "agent-login",
				Value: "x",
			}})
		},
		expectError: "cannot decode cookie value: illegal base64 data at input byte 0",
	}, {
		about: "invalid JSON",
		setCookie: func(client *httpbakery.Client, u *url.URL) {
			client.Jar.SetCookies(u, []*http.Cookie{{
				Name:  "agent-login",
				Value: base64.StdEncoding.EncodeToString([]byte("}")),
			}})
		},
		expectError: "cannot unmarshal agent login: invalid character '}' looking for beginning of value",
	}, {
		about: "no username",
		setCookie: func(client *httpbakery.Client, u *url.URL) {
			agent.SetCookie(client.Jar, u, "", &key.Public)
		},
		expectError: "agent login has no user name",
	}, {
		about: "no public key",
		setCookie: func(client *httpbakery.Client, u *url.URL) {
			agent.SetCookie(client.Jar, u, "hello", nil)
		},
		expectError: "agent login has no public key",
	}}
	var (
		foundUser string
		foundKey  *bakery.PublicKey
		foundErr  error
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		foundUser, foundKey, foundErr = agent.LoginCookie(req)
	}))
	defer srv.Close()

	srvURL, err := url.Parse(srv.URL)
	c.Assert(err, gc.IsNil)

	for i, test := range tests {
		c.Logf("test %d: %s", i, test.about)

		client := httpbakery.NewClient()
		client.Key = key
		test.setCookie(client, srvURL)

		req, err := http.NewRequest("GET", srv.URL, nil)
		c.Assert(err, gc.IsNil)
		resp, err := client.Do(req)
		c.Assert(err, gc.IsNil)
		c.Assert(resp.StatusCode, gc.Equals, http.StatusOK)
		if test.expectError != "" {
			c.Assert(foundErr, gc.ErrorMatches, test.expectError)
			if test.expectCause != nil {
				c.Assert(errgo.Cause(foundErr), gc.Equals, test.expectCause)
			}
			continue
		}
		c.Assert(foundUser, gc.Equals, test.expectUser)
		c.Assert(foundKey, gc.DeepEquals, test.expectKey)
	}
}

func ExampleVisitWebPage() {
	var key *bakery.KeyPair
	var u *url.URL

	client := httpbakery.NewClient()
	client.Key = key
	agent.SetCookie(client.Jar, u, "agent-username", &client.Key.Public)
	client.VisitWebPage = agent.VisitWebPage(client)
}
