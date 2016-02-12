package httpbakery_test

import (
	"net"
	"net/http"

	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v1/bakery/checkers"
	"gopkg.in/macaroon-bakery.v1/httpbakery"
)

type CheckersSuite struct{}

var _ = gc.Suite(&CheckersSuite{})

type checkTest struct {
	caveat      string
	expectError string
	expectCause func(err error) bool
}

var isCaveatNotRecognized = errgo.Is(checkers.ErrCaveatNotRecognized)

var checkerTests = []struct {
	about   string
	checker checkers.Checker
	checks  []checkTest
}{{
	about:   "no host name declared",
	checker: checkers.New(httpbakery.Checkers(&http.Request{})),
	checks: []checkTest{{
		caveat:      checkers.ClientIPAddrCaveat(net.IP{0, 0, 0, 0}).Condition,
		expectError: `caveat "client-ip-addr 0.0.0.0" not satisfied: client has no remote address`,
	}, {
		caveat:      checkers.ClientIPAddrCaveat(net.IP{127, 0, 0, 1}).Condition,
		expectError: `caveat "client-ip-addr 127.0.0.1" not satisfied: client has no remote address`,
	}, {
		caveat:      "client-ip-addr badip",
		expectError: `caveat "client-ip-addr badip" not satisfied: cannot parse IP address in caveat`,
	}},
}, {
	about: "IPv4 host name declared",
	checker: checkers.New(httpbakery.Checkers(&http.Request{
		RemoteAddr: "127.0.0.1:1234",
	})),
	checks: []checkTest{{
		caveat: checkers.ClientIPAddrCaveat(net.IP{127, 0, 0, 1}).Condition,
	}, {
		caveat: checkers.ClientIPAddrCaveat(net.IP{127, 0, 0, 1}.To16()).Condition,
	}, {
		caveat: "client-ip-addr ::ffff:7f00:1",
	}, {
		caveat:      checkers.ClientIPAddrCaveat(net.IP{127, 0, 0, 2}).Condition,
		expectError: `caveat "client-ip-addr 127.0.0.2" not satisfied: client IP address mismatch, got 127.0.0.1`,
	}, {
		caveat:      checkers.ClientIPAddrCaveat(net.ParseIP("2001:4860:0:2001::68")).Condition,
		expectError: `caveat "client-ip-addr 2001:4860:0:2001::68" not satisfied: client IP address mismatch, got 127.0.0.1`,
	}},
}, {
	about: "IPv6 host name declared",
	checker: checkers.New(httpbakery.Checkers(&http.Request{
		RemoteAddr: "[2001:4860:0:2001::68]:1234",
	})),
	checks: []checkTest{{
		caveat: checkers.ClientIPAddrCaveat(net.ParseIP("2001:4860:0:2001::68")).Condition,
	}, {
		caveat: "client-ip-addr 2001:4860:0:2001:0::68",
	}, {
		caveat:      checkers.ClientIPAddrCaveat(net.ParseIP("2001:4860:0:2001::69")).Condition,
		expectError: `caveat "client-ip-addr 2001:4860:0:2001::69" not satisfied: client IP address mismatch, got 2001:4860:0:2001::68`,
	}, {
		caveat:      checkers.ClientIPAddrCaveat(net.ParseIP("127.0.0.1")).Condition,
		expectError: `caveat "client-ip-addr 127.0.0.1" not satisfied: client IP address mismatch, got 2001:4860:0:2001::68`,
	}},
}, {
	about: "same client address, ipv4 request address",
	checker: checkers.New(httpbakery.Checkers(&http.Request{
		RemoteAddr: "127.0.0.1:1324",
	})),
	checks: []checkTest{{
		caveat: httpbakery.SameClientIPAddrCaveat(&http.Request{
			RemoteAddr: "127.0.0.1:1234",
		}).Condition,
	}, {
		caveat: httpbakery.SameClientIPAddrCaveat(&http.Request{
			RemoteAddr: "[::ffff:7f00:1]:1235",
		}).Condition,
	}, {
		caveat: httpbakery.SameClientIPAddrCaveat(&http.Request{
			RemoteAddr: "127.0.0.2:1234",
		}).Condition,
		expectError: `caveat "client-ip-addr 127.0.0.2" not satisfied: client IP address mismatch, got 127.0.0.1`,
	}, {
		caveat: httpbakery.SameClientIPAddrCaveat(&http.Request{
			RemoteAddr: "[::ffff:7f00:2]:1235",
		}).Condition,
		expectError: `caveat "client-ip-addr 127.0.0.2" not satisfied: client IP address mismatch, got 127.0.0.1`,
	}, {
		caveat:      httpbakery.SameClientIPAddrCaveat(&http.Request{}).Condition,
		expectError: `caveat "error client has no remote IP address" not satisfied: bad caveat`,
	}, {
		caveat: httpbakery.SameClientIPAddrCaveat(&http.Request{
			RemoteAddr: "bad",
		}).Condition,
		expectError: `caveat "error cannot parse host port in remote address: missing port in address bad" not satisfied: bad caveat`,
	}, {
		caveat: httpbakery.SameClientIPAddrCaveat(&http.Request{
			RemoteAddr: "bad:56",
		}).Condition,
		expectError: `caveat "error invalid IP address in remote address \\"bad:56\\"" not satisfied: bad caveat`,
	}},
}, {
	about: "same client address, ipv6 request address",
	checker: checkers.New(httpbakery.Checkers(&http.Request{
		RemoteAddr: "[2001:4860:0:2001:0::68]:1235",
	})),
	checks: []checkTest{{
		caveat: httpbakery.SameClientIPAddrCaveat(&http.Request{
			RemoteAddr: "[2001:4860:0:2001:0::68]:1234",
		}).Condition,
	}, {
		caveat: httpbakery.SameClientIPAddrCaveat(&http.Request{
			RemoteAddr: "127.0.0.2:1234",
		}).Condition,
		expectError: `caveat "client-ip-addr 127.0.0.2" not satisfied: client IP address mismatch, got 2001:4860:0:2001::68`,
	}},
}, {
	about:   "request with no origin",
	checker: checkers.New(httpbakery.Checkers(&http.Request{})),
	checks: []checkTest{{
		caveat: checkers.ClientOriginCaveat("").Condition,
	}, {
		caveat:      checkers.ClientOriginCaveat("somewhere").Condition,
		expectError: `caveat "origin somewhere" not satisfied: request has invalid Origin header; got ""`,
	}},
}, {
	about: "request with origin",
	checker: checkers.New(httpbakery.Checkers(&http.Request{
		Header: http.Header{
			"Origin": {"somewhere"},
		},
	})),
	checks: []checkTest{{
		caveat:      checkers.ClientOriginCaveat("").Condition,
		expectError: `caveat "origin " not satisfied: request has invalid Origin header; got "somewhere"`,
	}, {
		caveat: checkers.ClientOriginCaveat("somewhere").Condition,
	}},
}}

func (s *CheckersSuite) TestCheckers(c *gc.C) {
	for i, test := range checkerTests {
		c.Logf("test %d: %s", i, test.about)
		for j, check := range test.checks {
			c.Logf("\tcheck %d", j)
			err := checkers.New(test.checker).CheckFirstPartyCaveat(check.caveat)
			if check.expectError != "" {
				c.Assert(err, gc.ErrorMatches, check.expectError)
				if check.expectCause == nil {
					check.expectCause = errgo.Any
				}
				c.Assert(check.expectCause(errgo.Cause(err)), gc.Equals, true)
			} else {
				c.Assert(err, gc.IsNil)
			}
		}
	}
}
