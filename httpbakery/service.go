// The httpbakery package layers on top of the bakery
// package - it provides an HTTP-based implementation
// of a macaroon client and server.
package httpbakery

import (
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"

	"code.google.com/p/go.net/publicsuffix"
	"gopkg.in/macaroon.v1"

	"gopkg.in/macaroon-bakery.v0/bakery"
)

// Service represents a service that can use client-provided
// macaroons to authorize requests. It layers on top
// of *bakery.Service, providing http-based methods
// to create third-party caveats.
type Service struct {
	*bakery.Service
}

// DefaultHTTPClient is an http.Client that ensures that
// headers are sent to the server even when the server redirects.
var DefaultHTTPClient = defaultHTTPClient()

func defaultHTTPClient() *http.Client {
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

type cookieLogger struct {
	http.CookieJar
}

func (j *cookieLogger) SetCookies(u *url.URL, cookies []*http.Cookie) {
	log.Printf("%p setting %d cookies for %s", j.CookieJar, len(cookies), u)
	for i, c := range cookies {
		log.Printf("\t%d. path %s; name %s", i, c.Path, c.Name)
	}
	j.CookieJar.SetCookies(u, cookies)
}

// NewService returns a new Service.
func NewService(p bakery.NewServiceParams) (*Service, error) {
	svc, err := bakery.NewService(p)
	if err != nil {
		return nil, err
	}
	return &Service{Service: svc}, nil
}

// NewRequest returns a new request, converting cookies from the
// HTTP request into macaroons in the bakery request when they're
// found. Mmm.
func (svc *Service) NewRequest(httpReq *http.Request, checker bakery.FirstPartyChecker) *bakery.Request {
	req := svc.Service.NewRequest(checker)
	for _, cookie := range httpReq.Cookies() {
		if !strings.HasPrefix(cookie.Name, "macaroon-") {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(cookie.Value)
		if err != nil {
			log.Printf("cannot base64-decode cookie; ignoring: %v", err)
			continue
		}
		var m macaroon.Macaroon
		if err := m.UnmarshalJSON(data); err != nil {
			log.Printf("cannot unmarshal macaroon from cookie; ignoring: %v", err)
			continue
		}
		req.AddClientMacaroon(&m)
	}
	return req
}
