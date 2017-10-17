package agent_test

import (
	"net/http"
	"time"

	"github.com/juju/httprequest"
	"github.com/juju/testing"
	"golang.org/x/net/context"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2-unstable/bakerytest"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery/agent"
)

type legacyAgentSuite struct {
	testing.LoggingSuite
	agentBakery  *bakery.Bakery
	serverBakery *bakery.Bakery
	discharger   *bakerytest.Discharger
}

type visitFunc func(w http.ResponseWriter, req *http.Request, dischargeId string) error

var _ = gc.Suite(&legacyAgentSuite{})

func (s *legacyAgentSuite) SetUpTest(c *gc.C) {
	s.LoggingSuite.SetUpTest(c)
	s.discharger = bakerytest.NewDischarger(nil)
	s.agentBakery = bakery.New(bakery.BakeryParams{
		IdentityClient: idmClient{s.discharger.Location()},
		Key:            bakery.MustGenerateKey(),
	})
	s.serverBakery = bakery.New(bakery.BakeryParams{
		Locator:        s.discharger,
		IdentityClient: idmClient{s.discharger.Location()},
		Key:            bakery.MustGenerateKey(),
	})
}

func (s *legacyAgentSuite) TearDownTest(c *gc.C) {
	s.discharger.Close()
	s.LoggingSuite.TearDownTest(c)
}

