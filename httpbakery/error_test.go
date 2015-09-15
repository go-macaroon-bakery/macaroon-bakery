package httpbakery_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"

	"github.com/juju/httprequest"
	jc "github.com/juju/testing/checkers"
	"github.com/juju/testing/httptesting"
	gc "gopkg.in/check.v1"
	"gopkg.in/macaroon.v1"

	"gopkg.in/macaroon-bakery.v1/httpbakery"
)

type ErrorSuite struct{}

var _ = gc.Suite(&ErrorSuite{})

func (s *ErrorSuite) TestWriteDischargeRequiredError(c *gc.C) {
	m, err := macaroon.New([]byte("secret"), "id", "a location")
	c.Assert(err, gc.IsNil)
	tests := []struct {
		about            string
		path             string
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
				Macaroon: m,
			},
		},
	}, {
		about: `write discharge required with "an error" but and set a path`,
		path:  "http://foobar:1234",
		err:   errors.New("an error"),
		expectedResponse: httpbakery.Error{
			Code:    httpbakery.ErrDischargeRequired,
			Message: "an error",
			Info: &httpbakery.ErrorInfo{
				Macaroon:     m,
				MacaroonPath: "http://foobar:1234",
			},
		},
	}, {
		about: `write discharge required with nil error but set a path`,
		path:  "http://foobar:1234",
		err:   nil,
		expectedResponse: httpbakery.Error{
			Code:    httpbakery.ErrDischargeRequired,
			Message: httpbakery.ErrDischargeRequired.Error(),
			Info: &httpbakery.ErrorInfo{
				Macaroon:     m,
				MacaroonPath: "http://foobar:1234",
			},
		},
	},
	}

	for i, t := range tests {
		c.Logf("Running test %d %s", i, t.about)
		response := httptest.NewRecorder()
		httpbakery.WriteDischargeRequiredError(response, m, t.path, t.err)
		httptesting.AssertJSONResponse(c, response, http.StatusProxyAuthRequired, t.expectedResponse)
	}
}

func (s *ErrorSuite) TestNewInteractionRequiredError(c *gc.C) {
	// With a request with no version header, the response
	// should be 407.
	req, err := http.NewRequest("GET", "/", nil)
	c.Assert(err, gc.IsNil)

	err = httpbakery.NewInteractionRequiredError("/visit", "/wait", nil, req)
	code, resp := httpbakery.ErrorToResponse(err)
	c.Assert(code, gc.Equals, http.StatusProxyAuthRequired)

	data, err := json.Marshal(resp)
	c.Assert(err, gc.IsNil)

	c.Assert(string(data), jc.JSONEquals, &httpbakery.Error{
		Code:    httpbakery.ErrInteractionRequired,
		Message: httpbakery.ErrInteractionRequired.Error(),
		Info: &httpbakery.ErrorInfo{
			VisitURL: "/visit",
			WaitURL:  "/wait",
		},
	})

	// With a request with a version 1 header, the response
	// should be 401.
	req.Header.Set("Bakery-Protocol-Version", "1")

	err = httpbakery.NewInteractionRequiredError("/visit", "/wait", nil, req)
	code, resp = httpbakery.ErrorToResponse(err)
	c.Assert(code, gc.Equals, http.StatusUnauthorized)

	h := make(http.Header)
	resp.(httprequest.HeaderSetter).SetHeader(h)
	c.Assert(h.Get("WWW-Authenticate"), gc.Equals, "Macaroon")

	data, err = json.Marshal(resp)
	c.Assert(err, gc.IsNil)

	c.Assert(string(data), jc.JSONEquals, &httpbakery.Error{
		Code:    httpbakery.ErrInteractionRequired,
		Message: httpbakery.ErrInteractionRequired.Error(),
		Info: &httpbakery.ErrorInfo{
			VisitURL: "/visit",
			WaitURL:  "/wait",
		},
	})

}
