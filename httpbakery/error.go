package httpbakery

import (
	"net/http"

	"github.com/juju/utils/jsonhttp"
	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v1"
)

// ErrorCode holds an error code that classifies
// an error returned from a bakery HTTP handler.
type ErrorCode string

func (e ErrorCode) Error() string {
	return string(e)
}

func (e ErrorCode) ErrorCode() ErrorCode {
	return e
}

const (
	ErrBadRequest          = ErrorCode("bad request")
	ErrDischargeRequired   = ErrorCode("macaroon discharge required")
	ErrInteractionRequired = ErrorCode("interaction required")
)

var (
	handleJSON   = jsonhttp.HandleJSON(ErrorToResponse)
	handleErrors = jsonhttp.HandleErrors(ErrorToResponse)
	writeError   = jsonhttp.WriteError(ErrorToResponse)
)

// Error holds the type of a response from an httpbakery HTTP request,
// marshaled as JSON.
type Error struct {
	Code    ErrorCode  `json:",omitempty"`
	Message string     `json:",omitempty"`
	Info    *ErrorInfo `json:",omitempty"`
}

// ErrorInfo holds additional information provided
// by an error.
type ErrorInfo struct {
	// Macaroon may hold a macaroon that, when
	// discharged, may allow access to a service.
	// This field is associated with the ErrDischargeRequired
	// error code.
	Macaroon *macaroon.Macaroon `json:",omitempty"`

	// MacaroonPath holds the URL path to be associated
	// with the macaroon. The macaroon is potentially
	// valid for all URLs under the given path.
	// If it is empty, the macaroon will be associated with
	// the original URL from which the error was returned.
	MacaroonPath string `json:",omitempty"`

	// VisitURL and WaitURL are associated with the
	// ErrInteractionRequired error code.

	// VisitURL holds a URL that the client should visit
	// in a web browser to authenticate themselves.
	VisitURL string `json:",omitempty"`

	// WaitURL holds a URL that the client should visit
	// to acquire the discharge macaroon. A GET on
	// this URL will block until the client has authenticated,
	// and then it will return the discharge macaroon.
	WaitURL string `json:",omitempty"`
}

func (e *Error) Error() string {
	return e.Message
}

func (e *Error) ErrorCode() ErrorCode {
	return e.Code
}

// ErrorInfo returns additional information
// about the error.
// TODO return interface{} here?
func (e *Error) ErrorInfo() *ErrorInfo {
	return e.Info
}

// ErrorToResponse returns the HTTP status and an error body to be
// marshaled as JSON for the given error. This allows a third party
// package to integrate bakery errors into their error responses when
// they encounter an error with a *bakery.Error cause.
func ErrorToResponse(err error) (int, interface{}) {
	errorBody := errorResponseBody(err)
	status := http.StatusInternalServerError
	switch errorBody.Code {
	case ErrBadRequest:
		status = http.StatusBadRequest
	case ErrDischargeRequired, ErrInteractionRequired:
		status = http.StatusProxyAuthRequired
	}
	return status, errorBody
}

type errorInfoer interface {
	ErrorInfo() *ErrorInfo
}

type errorCoder interface {
	ErrorCode() ErrorCode
}

// errorResponse returns an appropriate error
// response for the provided error.
func errorResponseBody(err error) *Error {
	errResp := &Error{
		Message: err.Error(),
	}
	cause := errgo.Cause(err)
	if coder, ok := cause.(errorCoder); ok {
		errResp.Code = coder.ErrorCode()
	}
	if infoer, ok := cause.(errorInfoer); ok {
		errResp.Info = infoer.ErrorInfo()
	}
	return errResp
}

func badRequestErrorf(f string, a ...interface{}) error {
	return errgo.WithCausef(nil, ErrBadRequest, f, a...)
}

// WriteDischargeRequiredError creates an error using
// NewDischargeRequiredError and writes it to the given response writer,
// indicating that the client should discharge the macaroon to allow the
// original request to be accepted.
func WriteDischargeRequiredError(w http.ResponseWriter, m *macaroon.Macaroon, path string, originalErr error) {
	writeError(w, NewDischargeRequiredError(m, path, originalErr))
}

// NewDischargeRequiredError returns an error of type *Error
// that reports the given original error and includes the
// given macaroon.
//
// The returned macaroon will be
// declared as valid for the given URL path and may
// be relative. When the client stores the discharged
// macaroon as a cookie this will be the path associated
// with the cookie. See ErrorInfo.MacaroonPath for
// more information.
func NewDischargeRequiredError(m *macaroon.Macaroon, path string, originalErr error) error {
	if originalErr == nil {
		originalErr = ErrDischargeRequired
	}
	return &Error{
		Message: originalErr.Error(),
		Code:    ErrDischargeRequired,
		Info: &ErrorInfo{
			Macaroon:     m,
			MacaroonPath: path,
		},
	}
}
