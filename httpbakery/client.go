package httpbakery

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/juju/httprequest"
	"github.com/juju/loggo"
	"golang.org/x/net/context"
	"golang.org/x/net/publicsuffix"
	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v2-unstable"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
)

var logger = loggo.GetLogger("httpbakery")

var unmarshalError = httprequest.ErrorUnmarshaler(&Error{})

// maxDischargeRetries holds the maximum number of times that an HTTP
// request will be retried after a third party caveat has been successfully
// discharged.
const maxDischargeRetries = 3

// DischargeError represents the error when a third party discharge
// is refused by a server.
type DischargeError struct {
	// Reason holds the underlying remote error that caused the
	// discharge to fail.
	Reason *Error
}

func (e *DischargeError) Error() string {
	return fmt.Sprintf("third party refused discharge: %v", e.Reason)
}

// IsDischargeError reports whether err is a *DischargeError.
func IsDischargeError(err error) bool {
	_, ok := err.(*DischargeError)
	return ok
}

// InteractionError wraps an error returned by a call to visitWebPage.
type InteractionError struct {
	// Reason holds the actual error returned from visitWebPage.
	Reason error
}

func (e *InteractionError) Error() string {
	return fmt.Sprintf("cannot start interactive session: %v", e.Reason)
}

// IsInteractionError reports whether err is an *InteractionError.
func IsInteractionError(err error) bool {
	_, ok := err.(*InteractionError)
	return ok
}

// WaitResponse holds the type that should be returned
// by an HTTP response made to a WaitURL
// (See the ErrorInfo type).
type WaitResponse struct {
	Macaroon *macaroon.Macaroon
}

// NewHTTPClient returns an http.Client that ensures
// that headers are sent to the server even when the
// server redirects a GET request. The returned client
// also contains an empty in-memory cookie jar.
//
// See https://github.com/golang/go/issues/4677
func NewHTTPClient() *http.Client {
	c := *http.DefaultClient
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		if len(via) == 0 {
			return nil
		}
		for attr, val := range via[0].Header {
			if _, ok := req.Header[attr]; !ok {
				req.Header[attr] = val
			}
		}
		return nil
	}
	jar, err := cookiejar.New(&cookiejar.Options{
		PublicSuffixList: publicsuffix.List,
	})
	if err != nil {
		panic(err)
	}
	c.Jar = &cookieLogger{jar}
	return &c
}

// Client holds the context for making HTTP requests
// that automatically acquire and discharge macaroons.
type Client struct {
	// Client holds the HTTP client to use. It should have a cookie
	// jar configured, and when redirecting it should preserve the
	// headers (see NewHTTPClient).
	*http.Client

	// WebPageVisitor holds a Visitor that is called when the
	// discharge process requires further interaction. If this
	// is nil, VisitWebPage will be called; if that is also nil, no
	// interaction will be allowed.
	//
	// The VisitWebPage method will always be called with a map
	// containing a single entry with the key UserInteractionMethod,
	// holding the URL found in the InteractionRequired error's
	// VisitURL field.
	WebPageVisitor Visitor

	// VisitWebPage is called when WebPageVisitor is nil and
	// the discharge process requires further interaction.
	//
	// Note that this field is now deprecated in favour of
	// WebPageVisitor, which will take priority if set.
	VisitWebPage func(*url.URL) error

	// Key holds the client's key. If set, the client will try to
	// discharge third party caveats with the special location
	// "local" by using this key. See bakery.DischargeAllWithKey and
	// bakery.LocalThirdPartyCaveat for more information
	Key *bakery.KeyPair

	// DischargeAcquirer holds the object that will be used to obtain
	// third-party discharges. If nil, the Client itself will be used.
	DischargeAcquirer DischargeAcquirer
}

// DischargeAcquirer can be implemented by clients that want to customize the
// discharge-acquisition process used by a Client.
type DischargeAcquirer interface {
	// AcquireDischarge should return a discharge macaroon for the given third
	// party caveat.
	AcquireDischarge(cav macaroon.Caveat) (*macaroon.Macaroon, error)
}

// NewClient returns a new Client containing an HTTP client
// created with NewHTTPClient and leaves all other fields zero.
func NewClient() *Client {
	return &Client{
		Client: NewHTTPClient(),
	}
}

