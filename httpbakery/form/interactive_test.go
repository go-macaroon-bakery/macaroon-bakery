// The tests in this file are interactive and require user input. They
// are therefore not run by default. to run these tests please run:
// 	go test -tags interactive gopkg.in/macaroon-bakery.v1/httpbakery/form
// +build interactive,!windows

package form_test

import (
	"fmt"
	"os"

	"golang.org/x/crypto/ssh/terminal"
	gc "gopkg.in/check.v1"

	"gopkg.in/macaroon-bakery.v1/httpbakery/form"
)

type interactiveSuite struct{}

var _ = gc.Suite(&interactiveSuite{})

var interactiveIOPrompterTests = []struct {
	about       string
	message     string
	description string
	def         string
	secret      bool
	expect      string
}{{
	about:       "simple prompt",
	message:     `Please enter "pass" at the following prompt.`,
	description: "test",
	expect:      "pass",
}, {
	about:       "prompt with default",
	message:     `Please press enter on the following prompt.`,
	description: "test",
	def:         "pass",
	expect:      "pass",
}, {
	about:       "prompt with default",
	message:     `Please enter "pass" at the following prompt.`,
	description: "test",
	def:         "fail",
	expect:      "pass",
}, {
	about:       "secret",
	message:     `Please enter "pass" at the following prompt (there should be no echo)`,
	description: "test",
	secret:      true,
	expect:      "pass",
}, {
	about:       "prompt with default",
	message:     `Please press enter on the following prompt.`,
	description: "test",
	def:         "pass",
	secret:      true,
	expect:      "pass",
}, {
	about:       "prompt with default",
	message:     `Please enter "pass" at the following prompt (there should be no echo)`,
	description: "test",
	def:         "fail",
	secret:      true,
	expect:      "pass",
}}

func (s *interactiveSuite) TestIOPrompter(c *gc.C) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0666)
	c.Assert(err, gc.IsNil)
	prompter := form.IOPrompter{
		In:  f,
		Out: f,
	}
	c.Assert(terminal.IsTerminal(int(f.Fd())), gc.Equals, true)
	for i, test := range interactiveIOPrompterTests {
		c.Logf("%d. %s", i, test.about)
		fmt.Fprintf(f, "%d. %s\n", i, test.message)
		result, err := prompter.Prompt(test.description, test.def, test.secret)
		c.Assert(err, gc.IsNil)
		c.Assert(result, gc.Equals, test.expect)
	}
}
