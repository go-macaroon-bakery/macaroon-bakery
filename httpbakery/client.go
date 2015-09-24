package httpbakery

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"

	"github.com/juju/loggo"
	"golang.org/x/net/publicsuffix"
	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v1"

	"gopkg.in/macaroon-bakery.v1/bakery"
	"gopkg.in/macaroon-bakery.v1/bakery/checkers"
)

var logger = loggo.GetLogger("httpbakery")

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

// IsInteractionError reports whether err is a *DischargeError.
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

	// VisitWebPage is called when the authorization process
	// requires user interaction, and should cause the given URL to
	// be opened in a web browser. If this is nil, no interaction
	// will be allowed.
	VisitWebPage func(*url.URL) error

	// Key holds the client's key. If set, the client will try to
	// discharge third party caveats with the special location
	// "local" by using this key. See bakery.DischargeAllWithKey and
	// bakery.LocalThirdPartyCaveat for more information
	Key *bakery.KeyPair
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
// Note that because the request may be retried, no body may be provided
// in the request, otherwise the contents will be lost when retrying.
// For requests with a body (for example PUT or POST methods), use
// DoWithBody instead.
//
// If interaction is required by the user, the visitWebPage function is
// called with a URL to be opened in a web browser. If visitWebPage
// returns an error, an error with a *InteractionError cause will be
// returned. See OpenWebBrowser for a possible implementation of
// visitWebPage.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		return nil, fmt.Errorf("body unexpectedly provided in request - use DoWithBody")
	}
	return c.DoWithBody(req, nil)
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
	return bakery.DischargeAllWithKey(m, c.obtainThirdPartyDischarge, c.Key)
}

// PublicKeyForLocation returns the public key from a macaroon
// discharge server running at the given location URL.
// Note that this is insecure if an http: URL scheme is used.
func PublicKeyForLocation(client *http.Client, url string) (*bakery.PublicKey, error) {
	url = url + "/publickey"
	resp, err := client.Get(url)
	if err != nil {
		return nil, errgo.Notef(err, "cannot get public key from %q", url)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errgo.Newf("cannot get public key from %q: got status %s", url, resp.Status)
	}
	defer resp.Body.Close()
	data, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errgo.Notef(err, "failed to read response body from %q", url)
	}
	var pubkey struct {
		PublicKey *bakery.PublicKey
	}
	err = json.Unmarshal(data, &pubkey)
	if err != nil {
		return nil, errgo.Notef(err, "failed to decode response from %q", url)
	}
	return pubkey.PublicKey, nil
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

// DoWithBody is like Do except that the given body is used for the body
// of the HTTP request, and reset to its start by seeking if the request
// is retried. It is an error if req.Body is non-zero.
//
// Do may add headers to req.Header.
func (c *Client) DoWithBody(req *http.Request, body io.ReadSeeker) (*http.Response, error) {
	return c.DoWithBodyAndCustomError(req, body, nil)
}

// DoWithBodyAndCustomError is like DoWithBody except it
// allows a client to specify a custom error function, getError,
// which is called on the HTTP response and should return
// a non-nil Error if the response holds an error that
// can be converted to an Error value. The getError call must leave the
// response body unchanged otherwise.
//
// If getError is nil, DefaultGetError will be used.
//
// This method can be useful when dealing with APIs that
// return their errors in a format incompatible with Error, but the
// need for it should be avoided when creating new APIs,
// as it makes the endpoints less amenable to generic tools.
func (c *Client) DoWithBodyAndCustomError(req *http.Request, body io.ReadSeeker, getError func(resp *http.Response) *Error) (*http.Response, error) {
	logger.Debugf("client do %s %s {", req.Method, req.URL)
	resp, err := c.doWithBody(req, body, getError)
	logger.Debugf("} -> error %#v", err)
	return resp, err
}