var legacyAgentLoginErrorTests = []struct {
	about string

	visitHandler visitFunc
	expectError  string
}{{
	about: "error response",
	visitHandler: func(w http.ResponseWriter, req *http.Request, dischargeId string) error {
		return errgo.Newf("test error")
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: Get http(s)?://.*: test error`,
}, {
	about: "unexpected response",
	visitHandler: func(w http.ResponseWriter, req *http.Request, dischargeId string) error {
		w.Write([]byte("OK"))
		return nil
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: Get http(s)?://.*: unexpected content type text/plain; want application/json; content: OK`,
}, {
	about: "unexpected error response",
	visitHandler: func(w http.ResponseWriter, req *http.Request, dischargeId string) error {
		httprequest.WriteJSON(w, http.StatusBadRequest, httpbakery.Error{})
		return nil
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: Get http(s)?://.*: no error message found`,
}, {
	about: "login false value",
	visitHandler: func(w http.ResponseWriter, req *http.Request, dischargeId string) error {
		httprequest.WriteJSON(w, http.StatusOK, agent.LegacyAgentResponse{})
		return nil
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: agent login failed`,
}}

func (s *legacyAgentSuite) TestAgentLoginError(c *gc.C) {
	var visit visitFunc
	s.discharger.AddHTTPHandlers(LegacyAgentHandlers(LegacyAgentHandler{
		Visit: func(p httprequest.Params, dischargeId string) error {
			if handleLoginMethods(p) {
				return nil
			}
			return visit(p.Response, p.Request, dischargeId)
		},
	}))
	rendezvous := bakerytest.NewRendezvous()
	s.discharger.Checker = httpbakery.ThirdPartyCaveatCheckerFunc(func(ctx context.Context, req *http.Request, info *bakery.ThirdPartyCaveatInfo, token *httpbakery.DischargeToken) ([]checkers.Caveat, error) {
		if token != nil {
			return nil, errgo.Newf("received unexpected discharge token")
		}
		dischargeId := rendezvous.NewDischarge(info)
		err := httpbakery.NewInteractionRequiredError(nil, req)
		err.Info = &httpbakery.ErrorInfo{
			LegacyVisitURL: "/visit?dischargeid=" + dischargeId,
			LegacyWaitURL:  "/wait?dischargeid=" + dischargeId,
		}
		return nil, err
	})

	for i, test := range legacyAgentLoginErrorTests {
		c.Logf("%d. %s", i, test.about)
		visit = test.visitHandler

		client := httpbakery.NewClient()
		err := agent.SetUpAuth(client, &agent.AuthInfo{
			Key: s.agentBakery.Oven.Key(),
			Agents: []agent.Agent{{
				URL:      s.discharger.Location(),
				Username: "test-user",
			}},
		})
		c.Assert(err, gc.IsNil)
		m, err := s.serverBakery.Oven.NewMacaroon(
			context.Background(),
			bakery.LatestVersion,
			time.Now().Add(time.Minute),
			identityCaveats(s.discharger.Location()),
			bakery.LoginOp,
		)
		c.Assert(err, gc.IsNil)
		ms, err := client.DischargeAll(context.Background(), m)
		c.Assert(err, gc.ErrorMatches, test.expectError)
		c.Assert(ms, gc.IsNil)
	}
}

func (s *legacyAgentSuite) TestSetUpAuth(c *gc.C) {
	rendezvous := bakerytest.NewRendezvous()
	s.discharger.AddHTTPHandlers(LegacyAgentHandlers(LegacyAgentHandler{
		Visit: func(p httprequest.Params, dischargeId string) error {
			if handleLoginMethods(p) {
				return nil
			}
			return s.visit(p, dischargeId, rendezvous)
		},
		Wait: func(p httprequest.Params, dischargeId string) (*bakery.Macaroon, error) {
			caveats, err := rendezvous.Await(dischargeId, 5*time.Second)
			if err != nil {
				return nil, errgo.Mask(err)
			}
			info, _ := rendezvous.Info(dischargeId)
			return s.discharger.DischargeMacaroon(p.Context, info, caveats)
		},
	}))
	s.discharger.Checker = httpbakery.ThirdPartyCaveatCheckerFunc(func(ctx context.Context, req *http.Request, info *bakery.ThirdPartyCaveatInfo, token *httpbakery.DischargeToken) ([]checkers.Caveat, error) {
		if token != nil {
			return nil, errgo.Newf("received unexpected discharge token")
		}
		dischargeId := rendezvous.NewDischarge(info)
		err := httpbakery.NewInteractionRequiredError(nil, req)
		err.Info = &httpbakery.ErrorInfo{
			LegacyVisitURL: "/visit?dischargeid=" + dischargeId,
			LegacyWaitURL:  "/wait?dischargeid=" + dischargeId,
		}
		return nil, err
	})

	client := httpbakery.NewClient()
	err := agent.SetUpAuth(client, &agent.AuthInfo{
		Key: s.agentBakery.Oven.Key(),
		Agents: []agent.Agent{{
			URL:      s.discharger.Location(),
			Username: "test-user",
		}},
	})
	c.Assert(err, gc.IsNil)
	m, err := s.serverBakery.Oven.NewMacaroon(
		context.Background(),
		bakery.LatestVersion,
		time.Now().Add(time.Minute),
		identityCaveats(s.discharger.Location()),
		bakery.LoginOp,
	)
	c.Assert(err, gc.IsNil)
	ms, err := client.DischargeAll(context.Background(), m)
	c.Assert(err, gc.IsNil)
	authInfo, err := s.serverBakery.Checker.Auth(ms).Allow(context.Background(), bakery.LoginOp)
	c.Assert(err, gc.IsNil)
	c.Assert(authInfo.Identity, gc.Equals, bakery.SimpleIdentity("test-user"))
}

func (s *legacyAgentSuite) TestNoMatchingSite(c *gc.C) {
	rendezvous := bakerytest.NewRendezvous()
	s.discharger.AddHTTPHandlers(LegacyAgentHandlers(LegacyAgentHandler{
		Visit: func(p httprequest.Params, dischargeId string) error {
			if handleLoginMethods(p) {
				return nil
			}
			return s.visit(p, dischargeId, rendezvous)
		},
		Wait: func(p httprequest.Params, dischargeId string) (*bakery.Macaroon, error) {
			_, err := rendezvous.Await(dischargeId, 5*time.Second)
			if err != nil {
				return nil, errgo.Mask(err)
			}
			return nil, errgo.Newf("rendezvous unexpectedly succeeded")
		},
	}))
	s.discharger.Checker = httpbakery.ThirdPartyCaveatCheckerFunc(func(ctx context.Context, req *http.Request, info *bakery.ThirdPartyCaveatInfo, token *httpbakery.DischargeToken) ([]checkers.Caveat, error) {
		if token != nil {
			return nil, errgo.Newf("received unexpected discharge token")
		}
		dischargeId := rendezvous.NewDischarge(info)
		err := httpbakery.NewInteractionRequiredError(nil, req)
		err.Info = &httpbakery.ErrorInfo{
			LegacyVisitURL: "/visit?dischargeid=" + dischargeId,
			LegacyWaitURL:  "/wait?dischargeid=" + dischargeId,
		}
		return nil, err
	})
	client := httpbakery.NewClient()
	err := agent.SetUpAuth(client, &agent.AuthInfo{
		Key: bakery.MustGenerateKey(),
		Agents: []agent.Agent{{
			URL:      "http://0.1.2.3/",
			Username: "test-user",
		}},
	})
	c.Assert(err, gc.IsNil)
	m, err := s.serverBakery.Oven.NewMacaroon(
		context.Background(),
		bakery.LatestVersion,
		time.Now().Add(time.Minute),
		identityCaveats(s.discharger.Location()),
		bakery.LoginOp,
	)

	c.Assert(err, gc.IsNil)
	_, err = client.DischargeAll(context.Background(), m)
	c.Assert(err, gc.ErrorMatches, `cannot get discharge from ".*": cannot start interactive session: cannot find username for discharge location ".*"`)
	_, ok := errgo.Cause(err).(*httpbakery.InteractionError)
	c.Assert(ok, gc.Equals, true)
}

type idmClient struct {
	dischargerURL string
}

func (c idmClient) IdentityFromContext(ctx context.Context) (bakery.Identity, []checkers.Caveat, error) {
	return nil, identityCaveats(c.dischargerURL), nil
}

func identityCaveats(dischargerURL string) []checkers.Caveat {
	return []checkers.Caveat{{
		Location:  dischargerURL,
		Condition: "test condition",
	}}
}

func (c idmClient) DeclaredIdentity(ctx context.Context, declared map[string]string) (bakery.Identity, error) {
	return bakery.SimpleIdentity(declared["username"]), nil
}

var ages = time.Now().Add(time.Hour)

// handleLoginMethods handles a legacy visit request
// to ask for the set of login methods.
// It reports whether it has handled the request.
func handleLoginMethods(p httprequest.Params) bool {
	if p.Request.Header.Get("Accept") != "application/json" {
		return false
	}
	httprequest.WriteJSON(p.Response, http.StatusOK, map[string]string{
		"agent": p.Request.URL.String(),
	})
	return true
}

func (s *legacyAgentSuite) visit(p httprequest.Params, dischargeId string, rendezvous *bakerytest.Rendezvous) error {
	ctx := context.TODO()
	username, userPublicKey, err := agent.LoginCookie(p.Request)
	if err != nil {
		return errgo.Notef(err, "cannot read agent login")
	}
	authInfo, authErr := s.agentBakery.Checker.Auth(httpbakery.RequestMacaroons(p.Request)...).Allow(ctx, bakery.LoginOp)
	if authErr == nil && authInfo.Identity != nil {
		rendezvous.DischargeComplete(dischargeId, []checkers.Caveat{
			checkers.DeclaredCaveat("username", authInfo.Identity.Id()),
		})
		httprequest.WriteJSON(p.Response, http.StatusOK, agent.LegacyAgentResponse{true})
		return nil
	}
	version := httpbakery.RequestVersion(p.Request)
	m, err := s.agentBakery.Oven.NewMacaroon(ctx, version, ages, []checkers.Caveat{
		bakery.LocalThirdPartyCaveat(userPublicKey, version),
		checkers.DeclaredCaveat("username", username),
	}, bakery.LoginOp)
	if err != nil {
		return errgo.Notef(err, "cannot create macaroon")
	}
	return httpbakery.NewDischargeRequiredError(httpbakery.DischargeRequiredErrorParams{
		Macaroon: m,
		Request:  p.Request,
	})
}

// LegacyAgentHandler represents a handler for legacy
// agent interactions. Each member corresponds to an HTTP endpoint,
type LegacyAgentHandler struct {
	Visit func(p httprequest.Params, dischargeId string) error
	Wait  func(p httprequest.Params, dischargeId string) (*bakery.Macaroon, error)
}

var reqServer = httprequest.Server{
	ErrorMapper: httpbakery.ErrorToResponse,
}

func LegacyAgentHandlers(h LegacyAgentHandler) []httprequest.Handler {
	return reqServer.Handlers(func(p httprequest.Params) (legacyAgentHandlers, context.Context, error) {
		return legacyAgentHandlers{h}, p.Context, nil
	})
}

type legacyAgentHandlers struct {
	h LegacyAgentHandler
}

type visitRequest struct {
	httprequest.Route `httprequest:"GET /visit"`
	DischargeId       string `httprequest:"dischargeid,form"`
}

func (h legacyAgentHandlers) Visit(p httprequest.Params, r *visitRequest) error {
	if h.h.Visit == nil {
		return errgo.Newf("visit not implemented")
	}
	return h.h.Visit(p, r.DischargeId)
}

type waitRequest struct {
	httprequest.Route `httprequest:"GET /wait"`
	DischargeId       string `httprequest:"dischargeid,form"`
}

func (h legacyAgentHandlers) Wait(p httprequest.Params, r *waitRequest) (*httpbakery.WaitResponse, error) {
	if h.h.Wait == nil {
		return nil, errgo.Newf("wait not implemented")
	}
	m, err := h.h.Wait(p, r.DischargeId)
	if err != nil {
		return nil, errgo.Mask(err, errgo.Any)
	}
	return &httpbakery.WaitResponse{
		Macaroon: m,
	}, nil
}
