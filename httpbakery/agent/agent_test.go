package agent_test

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"os"

	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	"golang.org/x/net/context"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/httprequest.v1"
	"gopkg.in/macaroon.v2"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2/bakerytest"
	"gopkg.in/macaroon-bakery.v2/httpbakery"
	"gopkg.in/macaroon-bakery.v2/httpbakery/agent"
)

type agentSuite struct {
	testing.LoggingSuite
	agentBakery  *bakery.Bakery
	serverBakery *bakery.Bakery
	discharger   *bakerytest.Discharger
}

var _ = gc.Suite(&agentSuite{})

func (s *agentSuite) SetUpTest(c *gc.C) {
	s.LoggingSuite.SetUpTest(c)
	s.discharger = bakerytest.NewDischarger(nil)
	s.agentBakery = bakery.New(bakery.BakeryParams{
		Key: bakery.MustGenerateKey(),
	})
	s.serverBakery = bakery.New(bakery.BakeryParams{
		Locator: s.discharger,
		Key:     bakery.MustGenerateKey(),
	})
}

func (s *agentSuite) TearDownTest(c *gc.C) {
	s.discharger.Close()
	s.LoggingSuite.TearDownTest(c)
}

var agentLoginOp = bakery.Op{"agent", "login"}

func (s *agentSuite) TestSetUpAuth(c *gc.C) {
	dischargerBakery := bakery.New(bakery.BakeryParams{
		Key: s.discharger.Key,
	})
	s.discharger.AddHTTPHandlers(AgentHandlers(AgentHandler{
		AgentMacaroon: func(p httprequest.Params, username string, pubKey *bakery.PublicKey) (*bakery.Macaroon, error) {
			if username != "test-user" || *pubKey != s.agentBakery.Oven.Key().Public {
				return nil, errgo.Newf("mismatched user/pubkey; want %s got %s", s.agentBakery.Oven.Key().Public, *pubKey)
			}
			version := httpbakery.RequestVersion(p.Request)
			return dischargerBakery.Oven.NewMacaroon(
				context.Background(),
				bakery.LatestVersion,
				[]checkers.Caveat{
					bakery.LocalThirdPartyCaveat(pubKey, version),
				},
				agentLoginOp,
			)
		},
	}))
	s.discharger.Checker = httpbakery.ThirdPartyCaveatCheckerFunc(func(ctx context.Context, req *http.Request, info *bakery.ThirdPartyCaveatInfo, token *httpbakery.DischargeToken) ([]checkers.Caveat, error) {
		if token != nil {
			c.Logf("with token request: %v", req.URL)
			if token.Kind != "agent" {
				return nil, errgo.Newf("unexpected discharge token kind %q", token.Kind)
			}
			var m macaroon.Slice
			if err := m.UnmarshalBinary(token.Value); err != nil {
				return nil, errgo.Notef(err, "cannot unmarshal token")
			}
			if _, err := dischargerBakery.Checker.Auth(m).Allow(ctx, agentLoginOp); err != nil {
				return nil, errgo.Newf("received unexpected discharge token")
			}
			return nil, nil
		}
		if string(info.Condition) != "some-third-party-caveat" {
			return nil, errgo.Newf("unexpected caveat condition")
		}
		err := httpbakery.NewInteractionRequiredError(nil, req)
		agent.SetInteraction(err, "/agent-macaroon")
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
	someOp := bakery.Op{
		Entity: "something",
		Action: "doit",
	}
	c.Assert(err, gc.IsNil)
	m, err := s.serverBakery.Oven.NewMacaroon(
		context.Background(),
		bakery.LatestVersion,
		[]checkers.Caveat{{
			Location:  s.discharger.Location(),
			Condition: "some-third-party-caveat",
		}},
		someOp,
	)
	c.Assert(err, gc.Equals, nil)
	ms, err := client.DischargeAll(context.Background(), m)
	c.Assert(err, gc.Equals, nil)
	_, err = s.serverBakery.Checker.Auth(ms).Allow(context.Background(), someOp)
	c.Assert(err, gc.Equals, nil)
}

func (s *agentSuite) TestAuthInfoFromEnvironment(c *gc.C) {
	defer os.Setenv("BAKERY_AGENT_FILE", "")

	f, err := ioutil.TempFile("", "")
	c.Assert(err, gc.Equals, nil)
	defer os.Remove(f.Name())
	defer f.Close()

	authInfo := &agent.AuthInfo{
		Key: bakery.MustGenerateKey(),
		Agents: []agent.Agent{{
			URL:      "https://0.1.2.3/x",
			Username: "bob",
		}, {
			URL:      "https://0.2.3.4",
			Username: "charlie",
		}},
	}
	data, err := json.Marshal(authInfo)
	_, err = f.Write(data)
	c.Assert(err, gc.Equals, nil)
	f.Close()

	os.Setenv("BAKERY_AGENT_FILE", f.Name())

	authInfo1, err := agent.AuthInfoFromEnvironment()
	c.Assert(err, gc.Equals, nil)
	c.Assert(authInfo1, jc.DeepEquals, authInfo)
}

func (s *agentSuite) TestAuthInfoFromEnvironmentNotSet(c *gc.C) {
	os.Setenv("BAKERY_AGENT_FILE", "")
	authInfo, err := agent.AuthInfoFromEnvironment()
	c.Assert(errgo.Cause(err), gc.Equals, agent.ErrNoAuthInfo)
	c.Assert(authInfo, gc.IsNil)
}

func AgentHandlers(h AgentHandler) []httprequest.Handler {
	return reqServer.Handlers(func(p httprequest.Params) (agentHandlers, context.Context, error) {
		return agentHandlers{h}, p.Context, nil
	})
}

// AgentHandler holds the functions that may be called by the
// agent-interaction server.
type AgentHandler struct {
	AgentMacaroon func(p httprequest.Params, username string, pubKey *bakery.PublicKey) (*bakery.Macaroon, error)
}

// agentHandlers is used to define the handler methods.
type agentHandlers struct {
	h AgentHandler
}

// agentMacaroonRequest represents a request for the
// agent macaroon - it matches agent.agentMacaroonRequest.
type agentMacaroonRequest struct {
	httprequest.Route `httprequest:"GET /agent-macaroon"`
	Username          string            `httprequest:"username,form"`
	PublicKey         *bakery.PublicKey `httprequest:"public-key,form"`
}

type agentMacaroonResponse struct {
	Macaroon *bakery.Macaroon `json:"macaroon"`
}

func (h agentHandlers) AgentMacaroon(p httprequest.Params, r *agentMacaroonRequest) (*agentMacaroonResponse, error) {
	m, err := h.h.AgentMacaroon(p, r.Username, r.PublicKey)
	if err != nil {
		return nil, errgo.Mask(err, errgo.Any)
	}
	return &agentMacaroonResponse{
		Macaroon: m,
	}, nil
}
