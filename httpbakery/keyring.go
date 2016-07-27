package httpbakery

import (
	"net/http"
	"net/url"

	"github.com/juju/httprequest"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
)

// NewThirdPartyLocator returns a new third party
// locator that uses the given client to find
// information about third parties and
// uses the given cache as a backing.
//
// If cache is nil, a new cache will be created.
//
// If client is nil, http.DefaultClient will be used.
func NewThirdPartyLocator(client httprequest.Doer, cache *bakery.ThirdPartyLocatorStore) *ThirdPartyLocator {
	if cache == nil {
		cache = bakery.NewThirdPartyLocatorStore()
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &ThirdPartyLocator{
		client: client,
		cache:  cache,
	}
}

// ThirdPartyLocator represents locator that can interrogate
// third party discharge services for information. By default it refuses
// to use insecure URLs.
type ThirdPartyLocator struct {
	client        httprequest.Doer
	allowInsecure bool
	cache         *bakery.ThirdPartyLocatorStore
}

// AllowInsecure allows insecure URLs. This can be useful
// for testing purposes.
func (kr *ThirdPartyLocator) AllowInsecure() {
	kr.allowInsecure = true
}

// ThirdPartyLocator implements bakery.ThirdPartyLocator
// by first looking in the backing cache and, if that fails,
// making an HTTP request to find the information associated
// with the given discharge location.
func (kr *ThirdPartyLocator) ThirdPartyInfo(loc string) (bakery.ThirdPartyInfo, error) {
	u, err := url.Parse(loc)
	if err != nil {
		return bakery.ThirdPartyInfo{}, errgo.Notef(err, "invalid discharge URL %q", loc)
	}
	if u.Scheme != "https" && !kr.allowInsecure {
		return bakery.ThirdPartyInfo{}, errgo.Newf("untrusted discharge URL %q", loc)
	}
	info, err := kr.cache.ThirdPartyInfo(loc)
	if err == nil {
		return info, nil
	}
	info, err = ThirdPartyInfoForLocation(kr.client, loc)
	if err != nil {
		return bakery.ThirdPartyInfo{}, errgo.Mask(err)
	}
	kr.cache.AddInfo(loc, info)
	return info, nil
}

// ThirdPartyInfoForLocation returns information on the third party
// discharge server running at the given location URL. Note that this is
// insecure if an http: URL scheme is used. If client is nil,
// http.DefaultClient will be used.
func ThirdPartyInfoForLocation(client httprequest.Doer, url string) (bakery.ThirdPartyInfo, error) {
	dclient := newDischargeClient(url, client)
	var resp *http.Response
	// We use Call directly instead of calling dclient.DischargeInfo because currently
	// httprequest doesn't provide an easy way of finding out the HTTP response
	// code from the error when the error response isn't marshaled as JSON.
	// TODO(rog) change httprequest to make this straightforward.
	err := dclient.Client.Call(&dischargeInfoRequest{}, &resp)
	if err != nil {
		return bakery.ThirdPartyInfo{}, errgo.Mask(err)
	}
	defer resp.Body.Close()
	var info bakery.ThirdPartyInfo
	switch resp.StatusCode {
	case http.StatusOK:
		var r dischargeInfoResponse
		if err := httprequest.UnmarshalJSONResponse(resp, &r); err != nil {
			return bakery.ThirdPartyInfo{}, errgo.Mask(err)
		}
		info.PublicKey = *r.PublicKey
		info.Version = r.Version
	case http.StatusNotFound:
		// TODO(rog) this fallback does not work because
		// httprequest.Client.Client doesn't return a
		// response if there's an error. Fix httprequest to
		// return a HTTPErrorResponse error when it
		// can't unmarshal the error return.
		pkResp, err := dclient.PublicKey(&publicKeyRequest{})
		if err != nil {
			return bakery.ThirdPartyInfo{}, errgo.Mask(err)
		}
		info.PublicKey = *pkResp.PublicKey
	default:
		return bakery.ThirdPartyInfo{}, errgo.Mask(dclient.Client.UnmarshalError(resp), errgo.Any)
	}
	return info, nil
}
