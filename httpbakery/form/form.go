// Package form enables interactive login without using a web browser.
package form

import (
	"net/http"
	"net/url"

	"github.com/juju/httprequest"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/environschema.v1"

	"gopkg.in/macaroon-bakery.v1/httpbakery"
)

/*
PROTOCOL

A form login works as follows:

	   Client                            Login Service
	      |                                    |
	      | GET visitURL with                  |
	      | "Accept: application/json"         |
	      |----------------------------------->|
	      |                                    |
	      |   Login Methods (including "form") |
	      |<-----------------------------------|
	      |                                    |
	      | GET "form" URL                     |
	      |----------------------------------->|
	      |                                    |
	      |                  Schema definition |
	      |<-----------------------------------|
	      |                                    |
	+-------------+                            |
	|   Client    |                            |
	| Interaction |                            |
	+-------------+                            |
	      |                                    |
	      | POST data to "form" URL            |
	      |----------------------------------->|
	      |                                    |
	      |                Form login response |
	      |<-----------------------------------|
	      |                                    |

The schema is provided as a environschema.Fileds object. It is the
client's responsibility to interpret the schema and present it to the
user.
*/

// Filler represents an object that can fill out a form. The given schema
// represents the form template. The returned value should be compatible
// with this.
type Filler interface {
	Fill(schema environschema.Fields) (map[string]interface{}, error)
}

// SetUpAuth configures form authentication on c. The VisitWebPage field
// in c will be set to a function that will attempt form-based
// authentication using f to perform the interaction with the user and
// fall back to using the current value of VisitWebPage if form-based
// authentication is not supported.
func SetUpAuth(c *httpbakery.Client, f Filler) {
	c.VisitWebPage = VisitWebPage(
		&httprequest.Client{
			Doer: c,
		},
		f,
		c.VisitWebPage,
	)
}

// VisitWebPage creates a function suitable for use with
// httpbakery.Client.VisitWebPage. The new function downloads the schema
// from the specified server and calls f.Fill. The map returned by f.Fill
// should match the schema specified, but this is not verified before
// sending to the server. Any errors returned by f.Fill or fallback will
// not have their cause masked.
//
// If the new function detects that form login is not supported by the
// server and fallback is not nil then fallback will be called to perform
// the visit.
func VisitWebPage(c *httprequest.Client, f Filler, fallback func(u *url.URL) error) func(u *url.URL) error {
	v := webPageVisitor{
		client:   c,
		filler:   f,
		fallback: fallback,
	}
	return v.visitWebPage
}

// webPageVisitor contains the state required by visitWebPage.
type webPageVisitor struct {
	client   *httprequest.Client
	filler   Filler
	fallback func(u *url.URL) error
}

// loginMethods contains the response expected from the login URL. It
// only checks for the "form" method as that is the only one that can be
// handled.
type loginMethods struct {
	Form string `json:"form"`
}

// SchemaRequest is a request for a form schema.
type SchemaRequest struct {
	httprequest.Route `httprequest:"GET"`
}

// SchemaResponse contains the message expected in response to the schema
// request.
type SchemaResponse struct {
	Schema environschema.Fields `json:"schema"`
}

// LoginRequest is a request to perform a login using the provided form.
type LoginRequest struct {
	httprequest.Route `httprequest:"POST"`
	Body              LoginBody `httprequest:",body"`
}

// LoginBody holds the body of a form login request.
type LoginBody struct {
	Form map[string]interface{} `json:"form"`
}

// visitWebPage performs the actual visit request. It attempts to
// determine that form login is supported and then download the form
// schema. It calls v.handler.Handle using the downloaded schema and then
// submits the returned form. Any error produced by v.handler.Handle will
// not have it's cause masked.
func (v webPageVisitor) visitWebPage(u *url.URL) error {
	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		return errgo.Notef(err, "cannot create request")
	}
	req.Header.Set("Accept", "application/json")
	var lm loginMethods
	if err := v.client.Do(req, nil, &lm); err != nil {
		if v.fallback != nil {
			if err := v.fallback(u); err != nil {
				return errgo.Mask(err, errgo.Any)
			}
			return nil
		}
		return errgo.Notef(err, "cannot get login methods")
	}
	if lm.Form == "" {
		if v.fallback != nil {
			if err := v.fallback(u); err != nil {
				return errgo.Mask(err, errgo.Any)
			}
			return nil
		}
		return errgo.Newf("form login not supported")
	}
	var s SchemaResponse
	if err := v.client.CallURL(lm.Form, &SchemaRequest{}, &s); err != nil {
		return errgo.Notef(err, "cannot get schema")
	}
	if len(s.Schema) == 0 {
		return errgo.Newf("invalid schema: no fields found")
	}
	form, err := v.filler.Fill(s.Schema)
	if err != nil {
		return errgo.NoteMask(err, "cannot handle form", errgo.Any)
	}
	lr := LoginRequest{
		Body: LoginBody{
			Form: form,
		},
	}
	if err := v.client.CallURL(lm.Form, &lr, nil); err != nil {
		return errgo.Notef(err, "cannot submit form")
	}
	return nil
}
