// Package agent enables non-interactive (agent) login using macaroons.
// To enable agent authorization with a given httpbakery.Client c against
// a given third party discharge server URL u:
//
// 	SetUpAuth(c, u, agentUsername)
//
package agent

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/url"
	"os"
	"strings"

	"golang.org/x/net/context"
	"gopkg.in/errgo.v1"
	"gopkg.in/httprequest.v1"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/httpbakery"
)

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

var ErrNoAuthInfo = errgo.New("no bakery agent info found in environment")

// AuthInfoFromEnvironment returns an AuthInfo derived
// from environment variables.
//
// It recognizes the following variable:
// BAKERY_AGENT_FILE - path to a file containing agent authentication
//    info in JSON format (as marshaled by the AuthInfo type).
//
// If BAKERY_AGENT_FILE is not set, ErrNoAuthInfo will be returned.
func AuthInfoFromEnvironment() (*AuthInfo, error) {
	agentFile := os.Getenv("BAKERY_AGENT_FILE")
	if agentFile == "" {
		return nil, errgo.WithCausef(nil, ErrNoAuthInfo, "")
	}
	var ai AuthInfo
	data, err := ioutil.ReadFile(agentFile)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	if err := json.Unmarshal(data, &ai); err != nil {
		return nil, errgo.Notef(err, "cannot unmarshal agent information from %q: %v", agentFile)
	}
	if ai.Key == nil {
		return nil, errgo.Newf("no private key found in %q", agentFile)
	}
	return &ai, nil
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

type agentLoginRequest struct {
	httprequest.Route `httprequest:"POST"`
	Body              LegacyAgentLoginBody `httprequest:",body"`
}

// LegacyAgentLoginBody is used to encode the JSON body
// sent when making a legacy agent protocol
// POST request to the visit URL.
type LegacyAgentLoginBody struct {
	Username  string            `json:"username"`
	PublicKey *bakery.PublicKey `json:"public_key"`
}

// LegacyAgentResponse contains the response to a
// legacy agent login attempt.
type LegacyAgentResponse struct {
	AgentLogin bool `json:"agent_login"`
}

// LegacyInteract implements httpbakery.LegactInteractor.LegacyInteract.
func (i interactor) LegacyInteract(ctx context.Context, client *httpbakery.Client, location string, visitURL *url.URL) error {
	c := &httprequest.Client{
		Doer: client,
	}
	agent, err := i.findAgent(location)
	if err != nil {
		return errgo.Mask(err)
	}
	var resp LegacyAgentResponse
	err = c.CallURL(ctx, visitURL.String(), &agentLoginRequest{
		Body: LegacyAgentLoginBody{
			Username:  agent.Username,
			PublicKey: &client.Key.Public,
		},
	}, &resp)
	if err != nil {
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
