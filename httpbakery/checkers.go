package httpbakery

import (
	"net"
	"net/http"

	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v0/bakery"
	"gopkg.in/macaroon-bakery.v0/bakery/checkers"
)

type httpContext struct {
	req *http.Request
}

// Checkers implements the standard HTTP-request checkers.
// It does not include the "declared" checker, as that
// must be added for each individual set of macaroons
// that are checked.
func Checkers(req *http.Request) checkers.Checker {
	c := httpContext{req}
	return checkers.Map{
		checkers.CondClientIPAddr: c.clientIPAddr,
	}
}

// clientIPAddr implements the IP client address checker
// for an HTTP request.
func (c httpContext) clientIPAddr(_, addr string) error {
	ip := net.ParseIP(addr)
	if ip == nil {
		return errgo.Newf("cannot parse IP address in caveat")
	}
	if c.req.RemoteAddr == "" {
		return errgo.Newf("client has no remote address")
	}
	reqIP, err := requestIPAddr(c.req)
	if err != nil {
		return errgo.Mask(err)
	}
	if !reqIP.Equal(ip) {
		return errgo.Newf("client IP address mismatch, got %s", reqIP)
	}
	return nil
}

// SameClientIPAddrCaveat returns a caveat that will check that
// the remote IP address is the same as that in the given HTTP request.
func SameClientIPAddrCaveat(req *http.Request) bakery.Caveat {
	if req.RemoteAddr == "" {
		return checkers.ErrorCaveatf("client has no remote IP address")
	}
	ip, err := requestIPAddr(req)
	if err != nil {
		return checkers.ErrorCaveatf("%v", err)
	}
	return checkers.ClientIPAddrCaveat(ip)
}

func requestIPAddr(req *http.Request) (net.IP, error) {
	reqHost, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return nil, errgo.Newf("cannot parse host port in remote address: %v", err)
	}
	ip := net.ParseIP(reqHost)
	if ip == nil {
		return nil, errgo.Newf("invalid IP address in remote address %q", req.RemoteAddr)
	}
	return ip, nil
}
