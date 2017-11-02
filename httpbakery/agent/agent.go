// Package agent enables non-interactive (agent) login using macaroons.
// To enable agent authorization with a given httpbakery.Client c against
// a given third party discharge server URL u:
//
// 	SetUpAuth(c, u, agentUsername)
//
package agent

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"gopkg.in/juju/httprequest.v2"
	"github.com/juju/loggo"
	"golang.org/x/net/context"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/httpbakery"
)

var logger = loggo.GetLogger("httpbakery.agent")

// AuthInfo holds the agent information required
// to set up agent authentication information.
// It holds the agent's private key and information
// about the username associated with each
// known agent-authentication server.
type AuthInfo struct {
	Key    *bakery.KeyPair `json:"key,omitempty" yaml:"key,omitempty"`
	Agents []Agent         `json:"agents" yaml:"agents"`
}

// Agent represents an agent that can be used for agent authentication.
type Agent struct {
	// URL holds the URL associated with the agent.
	URL string `json:"url" yaml:"url"`
	// Username holds the username to use for the agent.
	Username string `json:"username" yaml:"username"`
}

// SetUpAuth sets up agent authentication on the given client.
// If this is called several times on the same client, earlier
// calls will take precedence over later calls when there's
// a URL and username match for both.
func SetUpAuth(client *httpbakery.Client, authInfo *AuthInfo) error {
	if authInfo.Key == nil {
		return errgo.Newf("no key in auth info")
	}
	if client.Key != nil {
		if *client.Key != *authInfo.Key {
			return errgo.Newf("client already has a different key set up")
		}
	} else {
		client.Key = authInfo.Key
	}
	client.AddInteractor(interactor{authInfo})
	return nil
}

// InteractionInfo holds the information expected in
// the agent interaction entry in an interaction-required
// error.
type InteractionInfo struct {
	// LoginURL holds the URL from which to acquire
	// a macaroon that can be used to complete the agent
	// login. To acquire the macaroon, make a POST
	// request to the URL with user and public-key
	// parameters.
	LoginURL string `json:"login-url"`
}

// SetInteraction sets agent interaction information on the
// given error, which should be an interaction-required
// error to be returned from a discharge request.
//
// The given URL (which may be relative to the discharger
// location) will be the subject of a GET request by the
// client to fetch the agent macaroon that, when discharged,
// can act as the discharge token.
func SetInteraction(e *httpbakery.Error, loginURL string) {
	e.SetInteraction("agent", &InteractionInfo{
		LoginURL: loginURL,
	})
}

// interactor is a httpbakery.Interactor that performs interaction using the
// agent login protocol.
type interactor struct {
	authInfo *AuthInfo
}

func (i interactor) Kind() string {
	return "agent"
}

// agentMacaroonRequest represents a request to get the
// agent macaroon that, when discharged, becomes
// the discharge token to complete the discharge.
type agentMacaroonRequest struct {
	httprequest.Route `httprequest:"GET"`
	Username          string            `httprequest:"username,form"`
	PublicKey         *bakery.PublicKey `httprequest:"public-key,form"`
}

type agentMacaroonResponse struct {
	Macaroon *bakery.Macaroon `json:"macaroon"`
}

func (i interactor) Interact(ctx context.Context, client *httpbakery.Client, location string, interactionRequiredErr *httpbakery.Error) (*httpbakery.DischargeToken, error) {
	var p InteractionInfo
	err := interactionRequiredErr.InteractionMethod("agent", &p)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	if p.LoginURL == "" {
		return nil, errgo.Newf("no login-url field found in agent interaction method")
	}
	agent, err := i.findAgent(location)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	loginURL, err := relativeURL(location, p.LoginURL)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	var resp agentMacaroonResponse
	err = (&httprequest.Client{
		Doer: client,
	}).CallURL(ctx, loginURL.String(), &agentMacaroonRequest{
		Username:  agent.Username,
		PublicKey: &client.Key.Public,
	}, &resp)
	if err != nil {
		return nil, errgo.Notef(err, "cannot acquire agent macaroon")
	}
	if resp.Macaroon == nil {
		return nil, errgo.Newf("no macaroon in response")
	}
	ms, err := client.DischargeAll(ctx, resp.Macaroon)
	if err != nil {
		return nil, errgo.Notef(err, "cannot discharge agent macaroon")
	}
	data, err := ms.MarshalBinary()
	if err != nil {
		return nil, errgo.Notef(err, "cannot marshal agent macaroon")
	}
	return &httpbakery.DischargeToken{
		Kind:  "agent",
		Value: data,
	}, nil
}

// findAgent finds an appropriate agent entry
// for the given location.
func (i interactor) findAgent(location string) (*Agent, error) {
	for _, a := range i.authInfo.Agents {
		// Don't worry about trailing slashes
		if strings.TrimSuffix(a.URL, "/") == strings.TrimSuffix(location, "/") {
			return &a, nil
		}
	}
	return nil, errgo.WithCausef(nil, httpbakery.ErrInteractionMethodNotFound, "cannot find username for discharge location %q", location)
}

// LegacyAgentResponse contains the response to a
// legacy agent login attempt.
type LegacyAgentResponse struct {
	AgentLogin bool `json:"agent-login"`
}

// LegacyInteract implements httpbakery.LegactInteractor.LegacyInteract by fetching a
//
func (i interactor) LegacyInteract(ctx context.Context, client *httpbakery.Client, location string, visitURL *url.URL) error {
	c := &httprequest.Client{
		Doer: client,
	}
	agent, err := i.findAgent(location)
	if err != nil {
		return errgo.Mask(err)
	}
	req, err := http.NewRequest("GET", "", nil)
	if err != nil {
		return errgo.Notef(err, "cannot make request")
	}
	req.URL = visitURL
	addCookie(req, agent.Username, &client.Key.Public)
	var resp LegacyAgentResponse
	if err := c.Do(ctx, req, &resp); err != nil {
		return errgo.Mask(err)
	}
	if !resp.AgentLogin {
		return errors.New("agent login failed")
	}
	return nil
}

// relativeURL returns newPath relative to an original URL.
func relativeURL(base, new string) (*url.URL, error) {
	if new == "" {
		return nil, errgo.Newf("empty URL")
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil, errgo.Notef(err, "cannot parse URL")
	}
	newURL, err := url.Parse(new)
	if err != nil {
		return nil, errgo.Notef(err, "cannot parse URL")
	}
	return baseURL.ResolveReference(newURL), nil
}