// Do sends the given HTTP request and returns its response. If the
// request fails with a discharge-required error, any required discharge
// macaroons will be acquired, and the request will be repeated with
// those attached. Do may add headers to req.Header.
//
// If the required discharges were refused by a third party, an error
// with a *DischargeError cause will be returned.
//
// If interaction is required by the user, the visitWebPage function is
// called with a URL to be opened in a web browser. If visitWebPage
// returns an error, an error with a *InteractionError cause will be
// returned. See OpenWebBrowser for a possible implementation of
// visitWebPage.
//
// Do may add headers to req.Header.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	return c.do(req, nil)
}

// DischargeAll attempts to acquire discharge macaroons for all the
// third party caveats in m, and returns a slice containing all
// of them bound to m.
//
// If the discharge fails because a third party refuses to discharge a
// caveat, the returned error will have a cause of type *DischargeError.
// If the discharge fails because visitWebPage returns an error,
// the returned error will have a cause of *InteractionError.
//
// The returned macaroon slice will not be stored in the client
// cookie jar (see SetCookie if you need to do that).
func (c *Client) DischargeAll(m *macaroon.Macaroon) (macaroon.Slice, error) {
	return bakery.DischargeAllWithKey(m, c.dischargeAcquirer().AcquireDischarge, c.Key)
}

