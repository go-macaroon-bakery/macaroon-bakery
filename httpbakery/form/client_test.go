package form_test

import (
	"bytes"
	"strings"

	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/environschema.v1"

	"gopkg.in/macaroon-bakery.v1/httpbakery/form"
)

type clientSuite struct {
	testing.OsEnvSuite
}

var _ = gc.Suite(&clientSuite{})

type prompt struct {
	description string
	def         string
	secret      bool
}

var ioPrompterTests = []struct {
	about        string
	prompts      []prompt
	input        string
	expect       []string
	expectOutput string
	expectError  string
}{{
	about: "single field no default",
	prompts: []prompt{{
		description: "A",
	}},
	input: "B\n",
	expect: []string{
		"B",
	},
	expectOutput: "A: ",
}, {
	about: "single field with default",
	prompts: []prompt{{
		description: "A",
		def:         "C",
	}},
	input: "B\n",
	expect: []string{
		"B",
	},
	expectOutput: "A (C): ",
}, {
	about: "single field with default no input",
	prompts: []prompt{{
		description: "A",
		def:         "C",
	}},
	input: "\n",
	expect: []string{
		"C",
	},
	expectOutput: "A (C): ",
}, {
	about: "secret single field with default no input",
	prompts: []prompt{{
		description: "A",
		def:         "C",
		secret:      true,
	}},
	input: "\n",
	expect: []string{
		"C",
	},
	expectOutput: "A (*): ",
}, {
	about: "input error",
	prompts: []prompt{{
		description: "A",
		def:         "C",
	}},
	input: "",
	expect: []string{
		"C",
	},
	expectOutput: "A (C): ",
	expectError:  "cannot read input: unexpected EOF",
}}

func (s *clientSuite) TestIOPrompter(c *gc.C) {
tests:
	for i, test := range ioPrompterTests {
		c.Logf("%d. %s", i, test.about)
		outBuf := new(bytes.Buffer)
		prompter := form.IOPrompter{
			In:  strings.NewReader(test.input),
			Out: outBuf,
		}
		for j, p := range test.prompts {
			result, err := prompter.Prompt(p.description, p.def, p.secret)
			if err != nil {
				c.Assert(err, gc.ErrorMatches, test.expectError)
				continue tests
			}
			c.Assert(result, gc.Equals, test.expect[j])
		}
		c.Assert(test.expectError, gc.Equals, "", gc.Commentf("did not reveive expected error"))
		c.Assert(outBuf.String(), gc.Equals, test.expectOutput)
	}
}

