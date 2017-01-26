package agent

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
)

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
