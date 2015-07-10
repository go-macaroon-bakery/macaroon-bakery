// Package agent enables non-interactive (agent) login using macaroons.
// To enable agent authorization with a given httpbakery.Client c against
// a given third party discharge server URL u:
//
// 	SetUpAuth(c, u, agentUsername)
//
package agent

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"mime"
	"net/http"
	"net/url"

	"github.com/juju/loggo"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v1/bakery"
	"gopkg.in/macaroon-bakery.v1/httpbakery"
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
encoded JSON object of the form:

	{
		"username": <username>,
		"public_key": <public key>
	}

A local third-party caveat is a third party caveat with the location
set to "local" and the caveat encrypted with the public key declared
in the agent cookie. The httpbakery.Client automatically discharges
the local third-party caveat.

On success the response is a JSON object of the form:

	{
		"agent_login": true
	}

If an error occurs then the response should be a JSON object that
unmarshalls to an httpbakery.Error.
*/

// agentLogin defines the structure of an agent login cookie. It is also
// returned in a successful agent login attempt to help indicate that an
// agent login has occurred.
type agentLogin struct {
	Username  string            `json:"username"`
	PublicKey *bakery.PublicKey `json:"public_key"`
}

// agentResponse defines the structure of an agent login response.
type agentResponse struct {
	AgentLogin bool `json:"agent_login"`
}

// SetUpAuth configures agent authentication on c. A cookie is created in
// c's cookie jar containing credentials derived from the username and
// c.Key. c.VisitWebPage is set to VisitWebPage(c). The return is
// non-nil only if c.Key is nil.
func SetUpAuth(c *httpbakery.Client, u *url.URL, username string) error {
	if c.Key == nil {
		return errgo.New("cannot set-up authentication: client key not configured")
	}
	SetCookie(c.Jar, u, username, &c.Key.Public)
	c.VisitWebPage = VisitWebPage(c)
	return nil
}

// SetCookie creates a cookie in jar which is suitable for performing agent
// logins to u.
func SetCookie(jar http.CookieJar, u *url.URL, username string, pk *bakery.PublicKey) {
	al := agentLogin{
		Username:  username,
		PublicKey: pk,
	}
	b, err := json.Marshal(al)
	if err != nil {
		// This shouldn't happen as the agentLogin type has to be marshalable.
		panic(errgo.Notef(err, "cannot marshal cookie"))
	}
	v := base64.StdEncoding.EncodeToString(b)
	jar.SetCookies(u, []*http.Cookie{{
		Name:  "agent-login",
		Value: v,
	}})
}

// VisitWebPage creates a function that can be used with
// httpbakery.Client.VisitWebPage. The function uses c to access the
// visit URL. If no agent-login cookie has been configured for u an error
// with the cause of http.ErrNoCookie will be returned. If the login
// fails the returned error will be of type *httpbakery.Error. If the
// response from the visitURL cannot be interpreted the error will be of
// type *UnexpectedResponseError.
func VisitWebPage(c *httpbakery.Client) func(u *url.URL) error {
	return func(u *url.URL) error {
		err := http.ErrNoCookie
		for _, c := range c.Jar.Cookies(u) {
			if c.Name == "agent-login" {
				err = nil
				break
			}
		}
		if err != nil {
			return errgo.WithCausef(err, http.ErrNoCookie, "cannot perform agent login")
		}
		req, err := http.NewRequest("GET", u.String(), nil)
		if err != nil {
			return errgo.Notef(err, "cannot create request")
		}
		resp, err := c.Do(req)
		if err != nil {
			return errgo.Notef(err, "cannot perform request")
		}
		defer resp.Body.Close()
		b, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			logger.Errorf("cannot read response body: %s", err)
			b = []byte{}
		}
		mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
		if err != nil {
			logger.Warningf("cannot parse response content type: %s", err)
			mt = ""
		}
		if mt != "application/json" {
			uerr := (*UnexpectedResponseError)(resp)
			uerr.Body = ioutil.NopCloser(bytes.NewReader(b))
			return uerr
		}
		if resp.StatusCode != http.StatusOK {
			var herr httpbakery.Error
			err := json.Unmarshal(b, &herr)
			if err == nil && herr.Message != "" {
				return &herr
			}
			if err != nil {
				logger.Warningf("cannot unmarshal error response: %s", err)
			}
			uerr := (*UnexpectedResponseError)(resp)
			uerr.Body = ioutil.NopCloser(bytes.NewReader(b))
			return uerr
		}
		var ar agentResponse
		err = json.Unmarshal(b, &ar)
		if err == nil && ar.AgentLogin {
			return nil
		}
		if err != nil {
			logger.Warningf("cannot unmarshal response: %s", err)
		}
		uerr := (*UnexpectedResponseError)(resp)
		uerr.Body = ioutil.NopCloser(bytes.NewReader(b))
		return uerr
	}
}

// UnexpectedResponseError is the error returned when a response is
// received that cannot be interpreted.
type UnexpectedResponseError http.Response

func (u *UnexpectedResponseError) Error() string {
	return fmt.Sprintf(
		"unexpected response to non-interactive web page visit %s (content type %s)",
		u.Request.URL.String(),
		u.Header.Get("Content-Type"))
}
