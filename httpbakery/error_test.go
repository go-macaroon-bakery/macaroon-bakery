package httpbakery_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"

	jc "github.com/juju/testing/checkers"
	"github.com/juju/testing/httptesting"
	gc "gopkg.in/check.v1"
	"gopkg.in/httprequest.v1"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/httpbakery"
)

type ErrorSuite struct{}

var _ = gc.Suite(&ErrorSuite{})

func (s *ErrorSuite) TestWriteDischargeRequiredError(c *gc.C) {
	m, err := bakery.NewMacaroon([]byte("secret"), []byte("id"), "a location", bakery.LatestVersion, nil)
	c.Assert(err, gc.IsNil)
	tests := []struct {
		about            string
		path             string
		requestPath      string
		cookieNameSuffix string
		err              error
		expectedResponse httpbakery.Error
	}{{
		about: `write discharge required with "an error" but no path`,
		path:  "",
		err:   errors.New("an error"),
		expectedResponse: httpbakery.Error{
			Code:    httpbakery.ErrDischargeRequired,
			Message: "an error",
			Info: &httpbakery.ErrorInfo{
				Macaroon:         m,
				MacaroonPath:     "/",
				CookieNameSuffix: "auth",
			},
		},
	}, {
		about: `write discharge required with "an error" but and set a path`,
		path:  "/foo",
		err:   errors.New("an error"),
		expectedResponse: httpbakery.Error{
			Code:    httpbakery.ErrDischargeRequired,
			Message: "an error",
			Info: &httpbakery.ErrorInfo{
				Macaroon:         m,
				MacaroonPath:     "/foo",
				CookieNameSuffix: "auth",
			},
		},
	}, {
		about: `write discharge required with nil error but set a path`,
		path:  "/foo",
		expectedResponse: httpbakery.Error{
			Code:    httpbakery.ErrDischargeRequired,
			Message: httpbakery.ErrDischargeRequired.Error(),
			Info: &httpbakery.ErrorInfo{
				Macaroon:         m,
				MacaroonPath:     "/foo",
				CookieNameSuffix: "auth",
			},
		},
	}, {
		about:       `empty cookie path`,
		requestPath: "/foo/bar/baz",
		expectedResponse: httpbakery.Error{
			Code:    httpbakery.ErrDischargeRequired,
			Message: httpbakery.ErrDischargeRequired.Error(),
			Info: &httpbakery.ErrorInfo{
				Macaroon:         m,
				MacaroonPath:     "../../",
				CookieNameSuffix: "auth",
			},
		},
	}, {
		about:            `specified cookie name suffix`,
		cookieNameSuffix: "some-name",
		expectedResponse: httpbakery.Error{
			Code:    httpbakery.ErrDischargeRequired,
			Message: httpbakery.ErrDischargeRequired.Error(),
			Info: &httpbakery.ErrorInfo{
				Macaroon:         m,
				MacaroonPath:     "/",
				CookieNameSuffix: "some-name",
			},
		},
	}}

	for i, t := range tests {
		c.Logf("test %d: %s", i, t.about)
		var req *http.Request
		if t.requestPath != "" {
			req0, err := http.NewRequest("GET", t.requestPath, nil)
			c.Check(err, gc.Equals, nil)
			req = req0
		}
		response := httptest.NewRecorder()
		err := httpbakery.NewDischargeRequiredError(httpbakery.DischargeRequiredErrorParams{
			Macaroon:         m,
			CookiePath:       t.path,
			OriginalError:    t.err,
			CookieNameSuffix: t.cookieNameSuffix,
			Request:          req,
		})
		httpbakery.WriteError(testContext, response, err)
		httptesting.AssertJSONResponse(c, response, http.StatusUnauthorized, t.expectedResponse)
	}
}

