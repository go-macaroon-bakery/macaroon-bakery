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
// in c will be set to a function that will attempt form based
// authentication using h to perform the interaction with the user.
//
// The VisitWebPage function downloads the schema from the specified
// server and calls h.Handle. The map returned by h.Handle should match
// the schema specified, but VisitWebPage will not verify this before
// sending to the server. Any errors returned by h.Handle will not have
// their cause masked.
func SetUpAuth(c *httpbakery.Client, f Filler) {
	v := webPageVisitor{
		client: &httprequest.Client{
			Doer: c,
		},
		filler: f,
	}
	c.VisitWebPage = v.visitWebPage
}

// webPageVisitor contains the state required by visitWebPage.
type webPageVisitor struct {
	client *httprequest.Client
	filler Filler
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

// LoginRequest is a request to perform a login using the provided details.
type LoginRequest struct {
	httprequest.Route `httprequest:"POST"`
	Login             LoginWrapper `httprequest:",body"`
}

// Login holds the body of a login request.
type LoginWrapper struct {
	Login map[string]interface{} `json:"login"`
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
		return errgo.Notef(err, "cannot get login methods")
	}
	if lm.Form == "" {
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
		Login: LoginWrapper{
			Login: form,
		},
	}
	if err := v.client.CallURL(lm.Form, &lr, nil); err != nil {
		return errgo.Notef(err, "cannot submit form")
	}
	return nil
}
