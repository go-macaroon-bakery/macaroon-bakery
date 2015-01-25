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

	"code.google.com/p/go.net/publicsuffix"
	"github.com/juju/loggo"
	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v1"

	"gopkg.in/macaroon-bakery.v0/bakery"
	"gopkg.in/macaroon-bakery.v0/bakery/checkers"
)

var logger = loggo.GetLogger("httpbakery")

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

// WaitResponse holds the type that should be returned
// by an HTTP response made to a WaitURL
// (See the ErrorInfo type).
type WaitResponse struct {
	Macaroon *macaroon.Macaroon
}

// Do makes an http request to the given client.
// If the request fails with a discharge-required error,
// any required discharge macaroons will be acquired,
// and the request will be repeated with those attached.
//
// Note that because the request may be retried, no
// body may be provided in the http request (otherwise
// the contents will be lost when retrying). For requests
// with a body (for example PUT or POST methods),
// use DoWithBody instead.
//
// If the client.Jar field is non-nil, the macaroons will be
// stored there and made available to subsequent requests.
//
// If interaction is required by the user, the visitWebPage
// function is called with a URL to be opened in a
// web browser.
func Do(client *http.Client, req *http.Request, visitWebPage func(url *url.URL) error) (*http.Response, error) {
	if req.Body != nil {
		return nil, fmt.Errorf("body unexpectedly provided in request - use DoWithBody")
	}
	return DoWithBody(client, req, noBody, visitWebPage)
}

func noBody() (io.ReadCloser, error) {
	return nil, nil
}

// DoWithBody is like Do except that the given getBody function is
// called to obtain the body for the HTTP request. Any returned body
// will be closed after each request is made.
func DoWithBody(client *http.Client, req *http.Request, getBody BodyGetter, visitWebPage func(url *url.URL) error) (*http.Response, error) {
	// Add a temporary cookie jar (without mutating the original
	// client) if there isn't one available.
	if client.Jar == nil {
		client1 := *client
		jar, err := cookiejar.New(&cookiejar.Options{
			PublicSuffixList: publicsuffix.List,
		})
		if err != nil {
			return nil, errgo.Notef(err, "cannot make cookie jar")
		}
		client1.Jar = jar
		client = &client1
	}
	ctxt := &clientContext{
		client:       client,
		visitWebPage: visitWebPage,
	}
	return ctxt.do(req, getBody)
}

