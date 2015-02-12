package httpbakery_test

import (
	"errors"
	"net/http"
	"net/http/httptest"

	"github.com/juju/testing/httptesting"
	gc "gopkg.in/check.v1"

	"gopkg.in/macaroon-bakery.v0/httpbakery"
	"gopkg.in/macaroon.v1"
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