func (c *Client) dischargeAcquirer() DischargeAcquirer {
	if c.DischargeAcquirer != nil {
		return c.DischargeAcquirer
	}
	return c
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

// DoWithCustomError is like Do except it allows a client
// to specify a custom error function, getError, which is called on the
// HTTP response and may return a non-nil error if the response holds an
// error. If the cause of the returned error is a *Error value and its
// code is ErrDischargeRequired, the macaroon in its Info field will be
// discharged and the request will be repeated with the discharged
// macaroon. If getError returns nil, it should leave the response body
// unchanged.
//
// If getError is nil, DefaultGetError will be used.
//
// This method can be useful when dealing with APIs that
// return their errors in a format incompatible with Error, but the
// need for it should be avoided when creating new APIs,
// as it makes the endpoints less amenable to generic tools.
func (c *Client) DoWithCustomError(req *http.Request, getError func(resp *http.Response) error) (*http.Response, error) {
	return c.do(req, getError)
}

func (c *Client) do(req *http.Request, getError func(resp *http.Response) error) (*http.Response, error) {
	logger.Debugf("client do %s %s {", req.Method, req.URL)
	resp, err := c.do1(req, getError)
	logger.Debugf("} -> error %#v", err)
	return resp, err
}

func (c *Client) do1(req *http.Request, getError func(resp *http.Response) error) (*http.Response, error) {
	if getError == nil {
		getError = DefaultGetError
	}
	if c.Client.Jar == nil {
		return nil, errgo.New("no cookie jar supplied in HTTP client")
	}
	rreq, ok := newRetryableRequest(req)
	if !ok {
		return nil, fmt.Errorf("request body is not seekable")
	}
	defer rreq.close()

	req.Header.Set(BakeryProtocolHeader, fmt.Sprint(bakery.LatestVersion))

	// Make several attempts to do the request, because we might have
	// to get through several layers of security. We only retry if
	// we get a DischargeRequiredError and succeed in discharging
	// the macaroon in it.
	retry := 0
	for {
		resp, err := c.do2(rreq, getError)
		if err == nil || !isDischargeRequiredError(err) {
			return resp, errgo.Mask(err, errgo.Any)
		}
		if retry++; retry > maxDischargeRetries {
			return nil, errgo.NoteMask(err, fmt.Sprintf("too many (%d) discharge requests", retry-1), errgo.Any)
		}
		if err1 := c.HandleError(req.URL, err); err1 != nil {
			return nil, errgo.Mask(err1, errgo.Any)
		}
		logger.Infof("discharge succeeded; retry %d", retry)
	}
}

func (c *Client) do2(req *retryableRequest, getError func(resp *http.Response) error) (*http.Response, error) {
	if err := req.try(); err != nil {
		return nil, errgo.Mask(err)
	}
	httpResp, err := c.Client.Do(req.req)
	if err != nil {
		return nil, errgo.Mask(err, errgo.Any)
	}
	err = getError(httpResp)
	if err == nil {
		logger.Infof("HTTP response OK (status %v)", httpResp.Status)
		return httpResp, nil
	}
	httpResp.Body.Close()
	return nil, errgo.Mask(err, errgo.Any)
}

// HandleError tries to resolve the given error, which should be a
// response to the given URL, by discharging any macaroon contained in
// it. That is, if the error cause is an *Error and its code is
// ErrDischargeRequired, then it will try to discharge
// err.Info.Macaroon. If the discharge succeeds, the discharged macaroon
// will be saved to the client's cookie jar and ResolveError will return
// nil.
//
// For any other kind of error, the original error will be returned.
func (c *Client) HandleError(reqURL *url.URL, err error) error {
	respErr, ok := errgo.Cause(err).(*Error)
	if !ok {
		return err
	}
	if respErr.Code != ErrDischargeRequired {
		return respErr
	}
	if respErr.Info == nil || respErr.Info.Macaroon == nil {
		return errgo.New("no macaroon found in discharge-required response")
	}
	mac := respErr.Info.Macaroon
	macaroons, err := bakery.DischargeAllWithKey(mac, c.dischargeAcquirer().AcquireDischarge, c.Key)
	if err != nil {
		return errgo.Mask(err, errgo.Any)
	}
	var cookiePath string
	if path := respErr.Info.MacaroonPath; path != "" {
		relURL, err := parseURLPath(path)
		if err != nil {
			logger.Warningf("ignoring invalid path in discharge-required response: %v", err)
		} else {
			cookiePath = reqURL.ResolveReference(relURL).Path
		}
	}
	// TODO use a namespace taken from the error response.
	cookie, err := NewCookie(nil, macaroons)
	if err != nil {
		return errgo.Notef(err, "cannot make cookie")
	}
	cookie.Path = cookiePath
	if name := respErr.Info.CookieNameSuffix; name != "" {
		cookie.Name = "macaroon-" + name
	}
	c.Jar.SetCookies(reqURL, []*http.Cookie{cookie})
	return nil
}

// DefaultGetError is the default error unmarshaler used by Client.Do.
func DefaultGetError(httpResp *http.Response) error {
	if httpResp.StatusCode != http.StatusProxyAuthRequired && httpResp.StatusCode != http.StatusUnauthorized {
		return nil
	}
	// Check for the new protocol discharge error.
	if httpResp.StatusCode == http.StatusUnauthorized && httpResp.Header.Get("WWW-Authenticate") != "Macaroon" {
		return nil
	}
	if httpResp.Header.Get("Content-Type") != "application/json" {
		return nil
	}
	var resp Error
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return fmt.Errorf("cannot unmarshal error response: %v", err)
	}
	return &resp
}

func parseURLPath(path string) (*url.URL, error) {
	u, err := url.Parse(path)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	if u.Scheme != "" ||
		u.Opaque != "" ||
		u.User != nil ||
		u.Host != "" ||
		u.RawQuery != "" ||
		u.Fragment != "" {
		return nil, errgo.Newf("URL path %q is not clean", path)
	}
	return u, nil
}

// NewCookie takes a slice of macaroons and returns them
// encoded as a cookie. The slice should contain a single primary
// macaroon in its first element, and any discharges after that.
//
// The given namespace specifies the first party caveat namespace,
// used for deriving the expiry time of the cookie.
func NewCookie(ns *checkers.Namespace, ms macaroon.Slice) (*http.Cookie, error) {
	if len(ms) == 0 {
		return nil, errgo.New("no macaroons in cookie")
	}
	// TODO(rog) marshal cookie as binary if version allows.
	data, err := json.Marshal(ms)
	if err != nil {
		return nil, errgo.Notef(err, "cannot marshal macaroons")
	}
	cookie := &http.Cookie{
		Name:  fmt.Sprintf("macaroon-%x", ms[0].Signature()),
		Value: base64.StdEncoding.EncodeToString(data),
	}
	cookie.Expires, _ = checkers.MacaroonsExpiryTime(ns, ms)
	// TODO(rog) other fields.
	return cookie, nil
}