// DischargeAll attempts to acquire discharge macaroons for all the
// third party caveats in m, and returns a slice containing all
// of them bound to m.
//
// The returned macaroon slice will not be stored in the client
// cookie jar (see SetCookie if you need to do that).
func DischargeAll(m *macaroon.Macaroon, client *http.Client, visitWebPage func(url *url.URL) error) (macaroon.Slice, error) {
	ctxt := &clientContext{
		client:       client,
		visitWebPage: visitWebPage,
	}
	return bakery.DischargeAll(m, ctxt.obtainThirdPartyDischarge)
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

type clientContext struct {
	client       *http.Client
	visitWebPage func(*url.URL) error
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

func (ctxt *clientContext) do(req *http.Request, getBody BodyGetter) (*http.Response, error) {
	logger.Debugf("client do %s %s {", req.Method, req.URL)
	resp, err := ctxt.do1(req, getBody)
	logger.Debugf("} -> error %#v", err)
	return resp, err
}

func (ctxt *clientContext) do1(req *http.Request, getBody BodyGetter) (*http.Response, error) {
	if err := ctxt.setRequestBody(req, getBody); err != nil {
		return nil, errgo.Mask(err)
	}
	httpResp, err := ctxt.client.Do(req)
	if err != nil {
		return nil, err
	}
	if httpResp.StatusCode != http.StatusProxyAuthRequired {
		return httpResp, nil
	}
	if httpResp.Header.Get("Content-Type") != "application/json" {
		return httpResp, nil
	}
	defer httpResp.Body.Close()

	var resp Error
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return nil, errgo.Notef(err, "cannot unmarshal error response")
	}
	if resp.Code != ErrDischargeRequired {
		return nil, errgo.NoteMask(&resp, fmt.Sprintf("%s %s failed", req.Method, req.URL), errgo.Any)
	}
	if resp.Info == nil || resp.Info.Macaroon == nil {
		return nil, errgo.New("no macaroon found in response")
	}
	mac := resp.Info.Macaroon
	macaroons, err := bakery.DischargeAll(mac, ctxt.obtainThirdPartyDischarge)
	if err != nil {
		return nil, errgo.Mask(err, errgo.Any)
	}
	cookieURL := req.URL
	if path := resp.Info.MacaroonPath; path != "" {
		relURL, err := parseURLPath(path)
		if err != nil {
			logger.Warningf("ignoring invalid path in discharge-required response: %v", err)
		} else {
			cookieURL = req.URL.ResolveReference(relURL)
		}
	}
	if err := SetCookie(ctxt.client.Jar, cookieURL, macaroons); err != nil {
		return nil, errgo.Notef(err, "cannot set cookie")
	}
	if err := ctxt.setRequestBody(req, getBody); err != nil {
		return nil, errgo.Mask(err)
	}
	// Try again with our newly acquired discharge macaroons
	hresp, err := ctxt.client.Do(req)
	if err != nil {
		return nil, errgo.Mask(err, errgo.Any)
	}
	return hresp, nil
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

func (ctxt *clientContext) setRequestBody(req *http.Request, getBody BodyGetter) error {
	body, err := getBody()
	if err != nil {
		return errgo.Notef(err, "cannot get request body")
	}
	req.Body = body
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
	return &http.Cookie{
		Name:  fmt.Sprintf("macaroon-%x", ms[0].Signature()),
		Value: base64.StdEncoding.EncodeToString(data),
		// TODO(rog) other fields, particularly expiry time.
	}, nil
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

func (ctxt *clientContext) addCookie(req *http.Request, ms macaroon.Slice) error {
	cookies, err := NewCookie(ms)
	if err != nil {
		return errgo.Mask(err)
	}
	// TODO should we set it for the URL only, or the host.
	// Can we set cookies such that they'll always get sent to any
	// URL on the given host?
	ctxt.client.Jar.SetCookies(req.URL, []*http.Cookie{cookies})
	return nil
}

func appendURLElem(u, elem string) string {
	if strings.HasSuffix(u, "/") {
		return u + elem
	}
	return u + "/" + elem
}

func (ctxt *clientContext) obtainThirdPartyDischarge(originalLocation string, cav macaroon.Caveat) (*macaroon.Macaroon, error) {
	var resp dischargeResponse
	loc := appendURLElem(cav.Location, "discharge")
	err := postFormJSON(
		loc,
		url.Values{
			"id":       {cav.Id},
			"location": {originalLocation},
		},
		&resp,
		ctxt.postForm,
	)
	if err == nil {
		return resp.Macaroon, nil
	}
	cause, ok := errgo.Cause(err).(*Error)
	if !ok {
		return nil, errgo.Notef(err, "cannot acquire discharge")
	}
	if cause.Code != ErrInteractionRequired {
		return nil, errgo.Mask(err)
	}
	if cause.Info == nil {
		return nil, errgo.Notef(err, "interaction-required response with no info")
	}
	return ctxt.interact(loc, cause.Info.VisitURL, cause.Info.WaitURL)
}

// interact gathers a macaroon by directing the user to interact
// with a web page.
func (ctxt *clientContext) interact(location, visitURLStr, waitURLStr string) (*macaroon.Macaroon, error) {
	visitURL, err := relativeURL(location, visitURLStr)
	if err != nil {
		return nil, errgo.Notef(err, "cannot make relative visit URL")
	}
	waitURL, err := relativeURL(location, waitURLStr)
	if err != nil {
		return nil, errgo.Notef(err, "cannot make relative wait URL")
	}
	if err := ctxt.visitWebPage(visitURL); err != nil {
		return nil, errgo.Notef(err, "cannot start interactive session")
	}
	waitResp, err := ctxt.client.Get(waitURL.String())
	if err != nil {
		return nil, errgo.Notef(err, "cannot get %q", waitURL)
	}
	defer waitResp.Body.Close()
	if waitResp.StatusCode != http.StatusOK {
		var resp Error
		if err := json.NewDecoder(waitResp.Body).Decode(&resp); err != nil {
			return nil, errgo.Notef(err, "cannot unmarshal wait error response")
		}
		return nil, errgo.NoteMask(&resp, "failed to acquire macaroon after waiting", errgo.Any)
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

func (ctxt *clientContext) postForm(url string, data url.Values) (*http.Response, error) {
	getBody := SeekerBody(strings.NewReader(data.Encode()))
	return ctxt.post(url, "application/x-www-form-urlencoded", getBody)
}

// SeekerBody returns a body getter function suitable for
// passing to DoWithBody that always returns the given reader,
// first seeking to its start.
func SeekerBody(r io.ReadSeeker) BodyGetter {
	rc := ioutil.NopCloser(r)
	return func() (io.ReadCloser, error) {
		if _, err := r.Seek(0, 0); err != nil {
			return nil, errgo.Notef(err, "cannot seek")
		}
		return rc, nil
	}
}

type BodyGetter func() (io.ReadCloser, error)

func (ctxt *clientContext) post(url string, bodyType string, getBody BodyGetter) (resp *http.Response, err error) {
	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", bodyType)
	// TODO(rog) see http.shouldRedirectPost
	return ctxt.do(req, getBody)
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

// RequestMacaroons returns any collections of macaroons from the cookies
// found in the request. By convention, each slice will contain a primary
// macaroon followed by its discharges.
func RequestMacaroons(req *http.Request) []macaroon.Slice {
	var mss []macaroon.Slice
	for _, cookie := range req.Cookies() {
		if !strings.HasPrefix(cookie.Name, "macaroon-") {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(cookie.Value)
		if err != nil {
			logger.Errorf("cannot base64-decode cookie; ignoring: %v", err)
			continue
		}
		var ms macaroon.Slice
		if err := json.Unmarshal(data, &ms); err != nil {
			logger.Errorf("cannot unmarshal macaroons from cookie; ignoring: %v", err)
			continue
		}
		mss = append(mss, ms)
	}
	return mss
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
