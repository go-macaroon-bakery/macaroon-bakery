// Package form enables interactive login without using a web browser.
package form

import (
	"net/url"

	"golang.org/x/net/context"
	"golang.org/x/net/publicsuffix"
	"gopkg.in/errgo.v1"
	"gopkg.in/httprequest.v1"
	"gopkg.in/juju/environschema.v1"
	"gopkg.in/juju/environschema.v1/form"

	"gopkg.in/macaroon-bakery.v2/httpbakery"
)

/*
PROTOCOL

A form login works as follows:

       Client                            Login Service
          |                                    |
          | Discharge request                  |
          |----------------------------------->|
          |                                    |
          |    Interaction-required error with |
          |         "form" entry with formURL. |
          |<-----------------------------------|
          |                                    |
          | GET "form" URL                     |
          |----------------------------------->|
          |                                    |
          | Schema definition                  |
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
          |               with discharge token |
          |<-----------------------------------|
          |                                    |
          | Discharge request with             |
          | discharge token.                   |
          |----------------------------------->|
          |                                    |
          | Discharge macaroon                 |
          |<-----------------------------------|


The schema is provided as a environschema.Fields object. It is the
client's responsibility to interpret the schema and present it to the
user.
*/

const (
	// InteractionMethod is the methodURLs key
	// used for a URL that can be used for form-based
	// interaction.
	InteractionMethod = "form"
)

// SchemaResponse contains the message expected in response to the schema
// request.
type SchemaResponse struct {
	Schema environschema.Fields `json:"schema"`
}

// InteractionInfo holds the information expected in
// the form interaction entry in an interaction-required
// error.
type InteractionInfo struct {
	URL string `json:"url"`
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

type LoginResponse struct {
	Token *httpbakery.DischargeToken `json:"token"`
}

// Interactor implements httpbakery.Interactor
// by providing form-based interaction.
type Interactor struct {
	// Filler holds the form filler that will be used when
	// form-based interaction is required.
	Filler form.Filler
}

// Kind implements httpbakery.Interactor.Kind.
func (i Interactor) Kind() string {
	return InteractionMethod
}

// Interact implements httpbakery.Interactor.Interact.
func (i Interactor) Interact(ctx context.Context, client *httpbakery.Client, location string, interactionRequiredErr *httpbakery.Error) (*httpbakery.DischargeToken, error) {
	var p InteractionInfo
	if err := interactionRequiredErr.InteractionMethod(InteractionMethod, &p); err != nil {
		return nil, errgo.Mask(err)
	}
	if p.URL == "" {
		return nil, errgo.Newf("no URL found in form information")
	}
	schemaURL, err := relativeURL(location, p.URL)
	if err != nil {
		return nil, errgo.Notef(err, "invalid url %q", p.URL)
	}
	httpReqClient := &httprequest.Client{
		Doer: client,
	}
	var s SchemaResponse
	if err := httpReqClient.Get(ctx, schemaURL.String(), &s); err != nil {
		return nil, errgo.Notef(err, "cannot get schema")
	}
	if len(s.Schema) == 0 {
		return nil, errgo.Newf("invalid schema: no fields found")
	}
	host, err := publicsuffix.EffectiveTLDPlusOne(schemaURL.Host)
	if err != nil {
		host = schemaURL.Host
	}
	formValues, err := i.Filler.Fill(form.Form{
		Title:  "Log in to " + host,
		Fields: s.Schema,
	})
	if err != nil {
		return nil, errgo.NoteMask(err, "cannot handle form", errgo.Any)
	}
	lr := LoginRequest{
		Body: LoginBody{
			Form: formValues,
		},
	}
	var lresp LoginResponse
	if err := httpReqClient.CallURL(ctx, schemaURL.String(), &lr, &lresp); err != nil {
		return nil, errgo.Notef(err, "cannot submit form")
	}
	if lresp.Token == nil {
		return nil, errgo.Newf("no token found in form response")
	}
	return lresp.Token, nil
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
