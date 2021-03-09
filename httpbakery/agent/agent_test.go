package agent_test

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"os"
	"testing"

	qt "github.com/frankban/quicktest"
	"gopkg.in/errgo.v1"
	"gopkg.in/httprequest.v1"
	"gopkg.in/macaroon.v2"

	"github.com/go-macaroon-bakery/macaroon-bakery/v3/bakery"
	"github.com/go-macaroon-bakery/macaroon-bakery/v3/bakery/checkers"
	"github.com/go-macaroon-bakery/macaroon-bakery/v3/bakerytest"
	"github.com/go-macaroon-bakery/macaroon-bakery/v3/httpbakery"
	"github.com/go-macaroon-bakery/macaroon-bakery/v3/httpbakery/agent"
)

var agentLoginOp = bakery.Op{"agent", "login"}

func TestSetUpAuth(t *testing.T) {
	c := qt.New(t)
	defer c.Done()
	f := newAgentFixture(c)
	dischargerBakery := bakery.New(bakery.BakeryParams{
		Key: f.discharger.Key,
	})
	f.discharger.AddHTTPHandlers(AgentHandlers(AgentHandler{
		AgentMacaroon: func(p httprequest.Params, username string, pubKey *bakery.PublicKey) (*bakery.Macaroon, error) {
			if username != "test-user" || *pubKey != f.agentBakery.Oven.Key().Public {
				return nil, errgo.Newf("mismatched user/pubkey; want %s got %s", f.agentBakery.Oven.Key().Public, *pubKey)
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
	f.discharger.Checker = httpbakery.ThirdPartyCaveatCheckerFunc(func(ctx context.Context, req *http.Request, info *bakery.ThirdPartyCaveatInfo, token *httpbakery.DischargeToken) ([]checkers.Caveat, error) {
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
		Key: f.agentBakery.Oven.Key(),
		Agents: []agent.Agent{{
			URL:      f.discharger.Location(),
			Username: "test-user",
		}},
	})
	someOp := bakery.Op{
		Entity: "something",
		Action: "doit",
	}
	c.Assert(err, qt.IsNil)
	m, err := f.serverBakery.Oven.NewMacaroon(
		context.Background(),
		bakery.LatestVersion,
		[]checkers.Caveat{{
			Location:  f.discharger.Location(),
			Condition: "some-third-party-caveat",
		}},
		someOp,
	)
	c.Assert(err, qt.Equals, nil)
	ms, err := client.DischargeAll(context.Background(), m)
	c.Assert(err, qt.Equals, nil)
	_, err = f.serverBakery.Checker.Auth(ms).Allow(context.Background(), someOp)
	c.Assert(err, qt.Equals, nil)
}

func TestAuthInfoFromEnvironment(t *testing.T) {
	c := qt.New(t)
	defer c.Done()
	defer os.Setenv("BAKERY_AGENT_FILE", "")

	f, err := ioutil.TempFile("", "")
	c.Assert(err, qt.Equals, nil)
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
	c.Assert(err, qt.Equals, nil)
	f.Close()

	os.Setenv("BAKERY_AGENT_FILE", f.Name())

	authInfo1, err := agent.AuthInfoFromEnvironment()
	c.Assert(err, qt.Equals, nil)
	c.Assert(authInfo1, qt.DeepEquals, authInfo)
}

func TestAuthInfoFromEnvironmentNotSet(t *testing.T) {
	c := qt.New(t)
	defer c.Done()
	os.Setenv("BAKERY_AGENT_FILE", "")
	authInfo, err := agent.AuthInfoFromEnvironment()
	c.Assert(errgo.Cause(err), qt.Equals, agent.ErrNoAuthInfo)
	c.Assert(authInfo, qt.IsNil)
}

type agentFixture struct {
	agentBakery  *bakery.Bakery
	serverBakery *bakery.Bakery
	discharger   *bakerytest.Discharger
}

func newAgentFixture(c *qt.C) *agentFixture {
	var f agentFixture
	f.discharger = bakerytest.NewDischarger(nil)
	c.Defer(f.discharger.Close)
	f.agentBakery = bakery.New(bakery.BakeryParams{
		Key: bakery.MustGenerateKey(),
	})
	f.serverBakery = bakery.New(bakery.BakeryParams{
		Locator: f.discharger,
		Key:     bakery.MustGenerateKey(),
	})
	return &f
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
