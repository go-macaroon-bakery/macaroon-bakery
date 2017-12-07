package agent_test

import (
	"net/http"
	"time"

	"github.com/juju/testing"
	"golang.org/x/net/context"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/httprequest.v1"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2/bakery/identchecker"
	"gopkg.in/macaroon-bakery.v2/bakerytest"
	"gopkg.in/macaroon-bakery.v2/httpbakery"
	"gopkg.in/macaroon-bakery.v2/httpbakery/agent"
)

type legacyAgentSuite struct {
	testing.LoggingSuite
	agentBakery  *identchecker.Bakery
	serverBakery *identchecker.Bakery
	discharger   *bakerytest.Discharger
}

type visitFunc func(w http.ResponseWriter, req *http.Request, dischargeId string) error
type agentPostFunc func(httprequest.Params, agentPostRequest) error

var _ = gc.Suite(&legacyAgentSuite{})

func (s *legacyAgentSuite) SetUpTest(c *gc.C) {
	s.LoggingSuite.SetUpTest(c)
	s.discharger = bakerytest.NewDischarger(nil)
	s.agentBakery = identchecker.NewBakery(identchecker.BakeryParams{
		IdentityClient: idmClient{s.discharger.Location()},
		Key:            bakery.MustGenerateKey(),
	})
	s.serverBakery = identchecker.NewBakery(identchecker.BakeryParams{
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

	visitHandler     visitFunc
	agentPostHandler agentPostFunc
	expectError      string
}{{
	about: "error response",
	agentPostHandler: func(httprequest.Params, agentPostRequest) error {
		return errgo.Newf("test error")
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: Post http(s)?://.*: test error`,
}, {
	about: "unexpected response",
	agentPostHandler: func(p httprequest.Params, _ agentPostRequest) error {
		p.Response.Write([]byte("OK"))
		return nil
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: Post http(s)?://.*: unexpected content type text/plain; want application/json; content: OK`,
}, {
	about: "unexpected error response",
	agentPostHandler: func(p httprequest.Params, _ agentPostRequest) error {
		httprequest.WriteJSON(p.Response, http.StatusBadRequest, httpbakery.Error{})
		return nil
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: Post http(s)?://.*: no error message found`,
}, {
	about: "login false value",
	agentPostHandler: func(p httprequest.Params, _ agentPostRequest) error {
		httprequest.WriteJSON(p.Response, http.StatusOK, agent.LegacyAgentResponse{})
		return nil
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: agent login failed`,
}}

func (s *legacyAgentSuite) TestAgentLoginError(c *gc.C) {
	var agentPost agentPostFunc
	s.discharger.AddHTTPHandlers(newLegacyAgentHandlers(legacyAgentHandler{
		agentPost: func(p httprequest.Params, r agentPostRequest) error {
			return agentPost(p, r)
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
		agentPost = test.agentPostHandler

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
			identityCaveats(s.discharger.Location()),
			identchecker.LoginOp,
		)
		c.Assert(err, gc.IsNil)
		ms, err := client.DischargeAll(context.Background(), m)
		c.Assert(err, gc.ErrorMatches, test.expectError)
		c.Assert(ms, gc.IsNil)
	}
}

func (s *legacyAgentSuite) TestSetUpAuth(c *gc.C) {
	rendezvous := bakerytest.NewRendezvous()
	s.discharger.AddHTTPHandlers(newLegacyAgentHandlers(legacyAgentHandler{
		agentPost: func(p httprequest.Params, r agentPostRequest) error {
			return s.agentPost(p, r, rendezvous)
		},
		wait: func(p httprequest.Params, dischargeId string) (*bakery.Macaroon, error) {
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
		identityCaveats(s.discharger.Location()),
		identchecker.LoginOp,
	)
	c.Assert(err, gc.IsNil)
	ms, err := client.DischargeAll(context.Background(), m)
	c.Assert(err, gc.IsNil)
	authInfo, err := s.serverBakery.Checker.Auth(ms).Allow(context.Background(), identchecker.LoginOp)
	c.Assert(err, gc.IsNil)
	c.Assert(authInfo.Identity, gc.Equals, identchecker.SimpleIdentity("test-user"))
}

func (s *legacyAgentSuite) TestNoMatchingSite(c *gc.C) {
	rendezvous := bakerytest.NewRendezvous()
	s.discharger.AddHTTPHandlers(newLegacyAgentHandlers(legacyAgentHandler{
		agentPost: func(p httprequest.Params, r agentPostRequest) error {
			return s.agentPost(p, r, rendezvous)
		},
		wait: func(p httprequest.Params, dischargeId string) (*bakery.Macaroon, error) {
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
		identityCaveats(s.discharger.Location()),
		identchecker.LoginOp,
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

func (c idmClient) IdentityFromContext(ctx context.Context) (identchecker.Identity, []checkers.Caveat, error) {
	return nil, identityCaveats(c.dischargerURL), nil
}

func identityCaveats(dischargerURL string) []checkers.Caveat {
	return []checkers.Caveat{{
		Location:  dischargerURL,
		Condition: "test condition",
	}}
}

func (c idmClient) DeclaredIdentity(ctx context.Context, declared map[string]string) (identchecker.Identity, error) {
	return identchecker.SimpleIdentity(declared["username"]), nil
}

func (s *legacyAgentSuite) agentPost(p httprequest.Params, r agentPostRequest, rendezvous *bakerytest.Rendezvous) error {
	ctx := context.TODO()
	if r.Body.Username == "" || r.Body.PublicKey == nil {
		return errgo.Newf("username or public key not found")
	}
	authInfo, authErr := s.agentBakery.Checker.Auth(httpbakery.RequestMacaroons(p.Request)...).Allow(ctx, identchecker.LoginOp)
	if authErr == nil && authInfo.Identity != nil {
		rendezvous.DischargeComplete(r.DischargeId, []checkers.Caveat{
			checkers.DeclaredCaveat("username", authInfo.Identity.Id()),
		})
		httprequest.WriteJSON(p.Response, http.StatusOK, agent.LegacyAgentResponse{true})
		return nil
	}
	version := httpbakery.RequestVersion(p.Request)
	m, err := s.agentBakery.Oven.NewMacaroon(ctx, version, []checkers.Caveat{
		bakery.LocalThirdPartyCaveat(r.Body.PublicKey, version),
		checkers.DeclaredCaveat("username", r.Body.Username),
	}, identchecker.LoginOp)
	if err != nil {
		return errgo.Notef(err, "cannot create macaroon")
	}
	return httpbakery.NewDischargeRequiredError(httpbakery.DischargeRequiredErrorParams{
		Macaroon: m,
		Request:  p.Request,
	})
}

// legacyAgentHandler represents a handler for legacy
// agent interactions. Each member corresponds to an HTTP endpoint,
type legacyAgentHandler struct {
	agentPost agentPostFunc
	wait      func(p httprequest.Params, dischargeId string) (*bakery.Macaroon, error)
}

var reqServer = httprequest.Server{
	ErrorMapper: httpbakery.ErrorToResponse,
}

func newLegacyAgentHandlers(h legacyAgentHandler) []httprequest.Handler {
	return reqServer.Handlers(func(p httprequest.Params) (legacyAgentHandlers, context.Context, error) {
		return legacyAgentHandlers{h}, p.Context, nil
	})
}

type legacyAgentHandlers struct {
	h legacyAgentHandler
}

type visitGetRequest struct {
	httprequest.Route `httprequest:"GET /visit"`
	DischargeId       string `httprequest:"dischargeid,form"`
}

func (h legacyAgentHandlers) VisitGet(p httprequest.Params, r *visitGetRequest) error {
	return handleLoginMethods(p, r.DischargeId)
}

// handleLoginMethods handles a legacy visit request
// to ask for the set of login methods.
// It reports whether it has handled the request.
func handleLoginMethods(p httprequest.Params, dischargeId string) error {
	if p.Request.Header.Get("Accept") != "application/json" {
		return errgo.Newf("got normal visit request", http.StatusInternalServerError)
	}
	httprequest.WriteJSON(p.Response, http.StatusOK, map[string]string{
		"agent": "/agent?discharge-id=" + dischargeId,
	})
	return nil
}

type agentPostRequest struct {
	httprequest.Route `httprequest:"POST /agent"`
	DischargeId       string                     `httprequest:"discharge-id,form"`
	Body              agent.LegacyAgentLoginBody `httprequest:",body"`
}

func (h legacyAgentHandlers) AgentPost(p httprequest.Params, r *agentPostRequest) error {
	if h.h.agentPost == nil {
		return errgo.Newf("agent POST not implemented")
	}
	return h.h.agentPost(p, *r)
}

type waitRequest struct {
	httprequest.Route `httprequest:"GET /wait"`
	DischargeId       string `httprequest:"dischargeid,form"`
}

func (h legacyAgentHandlers) Wait(p httprequest.Params, r *waitRequest) (*httpbakery.WaitResponse, error) {
	if h.h.wait == nil {
		return nil, errgo.Newf("wait not implemented")
	}
	m, err := h.h.wait(p, r.DischargeId)
	if err != nil {
		return nil, errgo.Mask(err, errgo.Any)
	}
	return &httpbakery.WaitResponse{
		Macaroon: m,
	}, nil
}