// SetCookie sets a cookie for the given URL on the given cookie jar
// that will holds the given macaroon slice. The macaroon slice should
// contain a single primary macaroon in its first element, and any
// discharges after that.
//
// The given namespace specifies the first party caveat namespace,
// used for deriving the expiry time of the cookie.
func SetCookie(jar http.CookieJar, url *url.URL, ns *checkers.Namespace, ms macaroon.Slice) error {
	cookie, err := NewCookie(ns, ms)
	if err != nil {
		return errgo.Mask(err)
	}
	jar.SetCookies(url, []*http.Cookie{cookie})
	return nil
}

// MacaroonsForURL returns any macaroons associated with the
// given URL in the given cookie jar.
func MacaroonsForURL(jar http.CookieJar, u *url.URL) []macaroon.Slice {
	return cookiesToMacaroons(jar.Cookies(u))
}

func appendURLElem(u, elem string) string {
	if strings.HasSuffix(u, "/") {
		return u + elem
	}
	return u + "/" + elem
}

// AcquireDischarge implements DischargeAcquirer by requesting a discharge
// macaroon from the caveat location as an HTTP URL.
func (c *Client) AcquireDischarge(cav macaroon.Caveat) (*macaroon.Macaroon, error) {
	dclient := newDischargeClient(cav.Location, c)
	var id, id64 string
	if utf8.Valid(cav.Id) {
		id = string(cav.Id)
	} else {
		id64 = base64.RawURLEncoding.EncodeToString(cav.Id)
	}
	resp, err := dclient.Discharge(&dischargeRequest{
		Id:   id,
		Id64: id64,
	})
	if err == nil {
		return resp.Macaroon, nil
	}
	cause, ok := errgo.Cause(err).(*Error)
	if !ok {
		return nil, errgo.NoteMask(err, "cannot acquire discharge", IsInteractionError)
	}
	if cause.Code != ErrInteractionRequired {
		return nil, &DischargeError{
			Reason: cause,
		}
	}
	if cause.Info == nil {
		return nil, errgo.Notef(err, "interaction-required response with no info")
	}
	// Make sure the location has a trailing slash so that
	// the relative URL calculations work correctly even when
	// cav.Location doesn't have a trailing slash.
	loc := appendURLElem(cav.Location, "")
	m, err := c.interact(loc, cause.Info.VisitURL, cause.Info.WaitURL)
	if err != nil {
		return nil, errgo.Mask(err, IsDischargeError, IsInteractionError)
	}
	return m, nil
}

// interact gathers a macaroon by directing the user to interact with a
// web page.
func (c *Client) interact(location, visitURLStr, waitURLStr string) (*macaroon.Macaroon, error) {
	visitURL, err := relativeURL(location, visitURLStr)
	if err != nil {
		return nil, errgo.Notef(err, "cannot make relative visit URL")
	}
	waitURL, err := relativeURL(location, waitURLStr)
	if err != nil {
		return nil, errgo.Notef(err, "cannot make relative wait URL")
	}
	switch {
	case c.WebPageVisitor != nil:
		err = c.WebPageVisitor.VisitWebPage(c, map[string]*url.URL{
			UserInteractionMethod: visitURL,
		})
	case c.VisitWebPage != nil:
		err = c.VisitWebPage(visitURL)
	default:
		err = errgo.New("interaction required but not possible")
	}
	if err != nil {
		return nil, &InteractionError{
			Reason: err,
		}
	}
	waitResp, err := c.Client.Get(waitURL.String())
	if err != nil {
		return nil, errgo.Notef(err, "cannot get %q", waitURL)
	}
	defer waitResp.Body.Close()
	if waitResp.StatusCode != http.StatusOK {
		var resp Error
		if err := json.NewDecoder(waitResp.Body).Decode(&resp); err != nil {
			return nil, errgo.Notef(err, "cannot unmarshal wait error response")
		}
		dischargeErr := &DischargeError{
			Reason: &resp,
		}
		return nil, errgo.NoteMask(dischargeErr, "failed to acquire macaroon after waiting", errgo.Any)
	}
	var resp WaitResponse
	if err := json.NewDecoder(waitResp.Body).Decode(&resp); err != nil {
		return nil, errgo.Notef(err, "cannot unmarshal wait response")
	}
	if resp.Macaroon == nil {
		return nil, errgo.New("no macaroon found in wait response")
	}
	return resp.Macaroon, nil
}