var promptingFillerTests = []struct {
	about         string
	schema        environschema.Fields
	responses     []response
	environment   map[string]string
	expectPrompts []prompt
	expectResult  map[string]interface{}
	expectError   string
}{{
	about: "correct ordering",
	schema: environschema.Fields{
		"a1": environschema.Attr{
			Group:       "A",
			Description: "a1",
			Type:        environschema.Tstring,
		},
		"b1": environschema.Attr{
			Group:       "A",
			Description: "b1",
			Type:        environschema.Tstring,
			Secret:      true,
		},
		"c1": environschema.Attr{
			Group:       "A",
			Description: "c1",
			Type:        environschema.Tstring,
		},
		"a2": environschema.Attr{
			Group:       "B",
			Description: "a2",
			Type:        environschema.Tstring,
		},
		"b2": environschema.Attr{
			Group:       "B",
			Description: "b2",
			Type:        environschema.Tstring,
			Secret:      true,
		},
		"c2": environschema.Attr{
			Group:       "B",
			Description: "c2",
			Type:        environschema.Tstring,
		},
	},
	responses: []response{{
		data: "a1",
	}, {
		data: "c1",
	}, {
		data: "b1",
	}, {
		data: "a2",
	}, {
		data: "c2",
	}, {
		data: "b2",
	}},
	expectResult: map[string]interface{}{
		"a1": "a1",
		"b1": "b1",
		"c1": "c1",
		"a2": "a2",
		"b2": "b2",
		"c2": "c2",
	},
	expectPrompts: []prompt{{
		description: "a1",
	}, {
		description: "c1",
	}, {
		description: "b1",
		secret:      true,
	}, {
		description: "a2",
	}, {
		description: "c2",
	}, {
		description: "b2",
		secret:      true,
	}},
}, {
	about: "bool type",
	schema: environschema.Fields{
		"a1": environschema.Attr{
			Group:       "A",
			Description: "a1",
			Type:        environschema.Tbool,
		},
		"b1": environschema.Attr{
			Group:       "A",
			Description: "b1",
			Type:        environschema.Tbool,
		},
	},
	responses: []response{{
		data: "true",
	}, {
		data: "false",
	}},
	expectResult: map[string]interface{}{
		"a1": true,
		"b1": false,
	},
	expectPrompts: []prompt{{
		description: "a1",
	}, {
		description: "b1",
	}},
}, {
	about: "int type",
	schema: environschema.Fields{
		"a1": environschema.Attr{
			Group:       "A",
			Description: "a1",
			Type:        environschema.Tint,
		},
		"b1": environschema.Attr{
			Group:       "A",
			Description: "b1",
			Type:        environschema.Tint,
		},
		"c1": environschema.Attr{
			Group:       "A",
			Description: "c1",
			Type:        environschema.Tint,
		},
	},
	responses: []response{{
		data: "0",
	}, {
		data: "-1000000",
	}, {
		data: "1000000",
	}},
	expectResult: map[string]interface{}{
		"a1": int64(0),
		"b1": int64(-1000000),
		"c1": int64(1000000),
	},
	expectPrompts: []prompt{{
		description: "a1",
	}, {
		description: "b1",
	}, {
		description: "c1",
	}},
}, {
	about: "too many bad responses",
	schema: environschema.Fields{
		"a1": environschema.Attr{
			Group:       "A",
			Description: "a1",
			Type:        environschema.Tint,
		},
	},
	responses: []response{{
		data: "one",
	}, {
		data: "two",
	}, {
		data: "three",
	}},
	expectPrompts: []prompt{{
		description: "a1",
	}, {
		description: "a1",
	}, {
		description: "a1",
	}},
	expectError: "cannot complete form: too many invalid inputs",
}, {
	about: "bad then good input",
	schema: environschema.Fields{
		"a1": environschema.Attr{
			Group:       "A",
			Description: "a1",
			Type:        environschema.Tint,
		},
	},
	responses: []response{{
		data: "one",
	}, {
		data: "two",
	}, {
		data: "3",
	}},
	expectPrompts: []prompt{{
		description: "a1",
	}, {
		description: "a1",
	}, {
		description: "a1",
	}},
	expectResult: map[string]interface{}{
		"a1": int64(3),
	},
}, {
	about: "prompt error",
	schema: environschema.Fields{
		"a1": environschema.Attr{
			Group:       "A",
			Description: "a1",
			Type:        environschema.Tstring,
		},
	},
	responses: []response{{
		err: errgo.New("test error"),
	}},
	expectPrompts: []prompt{{
		description: "a1",
	}},
	expectError: "cannot complete form: cannot get input: test error",
}, {
	about: "default from EnvVar",
	schema: environschema.Fields{
		"a1": environschema.Attr{
			Group:       "A",
			Description: "a1",
			Type:        environschema.Tstring,
			EnvVar:      "A1",
		},
	},
	environment: map[string]string{
		"A1": "a1-default",
	},
	responses: []response{{
		data: "a1",
	}},
	expectPrompts: []prompt{{
		description: "a1",
		def:         "a1-default",
	}},
	expectResult: map[string]interface{}{
		"a1": "a1",
	},
}, {
	about: "default from EnvVars not used",
	schema: environschema.Fields{
		"a1": environschema.Attr{
			Group:       "A",
			Description: "a1",
			Type:        environschema.Tstring,
			EnvVar:      "B1",
			EnvVars: []string{
				"A2",
				"A1",
			},
		},
	},
	environment: map[string]string{
		"A1": "a1-default",
	},
	responses: []response{{
		data: "a1",
	}},
	expectPrompts: []prompt{{
		description: "a1",
		def:         "a1-default",
	}},
	expectResult: map[string]interface{}{
		"a1": "a1",
	},
}}

func (s *clientSuite) TestPromptingFiller(c *gc.C) {
	for i, test := range promptingFillerTests {
		c.Logf("%d. %s", i, test.about)
		func() {
			for k, v := range test.environment {
				defer testing.PatchEnvironment(k, v)()
			}
			p := &testPrompter{
				responses: test.responses,
			}
			f := &form.PromptingFiller{
				Prompter: p,
			}
			result, err := f.Fill(test.schema)
			if test.expectError != "" {
				c.Assert(err, gc.ErrorMatches, test.expectError)
			} else {
				c.Assert(err, gc.IsNil)
			}
			c.Assert(result, jc.DeepEquals, test.expectResult)
			c.Assert(p.prompts, jc.DeepEquals, test.expectPrompts)
		}()
	}
}

type response struct {
	data string
	err  error
}

type testPrompter struct {
	prompts   []prompt
	responses []response
}

func (p *testPrompter) Prompt(description, def string, secret bool) (string, error) {
	i := len(p.prompts)
	r := p.responses[i]
	p.prompts = append(p.prompts, prompt{
		description: description,
		def:         def,
		secret:      secret,
	})
	return r.data, r.err
}
