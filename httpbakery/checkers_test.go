package httpbakery_test

import (
	"net"
	"net/http"

	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2/httpbakery"
)

type CheckersSuite struct{}

var _ = gc.Suite(&CheckersSuite{})

type checkTest struct {
	caveat      checkers.Caveat
	expectError string
	expectCause func(err error) bool
}

func caveatWithCondition(cond string) checkers.Caveat {
	return checkers.Caveat{
		Condition: cond,
	}
}

var checkerTests = []struct {
	about  string
	req    *http.Request
	checks []checkTest
}{{
	about: "no host name declared",
	req:   &http.Request{},
	checks: []checkTest{{
		caveat:      httpbakery.ClientIPAddrCaveat(net.IP{0, 0, 0, 0}),
		expectError: `caveat "http:client-ip-addr 0.0.0.0" not satisfied: client has no remote address`,
	}, {
		caveat:      httpbakery.ClientIPAddrCaveat(net.IP{127, 0, 0, 1}),
		expectError: `caveat "http:client-ip-addr 127.0.0.1" not satisfied: client has no remote address`,
	}, {
		caveat:      caveatWithCondition("http:client-ip-addr badip"),
		expectError: `caveat "http:client-ip-addr badip" not satisfied: cannot parse IP address in caveat`,
	}},
}, {
	about: "IPv4 host name declared",
	req: &http.Request{
		RemoteAddr: "127.0.0.1:1234",
	},
	checks: []checkTest{{
		caveat: httpbakery.ClientIPAddrCaveat(net.IP{127, 0, 0, 1}),
	}, {
		caveat: httpbakery.ClientIPAddrCaveat(net.IP{127, 0, 0, 1}.To16()),
	}, {
		caveat: caveatWithCondition("http:client-ip-addr ::ffff:7f00:1"),
	}, {
		caveat:      httpbakery.ClientIPAddrCaveat(net.IP{127, 0, 0, 2}),
		expectError: `caveat "http:client-ip-addr 127.0.0.2" not satisfied: client IP address mismatch, got 127.0.0.1`,
	}, {
		caveat:      httpbakery.ClientIPAddrCaveat(net.ParseIP("2001:4860:0:2001::68")),
		expectError: `caveat "http:client-ip-addr 2001:4860:0:2001::68" not satisfied: client IP address mismatch, got 127.0.0.1`,
	}},
}, {
	about: "IPv6 host name declared",
	req: &http.Request{
		RemoteAddr: "[2001:4860:0:2001::68]:1234",
	},
	checks: []checkTest{{
		caveat: httpbakery.ClientIPAddrCaveat(net.ParseIP("2001:4860:0:2001::68")),
	}, {
		caveat: caveatWithCondition("http:client-ip-addr 2001:4860:0:2001:0::68"),
	}, {
		caveat:      httpbakery.ClientIPAddrCaveat(net.ParseIP("2001:4860:0:2001::69")),
		expectError: `caveat "http:client-ip-addr 2001:4860:0:2001::69" not satisfied: client IP address mismatch, got 2001:4860:0:2001::68`,
	}, {
		caveat:      httpbakery.ClientIPAddrCaveat(net.ParseIP("127.0.0.1")),
		expectError: `caveat "http:client-ip-addr 127.0.0.1" not satisfied: client IP address mismatch, got 2001:4860:0:2001::68`,
	}},
}, {
	about: "same client address, ipv4 request address",
	req: &http.Request{
		RemoteAddr: "127.0.0.1:1324",
	},
	checks: []checkTest{{
		caveat: httpbakery.SameClientIPAddrCaveat(&http.Request{
			RemoteAddr: "127.0.0.1:1234",
		}),
	}, {
		caveat: httpbakery.SameClientIPAddrCaveat(&http.Request{
			RemoteAddr: "[::ffff:7f00:1]:1235",
		}),
	}, {
		caveat: httpbakery.SameClientIPAddrCaveat(&http.Request{
			RemoteAddr: "127.0.0.2:1234",
		}),
		expectError: `caveat "http:client-ip-addr 127.0.0.2" not satisfied: client IP address mismatch, got 127.0.0.1`,
	}, {
		caveat: httpbakery.SameClientIPAddrCaveat(&http.Request{
			RemoteAddr: "[::ffff:7f00:2]:1235",
		}),
		expectError: `caveat "http:client-ip-addr 127.0.0.2" not satisfied: client IP address mismatch, got 127.0.0.1`,
	}, {
		caveat:      httpbakery.SameClientIPAddrCaveat(&http.Request{}),
		expectError: `caveat "error client has no remote IP address" not satisfied: bad caveat`,
	}, {
		caveat: httpbakery.SameClientIPAddrCaveat(&http.Request{
			RemoteAddr: "bad",
		}),
		expectError: `caveat "error cannot parse host port in remote address: .*" not satisfied: bad caveat`,
	}, {
		caveat: httpbakery.SameClientIPAddrCaveat(&http.Request{
			RemoteAddr: "bad:56",
		}),
		expectError: `caveat "error invalid IP address in remote address \\"bad:56\\"" not satisfied: bad caveat`,
	}},
}, {
	about: "same client address, ipv6 request address",
	req: &http.Request{
		RemoteAddr: "[2001:4860:0:2001:0::68]:1235",
	},
	checks: []checkTest{{
		caveat: httpbakery.SameClientIPAddrCaveat(&http.Request{
			RemoteAddr: "[2001:4860:0:2001:0::68]:1234",
		}),
	}, {
		caveat: httpbakery.SameClientIPAddrCaveat(&http.Request{
			RemoteAddr: "127.0.0.2:1234",
		}),
		expectError: `caveat "http:client-ip-addr 127.0.0.2" not satisfied: client IP address mismatch, got 2001:4860:0:2001::68`,
	}},
}, {
	about: "request with no origin",
	req:   &http.Request{},
	checks: []checkTest{{
		caveat: httpbakery.ClientOriginCaveat(""),
	}, {
		caveat: httpbakery.ClientOriginCaveat("somewhere"),
	}},
}, {
	about: "request with origin",
	req: &http.Request{
		Header: http.Header{
			"Origin": {"somewhere"},
		},
	},
	checks: []checkTest{{
		caveat:      httpbakery.ClientOriginCaveat(""),
		expectError: `caveat "http:origin" not satisfied: request has invalid Origin header; got "somewhere"`,
	}, {
		caveat: httpbakery.ClientOriginCaveat("somewhere"),
	}},
}}

func (s *CheckersSuite) TestCheckers(c *gc.C) {
	checker := httpbakery.NewChecker()
	for i, test := range checkerTests {
		c.Logf("test %d: %s", i, test.about)
		ctx := httpbakery.ContextWithRequest(testContext, test.req)
		for j, check := range test.checks {
			c.Logf("\tcheck %d", j)

			err := checker.CheckFirstPartyCaveat(ctx, checker.Namespace().ResolveCaveat(check.caveat).Condition)
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