func (c *Client) doWithBody(req *http.Request, body io.ReadSeeker, getError func(resp *http.Response) *Error) (*http.Response, error) {
	if getError == nil {
		getError = DefaultGetError
	}
	if req.Body != nil {
		return nil, errgo.New("body unexpectedly supplied in Request struct")
	}
	if c.Client.Jar == nil {
		return nil, errgo.New("no cookie jar supplied in HTTP client")
	}
	if err := c.setRequestBody(req, body); err != nil {
		return nil, errgo.Mask(err)
	}
	req.Header.Set(BakeryProtocolHeader, fmt.Sprint(latestVersion))
	httpResp, err := c.Client.Do(req)
	if err != nil {
		return nil, errgo.Mask(err, errgo.Any)
	}
	respErr := getError(httpResp)
	if respErr == nil {
		return httpResp, nil
	}
	httpResp.Body.Close()
	if respErr.Code != ErrDischargeRequired {
		return nil, errgo.NoteMask(respErr, fmt.Sprintf("%s %s failed", req.Method, req.URL), errgo.Any)
	}
	if respErr.Info == nil || respErr.Info.Macaroon == nil {
		return nil, errgo.New("no macaroon found in discharge-required response")
	}
	mac := respErr.Info.Macaroon
	macaroons, err := bakery.DischargeAllWithKey(mac, c.obtainThirdPartyDischarge, c.Key)
	if err != nil {
		return nil, errgo.Mask(err, errgo.Any)
	}
	var cookiePath string
	if path := respErr.Info.MacaroonPath; path != "" {
		relURL, err := parseURLPath(path)
		if err != nil {
			logger.Warningf("ignoring invalid path in discharge-required response: %v", err)
		} else {
			cookiePath = req.URL.ResolveReference(relURL).Path
		}
	}
	if cookiePath != "/" {
		cookiePath = strings.TrimSuffix(cookiePath, "/")
	}
	cookie, err := NewCookie(macaroons)
	if err != nil {
		return nil, errgo.Notef(err, "cannot make cookie")
	}
	cookie.Path = cookiePath
	c.Jar.SetCookies(req.URL, []*http.Cookie{cookie})

	if err := c.setRequestBody(req, body); err != nil {
		return nil, errgo.Mask(err)
	}
	// Try again with our newly acquired discharge macaroons
	hresp, err := c.Client.Do(req)
	if err != nil {
		return nil, errgo.Mask(err, errgo.Any)
	}
	return hresp, nil
}

