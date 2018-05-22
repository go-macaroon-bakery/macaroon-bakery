package agent

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/url"
	"os"
	"strings"

	httprequest "github.com/juju/httprequest"
	errgo "gopkg.in/errgo.v1"
	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery"
)

// This file holds agent functionality back-ported from bakery.v2.
// The AuthInfo file format and environment variable conventions
// are the same as in that.

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

type legacyAgentLoginRequest struct {
	httprequest.Route `httprequest:"POST"`
	Body              agentLogin `httprequest:",body"`
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

// NewVisitor returns a new httpbakery.Visitor implementation that tries to
// use agent login for any sites specified in the given AuthInfo value.
func NewVisitor(authInfo *AuthInfo) Visitor {
	return Visitor{
		authInfo: authInfo,
	}
}

// Visitor is an implementation of httpbakery.Visitor that
// uses agent authentication.
type Visitor struct {
	authInfo *AuthInfo
}

// VisitWebPage implements httpbakery.Visitor.
func (v Visitor) VisitWebPage(client *httpbakery.Client, methodURLs map[string]*url.URL) error {
	visitURL := methodURLs["agent"]
	if visitURL == nil {
		return errgo.WithCausef(nil, httpbakery.ErrMethodNotSupported, "")
	}
	agent, err := v.findAgent(visitURL)
	if err != nil {
		return errgo.Mask(err)
	}
	c := &httprequest.Client{
		Doer: client,
	}
	var resp agentResponse
	err = c.CallURL(visitURL.String(), &legacyAgentLoginRequest{
		Body: agentLogin{
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

// findAgent finds an appropriate agent entry for the given location.
func (v Visitor) findAgent(visitURL *url.URL) (*Agent, error) {
	var best string
	var bestAgent *Agent
	for _, a := range v.authInfo.Agents {
		if matchURL(visitURL, a.URL) && len(a.URL) > len(best) {
			best = a.URL
			bestAgent = &a
		}
	}
	if bestAgent == nil {
		return nil, errgo.WithCausef(nil, httpbakery.ErrMethodNotSupported, "method not supported: cannot find username for discharge location %q", visitURL)
	}
	return bestAgent, nil
}

func matchURL(visitURL *url.URL, agentURL string) bool {
	u, err := url.Parse(agentURL)
	if err != nil {
		return false
	}
	if u.Host != visitURL.Host {
		return false
	}
	if !strings.HasPrefix(visitURL.Path, u.Path) {
		return false
	}
	if len(visitURL.Path) == len(u.Path) {
		return true
	}
	if visitURL.Path[len(u.Path)] == '/' {
		return true
	}
	return false
}
