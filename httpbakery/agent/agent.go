// Package agent enables non-interactive (agent) login using macaroons.
// To enable agent authorization with a given httpbakery.Client c against
// a given third party discharge server URL u:
//
// 	SetUpAuth(c, u, agentUsername)
//
package agent

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/juju/httprequest"
	"github.com/juju/loggo"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery"
)

var logger = loggo.GetLogger("httpbakery.agent")

/*
PROTOCOL

An agent login works as follows:

	    Agent                            Login Service
	      |                                    |
	      | GET visitURL with agent cookie     |
	      |----------------------------------->|
	      |                                    |
	      |    Macaroon with local third-party |
	      |                             caveat |
	      |<-----------------------------------|
	      |                                    |
	      | GET visitURL with agent cookie &   |
	      | discharged macaroon                |
	      |----------------------------------->|
	      |                                    |
	      |               Agent login response |
	      |<-----------------------------------|
	      |                                    |

The agent cookie is a cookie named "agent-login" holding a base64
encoded JSON object described by the agentLogin struct.

A local third-party caveat is a third party caveat with the location
set to "local" and the caveat encrypted with the public key declared
in the agent cookie. The httpbakery.Client automatically discharges
the local third-party caveat.

On success the response is a JSON object described by agentResponse
with the AgentLogin field set to true.

If an error occurs then the response should be a JSON object that
unmarshals to an httpbakery.Error.
*/

const cookieName = "agent-login"

// agentLogin defines the structure of an agent login cookie.
type agentLogin struct {
	Username  string            `json:"username"`
	PublicKey *bakery.PublicKey `json:"public_key"`
}

// setCookie sets an agent-login cookie with the specified parameters on
// the given request.
func setCookie(req *http.Request, username string, key *bakery.PublicKey) {
	al := agentLogin{
		Username:  username,
		PublicKey: key,
	}
	data, err := json.Marshal(al)
	if err != nil {
		// This should be impossible as the agentLogin structure
		// has to be marshalable. It is certainly a bug if it
		// isn't.
		panic(errgo.Notef(err, "cannot marshal %s cookie", cookieName))
	}
	req.AddCookie(&http.Cookie{
		Name:  cookieName,
		Value: base64.StdEncoding.EncodeToString(data),
	})
}

// agentResponse contains the response to an agent login attempt.
type agentResponse struct {
	AgentLogin bool `json:"agent_login"`
}

// ErrNoAgentLoginCookie is the error returned when the expected
// agent login cookie has not been found.
var ErrNoAgentLoginCookie = errgo.New("no agent-login cookie found")

// LoginCookie returns details of the agent login cookie
// from the given request. If no agent-login cookie is found,
// it returns an ErrNoAgentLoginCookie error.
func LoginCookie(req *http.Request) (username string, key *bakery.PublicKey, err error) {
	c, err := req.Cookie(cookieName)
	if err != nil {
		return "", nil, ErrNoAgentLoginCookie
	}
	b, err := base64.StdEncoding.DecodeString(c.Value)
	if err != nil {
		return "", nil, errgo.Notef(err, "cannot decode cookie value")
	}
	var al agentLogin
	if err := json.Unmarshal(b, &al); err != nil {
		return "", nil, errgo.Notef(err, "cannot unmarshal agent login")
	}
	if al.Username == "" {
		return "", nil, errgo.Newf("agent login has no user name")
	}
	if al.PublicKey == nil {
		return "", nil, errgo.Newf("agent login has no public key")
	}
	return al.Username, al.PublicKey, nil
}

// agent represents an agent that can be used for agent authentication.
type agent struct {
	pathParts []string
	username  string
	key       *bakery.KeyPair
}

// Visitor is a httpbakery.Visitor that performs interaction using the
// agent login protocol.
type Visitor struct {
	agents map[string][]agent
}

// AddAgent adds an agent to the visitor. The agent will have the given
// username and key and will be used for requests that match the given
// URL. If more than one agent matches a target URL then the most
// specific one will be used. If an agent is added with an identical URL
// as an existing agent then the existing agent will be replaced.
func (v *Visitor) AddAgent(u *url.URL, username string, key *bakery.KeyPair) {
	logger.Tracef("adding agent %s for %v", username, u)
	if v.agents == nil {
		v.agents = make(map[string][]agent)
	}
	v.agents[u.Host] = insertAgent(v.agents[u.Host], agent{
		pathParts: pathParts(u.Path),
		username:  username,
		key:       key,
	})
}

func insertAgent(agents []agent, ag agent) []agent {
	for i, a := range agents {
		switch pathCmp(ag.pathParts, a.pathParts) {
		case -1:
			agents = append(agents, agent{})
			copy(agents[i+1:], agents[i:])
			agents[i] = ag
			return agents
		case 0:
			agents[i] = ag
			return agents
		}
	}
	return append(agents, ag)
}

// pathParts splits a path up into an array of parts.
func pathParts(path string) []string {
	return strings.FieldsFunc(path, func(c rune) bool {
		return c == '/'
	})
}

// pathCmp compares two slices of path parts and returns -1 if p1 comes
// first logically, 1 if p1 comes second logically or 0 if they are
// equal. The logical ordering used by this function compares each part
// lexically, if these are equal up to the length of the shortest slice
// then the longer slice comes first.
func pathCmp(p1, p2 []string) int {
	for len(p1) > 0 && len(p2) > 0 {
		if cmp := strings.Compare(p1[0], p2[0]); cmp != 0 {
			return cmp
		}
		p1, p2 = p1[1:], p2[1:]
	}
	if len(p1) > 0 {
		return -1
	}
	if len(p2) > 0 {
		return 1
	}
	return 0
}

// VisitWebPage implements httpbakery.Visitor.VisitWebPage
func (v *Visitor) VisitWebPage(client *httpbakery.Client, m map[string]*url.URL) error {
	url := m[httpbakery.UserInteractionMethod]
	pathParts := pathParts(url.Path)
	for _, agent := range v.agents[url.Host] {
		if len(pathParts) < len(agent.pathParts) {
			continue
		}
		if pathCmp(agent.pathParts, pathParts[:len(agent.pathParts)]) != 0 {
			continue
		}
		logger.Debugf("attempting login to %v using agent %s", url, agent.username)
		c := &httprequest.Client{
			Doer: &httpbakery.Client{
				Client: client.Client,
				Key:    agent.key,
			},
		}
		req, err := http.NewRequest("GET", url.String(), nil)
		if err != nil {
			return errgo.Mask(err)
		}
		setCookie(req, agent.username, &agent.key.Public)
		var ar agentResponse
		if err := c.Do(req, nil, &ar); err != nil {
			return errgo.Mask(err)
		}
		if !ar.AgentLogin {
			return errors.New("agent login failed")
		}
		return nil
	}
	return errgo.New("no suitable agent found")
}
