package form

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/juju/schema"
	"golang.org/x/crypto/ssh/terminal"
	"gopkg.in/errgo.v1"
	"gopkg.in/juju/environschema.v1"
)

// PromptingFiller implements a Filler by prompting for each field in
// turn.
type PromptingFiller struct {
	// The Prompter to use to get the responses if this is nil then
	// DefaultPrompter will be used.
	Prompter Prompter
}

// Fill processes fields by first sorting them and then prompting for the
// value of each one in turn.
//
// The fields are sorted by first by group name. Those in the same group
// are sorted so that secret fields come after non-secret ones, finally
// the fields are sorted by description.
//
// Each field will be prompted for, the returned value will then be
// validated against the type for the field. If the returned value does
// not validate correctly it will be prompted again twice before giving
// up.
//
// If a field contains environment variables these will be checked in
// order. The first one that is set will be used as the value of default
// when Prompt is called.
func (f PromptingFiller) Fill(fields environschema.Fields) (map[string]interface{}, error) {
	fs := make(fieldSlice, len(fields))
	var i int
	for k, v := range fields {
		fs[i] = field{
			name:  k,
			attrs: v,
		}
		i++
	}
	sort.Sort(fs)
	form := make(map[string]interface{}, len(fields))
	for _, field := range fs {
		var err error
		form[field.name], err = f.prompt(field.attrs)
		if err != nil {
			return nil, errgo.Notef(err, "cannot complete form")
		}
	}
	return form, nil
}

type field struct {
	name  string
	attrs environschema.Attr
}

type fieldSlice []field

func (s fieldSlice) Len() int {
	return len(s)
}

func (s fieldSlice) Less(i, j int) bool {
	a1 := s[i].attrs
	a2 := s[j].attrs
	if a1.Group != a2.Group {
		return a1.Group < a2.Group
	}
	if a1.Secret != a2.Secret {
		return a2.Secret
	}
	return a1.Description < a2.Description
}

func (s fieldSlice) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (f *PromptingFiller) prompt(attr environschema.Attr) (interface{}, error) {
	prompter := f.Prompter
	if prompter == nil {
		prompter = DefaultPrompter
	}
	def := f.getDefault(attr)
	for i := 0; i < 3; i++ {
		val, err := prompter.Prompt(attr.Description, def, attr.Secret)
		if err != nil {
			return nil, errgo.Notef(err, "cannot get input")
		}
		switch attr.Type {
		case environschema.Tbool:
			b, err := schema.Bool().Coerce(val, nil)
			if err == nil {
				return b, nil
			}
		case environschema.Tint:
			i, err := schema.Int().Coerce(val, nil)
			if err == nil {
				return i, nil
			}
		default:
			return val, nil
		}
	}
	return nil, errgo.New("too many invalid inputs")
}

func (f *PromptingFiller) getDefault(attr environschema.Attr) string {
	if attr.EnvVar != "" {
		if env := os.Getenv(attr.EnvVar); env != "" {
			return env
		}
	}
	for _, envVar := range attr.EnvVars {
		if env := os.Getenv(envVar); env != "" {
			return env
		}
	}
	return ""
}

// A Prompter is used by a PromptingFiller to get the values of the
// fields.
type Prompter interface {
	Prompt(description, def string, secret bool) (string, error)
}

// DefaultPrompter is the default Prompter used by a PromptingFiller when
// Prompter has not been set.
var DefaultPrompter Prompter = IOPrompter{
	In:  os.Stdin,
	Out: os.Stderr,
}

// IOPrompter is a Prompter based around an io.Reader and io.Writer.
type IOPrompter struct {
	In  io.Reader
	Out io.Writer
}

// Prompt implements Prompter. A prompt is written to p.Out consisting of
// the description and if specified def. If secret is true then def will
// be obscured with '*'. It then waits for input on p.In, if secret is
// true and p.In is a terminal then the input will be read without echo.
func (p IOPrompter) Prompt(description, def string, secret bool) (string, error) {
	prompt := description
	if def != "" {
		def1 := def
		if secret {
			def1 = strings.Repeat("*", len(def))
		}
		prompt = fmt.Sprintf("%s (%s)", description, def1)
	}
	prompt += ": "
	_, err := fmt.Fprintf(p.Out, "%s", prompt)
	if err != nil {
		return "", errgo.Notef(err, "cannot write prompt")
	}
	input, err := readLine(p.Out, p.In, secret)
	if err != nil {
		return "", errgo.Notef(err, "cannot read input")
	}
	if len(input) == 0 {
		return def, nil
	}
	return string(input), nil
}

func readLine(w io.Writer, r io.Reader, secret bool) ([]byte, error) {
	if f, ok := r.(*os.File); ok && secret && terminal.IsTerminal(int(f.Fd())) {
		defer w.Write([]byte{'\n'})
		return terminal.ReadPassword(int(f.Fd()))
	}
	var input []byte
	for {
		var buf [1]byte
		n, err := r.Read(buf[:])
		if n == 1 {
			if buf[0] == '\n' {
				break
			}
			input = append(input, buf[0])
		}
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return nil, errgo.Mask(err)
		}
	}
	return input, nil
}