// TODO(rog) move a lot of the code below into server.go, as it's
// much more about server side than client side.

// MacaroonsHeader is the key of the HTTP header that can be used to provide a
// macaroon for request authorization.
const MacaroonsHeader = "Macaroons"

// RequestMacaroons returns any collections of macaroons from the header and
// cookies found in the request. By convention, each slice will contain a
// primary macaroon followed by its discharges.
func RequestMacaroons(req *http.Request) []macaroon.Slice {
	mss := cookiesToMacaroons(req.Cookies())
	for _, h := range req.Header[MacaroonsHeader] {
		ms, err := decodeMacaroonSlice(h)
		if err != nil {
			logger.Errorf("cannot retrieve macaroons from header: %v", err)
		} else {
			mss = append(mss, ms)
		}
	}
	return mss
}

// cookiesToMacaroons returns a slice of any macaroons found
// in the given slice of cookies.
func cookiesToMacaroons(cookies []*http.Cookie) []macaroon.Slice {
	var mss []macaroon.Slice
	for _, cookie := range cookies {
		if !strings.HasPrefix(cookie.Name, "macaroon-") {
			continue
		}
		ms, err := decodeMacaroonSlice(cookie.Value)
		if err != nil {
			logger.Errorf("cannot retrieve macaroons from cookie: %v", err)
			continue
		}
		mss = append(mss, ms)
	}
	return mss
}

// decodeMacaroonSlice decodes a base64-JSON-encoded slice of macaroons from
// the given string.
func decodeMacaroonSlice(value string) (macaroon.Slice, error) {
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, errgo.NoteMask(err, "cannot base64-decode macaroons")
	}
	// TODO(rog) accept binary encoded macaroon cookies.
	var ms macaroon.Slice
	if err := json.Unmarshal(data, &ms); err != nil {
		return nil, errgo.NoteMask(err, "cannot unmarshal macaroons")
	}
	return ms, nil
}

func isVerificationError(err error) bool {
	_, ok := err.(*bakery.VerificationError)
	return ok
}

// CheckRequest checks that the given http request contains at least one
// valid macaroon minted by the given service, using checker to check
// any first party caveats. It returns an error with a
// *bakery.VerificationError cause if the macaroon verification failed.
//
// It assumes that checker implements the HTTP and standard caveat
// checkers.
//
// The assert map holds any required attributes of "declared" attributes,
// overriding any inferences made from the macaroons themselves.
// It has a similar effect to adding a checkers.DeclaredCaveat
// for each key and value, but the error message will be more
// useful.
//
// It adds all the standard caveat checkers to the given checker.
//
// It returns any attributes declared in the successfully validated request
// and the macaroon that was successfully checked.
func CheckRequest(ctxt context.Context, svc *bakery.Service, req *http.Request, assert map[string]string) (map[string]string, macaroon.Slice, error) {
	mss := RequestMacaroons(req)
	if len(mss) == 0 {
		return nil, nil, &bakery.VerificationError{
			Reason: errgo.Newf("no macaroon cookies in request"),
		}
	}
	ctxt = ContextWithRequest(ctxt, req)
	attrs, ms, err := svc.CheckAny(ctxt, mss, assert)
	if err != nil {
		return nil, nil, errgo.Mask(err, isVerificationError)
	}
	return attrs, ms, nil
}

type cookieLogger struct {
	http.CookieJar
}

func (j *cookieLogger) SetCookies(u *url.URL, cookies []*http.Cookie) {
	logger.Debugf("%p setting %d cookies for %s", j.CookieJar, len(cookies), u)
	for i, c := range cookies {
		logger.Debugf("\t%d. path %s; name %s", i, c.Path, c.Name)
	}
	j.CookieJar.SetCookies(u, cookies)
}

type nopSeekCloser struct {
	io.ReadSeeker
}

func (nopSeekCloser) Close() error {
	return nil
}