// DefaultGetError is the default error unmarshaler used by Client.DoWithBody.
func DefaultGetError(httpResp *http.Response) *Error {
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
		return &Error{
			Message: fmt.Sprintf("cannot unmarshal error response: %v", err),
		}
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

func (c *Client) setRequestBody(req *http.Request, body io.ReadSeeker) error {
	if body == nil {
		return nil
	}
	if req.Body == nil {
		req.Body = ioutil.NopCloser(body)
	} else {
		_, err := body.Seek(0, 0)
		if err != nil {
			return errgo.Notef(err, "cannot seek to start of request body")
		}
	}
	return nil
}

// NewCookie takes a slice of macaroons and returns them
// encoded as a cookie. The slice should contain a single primary
// macaroon in its first element, and any discharges after that.
func NewCookie(ms macaroon.Slice) (*http.Cookie, error) {
	if len(ms) == 0 {
		return nil, errgo.New("no macaroons in cookie")
	}
	data, err := json.Marshal(ms)
	if err != nil {
		return nil, errgo.Notef(err, "cannot marshal macaroons")
	}
	cookie := &http.Cookie{
		Name:  fmt.Sprintf("macaroon-%x", ms[0].Signature()),
		Value: base64.StdEncoding.EncodeToString(data),
	}
	cookie.Expires, _ = checkers.MacaroonsExpiryTime(ms)
	// TODO(rog) other fields.
	return cookie, nil
}

// SetCookie sets a cookie for the given URL on the given cookie jar
// that will holds the given macaroon slice. The macaroon slice should
// contain a single primary macaroon in its first element, and any
// discharges after that.
func SetCookie(jar http.CookieJar, url *url.URL, ms macaroon.Slice) error {
	cookie, err := NewCookie(ms)
	if err != nil {
		return errgo.Mask(err)
	}
	// TODO verify that setting this for the URL makes it available
	// to all paths under that URL.
	jar.SetCookies(url, []*http.Cookie{cookie})
	return nil
}

func (c *Client) addCookie(req *http.Request, ms macaroon.Slice) error {
	cookies, err := NewCookie(ms)
	if err != nil {
		return errgo.Mask(err)
	}
	// TODO should we set it for the URL only, or the host.
	// Can we set cookies such that they'll always get sent to any
	// URL on the given host?
	c.Jar.SetCookies(req.URL, []*http.Cookie{cookies})
	return nil
}

func appendURLElem(u, elem string) string {
	if strings.HasSuffix(u, "/") {
		return u + elem
	}
	return u + "/" + elem
}

func (c *Client) obtainThirdPartyDischarge(originalLocation string, cav macaroon.Caveat) (*macaroon.Macaroon, error) {
	var resp dischargeResponse
	loc := appendURLElem(cav.Location, "discharge")
	err := postFormJSON(
		loc,
		url.Values{
			"id":       {cav.Id},
			"location": {originalLocation},
		},
		&resp,
		c.postForm,
	)
	if err == nil {
		return resp.Macaroon, nil
	}
	cause, ok := errgo.Cause(err).(*Error)
	if !ok {
		return nil, errgo.Notef(err, "cannot acquire discharge")
	}
	if cause.Code != ErrInteractionRequired {
		return nil, &DischargeError{
			Reason: cause,
		}
	}
	if cause.Info == nil {
		return nil, errgo.Notef(err, "interaction-required response with no info")
	}
	m, err := c.interact(loc, cause.Info.VisitURL, cause.Info.WaitURL)
	if err != nil {
		return nil, errgo.Mask(err, IsDischargeError, IsInteractionError)
	}
	return m, nil
}

// interact gathers a macaroon by directing the user to interact
// with a web page.
func (c *Client) interact(location, visitURLStr, waitURLStr string) (*macaroon.Macaroon, error) {
	visitURL, err := relativeURL(location, visitURLStr)
	if err != nil {
		return nil, errgo.Notef(err, "cannot make relative visit URL")
	}
	waitURL, err := relativeURL(location, waitURLStr)
	if err != nil {
		return nil, errgo.Notef(err, "cannot make relative wait URL")
	}
	if c.VisitWebPage == nil {
		return nil, &InteractionError{
			Reason: errgo.New("interaction required but not possible"),
		}
	}
	if err := c.VisitWebPage(visitURL); err != nil {
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

func (c *Client) postForm(url string, data url.Values) (*http.Response, error) {
	return c.post(url, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
}

func (c *Client) post(url string, bodyType string, body io.ReadSeeker) (resp *http.Response, err error) {
	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", bodyType)
	// TODO(rog) see http.shouldRedirectPost
	return c.DoWithBody(req, body)
}

// postFormJSON does an HTTP POST request to the given url with the given
// values and unmarshals the response in the value pointed to be resp.
// It uses the given postForm function to actually make the POST request.
func postFormJSON(url string, vals url.Values, resp interface{}, postForm func(url string, vals url.Values) (*http.Response, error)) error {
	logger.Debugf("postFormJSON to %s; vals: %#v", url, vals)
	httpResp, err := postForm(url, vals)
	if err != nil {
		return errgo.NoteMask(err, fmt.Sprintf("cannot http POST to %q", url), errgo.Any)
	}
	defer httpResp.Body.Close()
	data, err := ioutil.ReadAll(httpResp.Body)
	if err != nil {
		return errgo.Notef(err, "failed to read body from %q", url)
	}
	if httpResp.StatusCode != http.StatusOK {
		var errResp Error
		if err := json.Unmarshal(data, &errResp); err != nil {
			// TODO better error here
			return errgo.Notef(err, "POST %q failed with status %q; cannot parse body %q", url, httpResp.Status, data)
		}
		return &errResp
	}
	if err := json.Unmarshal(data, resp); err != nil {
		return errgo.Notef(err, "cannot unmarshal response from %q", url)
	}
	return nil
}

// MacaroonsHeader is the key of the HTTP header that can be used to provide a
// macaroon for request authorization.
const MacaroonsHeader = "Macaroons"

// RequestMacaroons returns any collections of macaroons from the header and
// cookies found in the request. By convention, each slice will contain a
// primary macaroon followed by its discharges.
func RequestMacaroons(req *http.Request) []macaroon.Slice {
	var mss []macaroon.Slice
	for _, h := range req.Header[MacaroonsHeader] {
		ms, err := decodeMacaroonSlice(h)
		if err != nil {
			logger.Errorf("cannot retrieve macaroons from header: %v", err)
		} else {
			mss = append(mss, ms)
		}
	}
	for _, cookie := range req.Cookies() {
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
// The assert map holds any required attributes of "declared" attributes,
// overriding any inferences made from the macaroons themselves.
// It has a similar effect to adding a checkers.DeclaredCaveat
// for each key and value, but the error message will be more
// useful.
//
// It adds all the standard caveat checkers to the given checker.
//
// It returns any attributes declared in the successfully validated request.
func CheckRequest(svc *bakery.Service, req *http.Request, assert map[string]string, checker checkers.Checker) (map[string]string, error) {
	mss := RequestMacaroons(req)
	if len(mss) == 0 {
		return nil, &bakery.VerificationError{
			Reason: errgo.Newf("no macaroon cookies in request"),
		}
	}
	checker = checkers.New(
		checker,
		Checkers(req),
		checkers.TimeBefore,
	)
	attrs, err := svc.CheckAny(mss, assert, checker)
	if err != nil {
		return nil, errgo.Mask(err, isVerificationError)
	}
	return attrs, nil
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