func (s *ErrorSuite) TestNewInteractionRequiredError(c *gc.C) {
	// With a request with no version header, the response
	// should be 407.
	req, err := http.NewRequest("GET", "/", nil)
	c.Assert(err, gc.IsNil)

	err = httpbakery.NewInteractionRequiredError(nil, req)
	code, resp := httpbakery.ErrorToResponse(testContext, err)
	c.Assert(code, gc.Equals, http.StatusProxyAuthRequired)

	data, err := json.Marshal(resp)
	c.Assert(err, gc.IsNil)

	c.Assert(string(data), jc.JSONEquals, &httpbakery.Error{
		Code:    httpbakery.ErrInteractionRequired,
		Message: httpbakery.ErrInteractionRequired.Error(),
	})

	// With a request with a version 1 header, the response
	// should be 401.
	req.Header.Set("Bakery-Protocol-Version", "1")

	err = httpbakery.NewInteractionRequiredError(nil, req)
	code, resp = httpbakery.ErrorToResponse(testContext, err)
	c.Assert(code, gc.Equals, http.StatusUnauthorized)

	h := make(http.Header)
	resp.(httprequest.HeaderSetter).SetHeader(h)
	c.Assert(h.Get("WWW-Authenticate"), gc.Equals, "Macaroon")

	data, err = json.Marshal(resp)
	c.Assert(err, gc.IsNil)

	c.Assert(string(data), jc.JSONEquals, &httpbakery.Error{
		Code:    httpbakery.ErrInteractionRequired,
		Message: httpbakery.ErrInteractionRequired.Error(),
	})

	// With a request with a later version header, the response
	// should be also be 401.
	req.Header.Set("Bakery-Protocol-Version", "2")

	err = httpbakery.NewInteractionRequiredError(nil, req)
	code, resp = httpbakery.ErrorToResponse(testContext, err)
	c.Assert(code, gc.Equals, http.StatusUnauthorized)

	h = make(http.Header)
	resp.(httprequest.HeaderSetter).SetHeader(h)
	c.Assert(h.Get("WWW-Authenticate"), gc.Equals, "Macaroon")

	data, err = json.Marshal(resp)
	c.Assert(err, gc.IsNil)

	c.Assert(string(data), jc.JSONEquals, &httpbakery.Error{
		Code:    httpbakery.ErrInteractionRequired,
		Message: httpbakery.ErrInteractionRequired.Error(),
	})
}

func (*ErrorSuite) TestSetInteraction(c *gc.C) {
	var e httpbakery.Error
	e.SetInteraction("foo", 5)
	c.Assert(e, jc.DeepEquals, httpbakery.Error{
		Info: &httpbakery.ErrorInfo{
			InteractionMethods: map[string]*json.RawMessage{
				"foo": jsonRawMessage("5"),
			},
		},
	})
}

func jsonRawMessage(s string) *json.RawMessage {
	m := json.RawMessage(s)
	return &m
}

var interactionMethodTests = []struct {
	about       string
	err         *httpbakery.Error
	kind        string
	expect      interface{}
	expectError string
}{{
	about:       "no info",
	err:         &httpbakery.Error{},
	expect:      0,
	expectError: `not an interaction-required error \(code \)`,
}, {
	about: "not interaction-required code",
	err: &httpbakery.Error{
		Code: "other",
		Info: &httpbakery.ErrorInfo{},
	},
	expect:      0,
	expectError: `not an interaction-required error \(code other\)`,
}, {
	about: "interaction method not found",
	err: &httpbakery.Error{
		Code: httpbakery.ErrInteractionRequired,
		Info: &httpbakery.ErrorInfo{
			InteractionMethods: map[string]*json.RawMessage{
				"foo": jsonRawMessage("0"),
			},
		},
	},
	kind:        "x",
	expect:      0,
	expectError: `interaction method "x" not found`,
}, {
	about: "cannot unmarshal",
	err: &httpbakery.Error{
		Code: httpbakery.ErrInteractionRequired,
		Info: &httpbakery.ErrorInfo{
			InteractionMethods: map[string]*json.RawMessage{
				"x": jsonRawMessage(`{"X": 45}`),
			},
		},
	},
	kind: "x",
	expect: struct {
		X string
	}{},
	expectError: `cannot unmarshal data for interaction method "x": json: cannot unmarshal number into .* of type string`,
}, {
	about: "success",
	err: &httpbakery.Error{
		Code: httpbakery.ErrInteractionRequired,
		Info: &httpbakery.ErrorInfo{
			InteractionMethods: map[string]*json.RawMessage{
				"x": jsonRawMessage(`45`),
			},
		},
	},
	kind:   "x",
	expect: 45,
}}

func (*ErrorSuite) TestInteractionMethod(c *gc.C) {
	for i, test := range interactionMethodTests {
		c.Logf("test %d: %s", i, test.about)
		v := reflect.New(reflect.TypeOf(test.expect))
		err := test.err.InteractionMethod(test.kind, v.Interface())
		if test.expectError != "" {
			c.Assert(err, gc.ErrorMatches, test.expectError)
		} else {
			c.Assert(err, gc.Equals, nil)
			c.Assert(v.Elem().Interface(), jc.DeepEquals, test.expect)
		}
	}
}
