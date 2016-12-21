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
	"sort"
	"strings"

	"github.com/juju/httprequest"
	"github.com/juju/loggo"
	"golang.org/x/net/context"
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

// SetUpAuth is a convenience function that makes a new Visitor
// and adds an agent for the given URL using the given username
// and the public key of the client.Key.
func SetUpAuth(client *httpbakery.Client, siteURL string, agentUsername string) error {
	if client.Key == nil {
		return errgo.Newf("no key found in client")
	}
	var v Visitor
	if err := v.AddAgent(Agent{
		URL:      siteURL,
		Username: agentUsername,
		Key:      client.Key,
	}); err != nil {
		return errgo.Mask(err)
	}
	client.WebPageVisitor = &v
	return nil
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

// agent is the internal version of the agent type which also
// includes the parsed URL.
type agent struct {
	url *url.URL
	Agent
}

// agent represents an agent that can be used for agent authentication.
type Agent struct {
	// URL holds the URL associated with the agent.
	URL string
	// Username holds the username to use for the agent.
	Username string
	// Key holds the agent's private key pair.
	Key *bakery.KeyPair
}

// Visitor is a httpbakery.Visitor that performs interaction using the
// agent login protocol.
type Visitor struct {
	agents map[string][]agent
}

// Agents returns all the agents registered with the visitor
// ordered by URL.
func (v *Visitor) Agents() []Agent {
	var agents []Agent
	for _, as := range v.agents {
		for _, a := range as {
			agents = append(agents, a.Agent)
		}
	}
	sort.Sort(agentsByURL(agents))
	return agents
}

// AddAgent adds an agent to the visitor. The agent information will be
// used when sending discharge requests to all URLs under the given URL.
// If more than one agent matches a target URL then the agent with the
// most specific matching URL will be used. Longer paths are counted as
// more specific than shorter paths.
//
// Unlike HTTP cookies, a trailing slash is not significant, so for
// example, if an agent is registered with the URL
// http://example.com/foo, its information will be sent to
// http://example.com/foo/bar but not http://kremvax.com/other.
//
// If an agent is added with the same URL as an existing agent (ignoring
// any trailing slash), the existing agent will be replaced.
//
// AddAgent returns an error if the agent's URL cannot be parsed
// or if the agent does not have a key.
func (v *Visitor) AddAgent(a Agent) error {
	if a.Key == nil {
		return errgo.Newf("no key for agent")
	}
	u, err := url.Parse(a.URL)
	if err != nil {
		return errgo.Notef(err, "bad agent URL")
	}
	// The path should behave the same whether it has a trailing
	// slash or not.
	u.Path = strings.TrimSuffix(u.Path, "/")
	if v.agents == nil {
		v.agents = make(map[string][]agent)
	}
	v.agents[u.Host] = insertAgent(v.agents[u.Host], agent{
		Agent: a,
		url:   u,
	})
	return nil
}

// pathMatch checks whether reqPath matches the given registered path.
func pathMatch(reqPath, path string) bool {
	if path == reqPath {
		return true
	}
	if !strings.HasPrefix(reqPath, path) {
		return false
	}
	// /foo/bar matches /foo/bar/baz.
	// /foo/bar/ also matches /foo/bar/baz.
	// /foo/bar does not match /foo/barfly.
	// So trim off the suffix and check that the equivalent place in
	// reqPath holds a slash. Note that we know that reqPath must be
	// longer than path because path is a prefix of reqPath but not
	// equal to it.
	return reqPath[len(path)] == '/'
}

func (v *Visitor) findAgent(u *url.URL) (agent, bool) {
	for _, a := range v.agents[u.Host] {
		if pathMatch(u.Path, a.url.Path) {
			return a, true
		}
	}
	return agent{}, false
}

// VisitWebPage implements httpbakery.Visitor.VisitWebPage
func (v *Visitor) VisitWebPage(ctx context.Context, client *httpbakery.Client, m map[string]*url.URL) error {
	url := m[httpbakery.UserInteractionMethod]
	a, ok := v.findAgent(url)
	if !ok {
		return errgo.New("no suitable agent found")
	}
	client1 := *client
	client1.Key = a.Key
	c := &httprequest.Client{
		Doer: &client1,
	}
	req, err := http.NewRequest("GET", url.String(), nil)
	if err != nil {
		return errgo.Mask(err)
	}
	setCookie(req, a.Username, &a.Key.Public)
	var resp agentResponse
	if err := c.Do(ctx, req, &resp); err != nil {
		return errgo.Mask(err)
	}
	if !resp.AgentLogin {
		return errors.New("agent login failed")
	}
	return nil
}

func insertAgent(agents []agent, a agent) []agent {
	for i, a1 := range agents {
		if a1.url.Path == a.url.Path {
			agents[i] = a
			return agents
		}
	}
	agents = append(agents, a)
	sort.Sort(byReverseURLLength(agents))
	return agents
}

type byReverseURLLength []agent

func (as byReverseURLLength) Less(i, j int) bool {
	p0, p1 := as[i].url.Path, as[j].url.Path
	if len(p0) != len(p1) {
		return len(p0) > len(p1)
	}
	return p0 < p1
}

func (as byReverseURLLength) Swap(i, j int) {
	as[i], as[j] = as[j], as[i]
}

func (as byReverseURLLength) Len() int {
	return len(as)
}

type agentsByURL []Agent

func (as agentsByURL) Less(i, j int) bool {
	return as[i].URL < as[j].URL
}

func (as agentsByURL) Swap(i, j int) {
	as[i], as[j] = as[j], as[i]
}

func (as agentsByURL) Len() int {
	return len(as)
}
