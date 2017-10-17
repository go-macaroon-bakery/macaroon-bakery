// Copyright 2016 Canonical Ltd.
// Licensed under the LGPLv3, see LICENCE file for details.

// Note: this code was copied from github.com/juju/utils.

package httputil_test

import (
	"fmt"
	"net/url"
	"testing"

	qt "github.com/frankban/quicktest"

	"gopkg.in/macaroon-bakery.v2-unstable/internal/httputil"
)

var relativeURLTests = []struct {
	base        string
	target      string
	expect      string
	expectError string
}{{
	expectError: "non-absolute base URL",
}, {
	base:        "/foo",
	expectError: "non-absolute target URL",
}, {
	base:        "foo",
	expectError: "non-absolute base URL",
}, {
	base:        "/foo",
	target:      "foo",
	expectError: "non-absolute target URL",
}, {
	base:   "/foo",
	target: "/bar",
	expect: "bar",
}, {
	base:   "/foo/",
	target: "/bar",
	expect: "../bar",
}, {
	base:   "/bar",
	target: "/foo/",
	expect: "foo/",
}, {
	base:   "/foo/",
	target: "/bar/",
	expect: "../bar/",
}, {
	base:   "/foo/bar",
	target: "/bar/",
	expect: "../bar/",
}, {
	base:   "/foo/bar/",
	target: "/bar/",
	expect: "../../bar/",
}, {
	base:   "/foo/bar/baz",
	target: "/foo/targ",
	expect: "../targ",
}, {
	base:   "/foo/bar/baz/frob",
	target: "/foo/bar/one/two/",
	expect: "../one/two/",
}, {
	base:   "/foo/bar/baz/",
	target: "/foo/targ",
	expect: "../../targ",
}, {
	base:   "/foo/bar/baz/frob/",
	target: "/foo/bar/one/two/",
	expect: "../../one/two/",
}, {
	base:   "/foo/bar",
	target: "/foot/bar",
	expect: "../foot/bar",
}, {
	base:   "/foo/bar/baz/frob",
	target: "/foo/bar",
	expect: "../../bar",
}, {
	base:   "/foo/bar/baz/frob/",
	target: "/foo/bar",
	expect: "../../../bar",
}, {
	base:   "/foo/bar/baz/frob/",
	target: "/foo/bar/",
	expect: "../../",
}, {
	base:   "/foo/bar/baz",
	target: "/foo/bar/other",
	expect: "other",
}, {
	base:   "/foo/bar/",
	target: "/foo/bar/",
	expect: ".",
}, {
	base:   "/foo/bar",
	target: "/foo/bar",
	expect: "bar",
}, {
	base:   "/foo/bar/",
	target: "/foo/bar/",
	expect: ".",
}, {
	base:   "/foo/bar",
	target: "/foo/",
	expect: ".",
}, {
	base:   "/foo",
	target: "/",
	expect: ".",
}, {
	base:   "/foo/",
	target: "/",
	expect: "../",
}, {
	base:   "/foo/bar",
	target: "/",
	expect: "../",
}, {
	base:   "/foo/bar/",
	target: "/",
	expect: "../../",
}}

func TestRelativeURL(t *testing.T) {
	c := qt.New(t)
	for i, test := range relativeURLTests {
		t.Logf("test %d: %q %q", i, test.base, test.target)
		// Sanity check the test itself.
		if test.expectError == "" {
			baseURL := &url.URL{Path: test.base}
			expectURL := &url.URL{Path: test.expect}
			targetURL := baseURL.ResolveReference(expectURL)
			c.Check(targetURL.Path, qt.Equals, test.target, fmt.Sprintf("resolve reference failure (%q + %q != %q)", test.base, test.expect, test.target))
		}

		result, err := httputil.RelativeURLPath(test.base, test.target)
		if test.expectError != "" {
			c.Assert(err, qt.ErrorMatches, test.expectError)
			c.Assert(result, qt.Equals, "")
		} else {
			c.Assert(err, qt.IsNil)
			c.Check(result, qt.Equals, test.expect)
		}
	}
}
