package form_test

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/juju/httprequest"
	jujutesting "github.com/juju/testing"
	"github.com/juju/testing/httptesting"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/environschema.v1"
	esform "gopkg.in/juju/environschema.v1/form"

	"gopkg.in/macaroon-bakery.v1/bakery"
	"gopkg.in/macaroon-bakery.v1/bakery/checkers"
	"gopkg.in/macaroon-bakery.v1/bakerytest"
	"gopkg.in/macaroon-bakery.v1/httpbakery"
	"gopkg.in/macaroon-bakery.v1/httpbakery/form"
)

type formSuite struct {
	jujutesting.LoggingSuite
}

var _ = gc.Suite(&formSuite{})

var formLoginTests = []struct {
	about       string
	opts        dischargeOptions
	filler      fillerFunc
	fallback    httpbakery.Visitor
	expectError string
}{{
	about: "complete visit",
}, {
	about: "visit error",
	opts: dischargeOptions{
		visitError: true,
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: no methods supported`,
}, {
	about: "interaction methods not supported",
	opts: dischargeOptions{
		ignoreAccept: true,
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: no methods supported`,
}, {
	about: "form visit method not supported",
	opts: dischargeOptions{
		formUnsupported: true,
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: no methods supported`,
}, {
	about: "error getting schema",
	opts: dischargeOptions{
		getError: true,
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: cannot get schema.*: test error`,
}, {
	about: "error submitting form",
	opts: dischargeOptions{
		postError: true,
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: cannot submit form.*: test error`,
}, {
	about: "no schema",
	opts: dischargeOptions{
		emptySchema: true,
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: invalid schema: no fields found`,
}, {
	about: "filler error",
	filler: func(esform.Form) (map[string]interface{}, error) {
		return nil, testError
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: cannot handle form: test error`,
}, {
	about: "interaction methods fallback success",
	opts: dischargeOptions{
		ignoreAccept: true,
	},
	fallback: visitorFunc(func(c *httpbakery.Client, m map[string]*url.URL) error {
		req, _ := http.NewRequest("GET", m[httpbakery.UserInteractionMethod].String()+"&fallback=OK", nil)
		resp, err := c.Do(req)
		if err == nil {
			resp.Body.Close()
		}
		return err
	}),
}, {
	about: "interaction methods fallback failure",
	opts: dischargeOptions{
		ignoreAccept: true,
	},
	fallback: visitorFunc(func(c *httpbakery.Client, m map[string]*url.URL) error {
		return testError
	}),
	expectError: `cannot get discharge from ".*": cannot start interactive session: test error`,
}, {
	about: "form not supported fallback success",
	opts: dischargeOptions{
		formUnsupported: true,
	},
	fallback: visitorFunc(func(c *httpbakery.Client, m map[string]*url.URL) error {
		req, err := http.NewRequest("GET", m["othermethod"].String()+"&fallback=OK", nil)
		if err != nil {
			panic(err)
		}
		resp, err := c.Do(req)
		if err == nil {
			resp.Body.Close()
		}
		return err
	}),
}, {
	about: "form not supported fallback failure",
	opts: dischargeOptions{
		formUnsupported: true,
	},
	fallback: visitorFunc(func(c *httpbakery.Client, m map[string]*url.URL) error {
		return testError
	}),
	expectError: `cannot get discharge from ".*": cannot start interactive session: test error`,
}}

type visitorFunc func(*httpbakery.Client, map[string]*url.URL) error

func (f visitorFunc) VisitWebPage(c *httpbakery.Client, m map[string]*url.URL) error {
	return f(c, m)
}

func (s *formSuite) TestFormLogin(c *gc.C) {
	d := &formDischarger{}
	d.discharger = bakerytest.NewInteractiveDischarger(nil, http.HandlerFunc(d.visit))
	defer d.discharger.Close()
	d.discharger.Mux.Handle("/form", http.HandlerFunc(d.form))
	svc, err := bakery.NewService(bakery.NewServiceParams{
		Locator: d.discharger,
	})
	c.Assert(err, gc.IsNil)
	for i, test := range formLoginTests {
		c.Logf("test %d: %s", i, test.about)
		d.dischargeOptions = test.opts
		m, err := svc.NewMacaroon("", nil, []checkers.Caveat{{
			Location:  d.discharger.Location(),
			Condition: "test condition",
		}})
		c.Assert(err, gc.Equals, nil)
		client := httpbakery.NewClient()
		filler := defaultFiller
		if test.filler != nil {
			filler = test.filler
		}
		handlers := []httpbakery.Visitor{
			form.Visitor{
				Filler: filler,
			},
		}
		if test.fallback != nil {
			handlers = append(handlers, test.fallback)
		}
		client.WebPageVisitor = httpbakery.NewMultiVisitor(handlers...)

		ms, err := client.DischargeAll(m)
		if test.expectError != "" {
			c.Assert(err, gc.ErrorMatches, test.expectError)
			continue
		}
		c.Assert(err, gc.IsNil)
		c.Assert(len(ms), gc.Equals, 2)
	}
}

var formTitleTests = []struct {
	host   string
	expect string
}{{
	host:   "xyz.com",
	expect: "Log in to xyz.com",
}, {
	host:   "abc.xyz.com",
	expect: "Log in to xyz.com",
}, {
	host:   "com",
	expect: "Log in to com",
}}

func (s *formSuite) TestFormTitle(c *gc.C) {
	d := &formDischarger{}
	d.discharger = bakerytest.NewInteractiveDischarger(nil, http.HandlerFunc(d.visit))
	defer d.discharger.Close()
	d.discharger.Mux.Handle("/form", http.HandlerFunc(d.form))
	svc, err := bakery.NewService(bakery.NewServiceParams{
		Locator: testLocator{
			loc:     d.discharger.Location(),
			locator: d.discharger,
		},
	})
	c.Assert(err, gc.IsNil)
	for i, test := range formTitleTests {
		c.Logf("test %d: %s", i, test.host)
		m, err := svc.NewMacaroon("", nil, []checkers.Caveat{{
			Location:  "https://" + test.host,
			Condition: "test condition",
		}})
		c.Assert(err, gc.Equals, nil)
		client := httpbakery.NewClient()
		c.Logf("match %v; replace with %v", test.host, d.discharger.Location())
		client.Client.Transport = httptesting.URLRewritingTransport{
			MatchPrefix:  "https://" + test.host,
			Replace:      d.discharger.Location(),
			RoundTripper: http.DefaultTransport,
		}
		var f titleTestFiller
		client.WebPageVisitor = httpbakery.NewMultiVisitor(
			form.Visitor{
				Filler: &f,
			},
		)

		ms, err := client.DischargeAll(m)
		c.Assert(err, gc.IsNil)
		c.Assert(len(ms), gc.Equals, 2)
		c.Assert(f.title, gc.Equals, test.expect)
	}
}

type dischargeOptions struct {
	ignoreAccept    bool
	visitError      bool
	formUnsupported bool
	getError        bool
	postError       bool
	emptySchema     bool
}

type formDischarger struct {
	discharger *bakerytest.InteractiveDischarger
	dischargeOptions
}

func (d *formDischarger) visit(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	if r.Form.Get("fallback") != "" {
		d.discharger.FinishInteraction(w, r, nil, nil)
		return
	}
	if d.ignoreAccept {
		w.Write([]byte("OK"))
		return
	}
	if r.Header.Get("Accept") != "application/json" {
		d.errorf(w, r, "bad accept header %q", r.Header.Get("Accept"))
	}
	if d.visitError {
		httprequest.WriteJSON(w, http.StatusInternalServerError, testError)
		d.discharger.FinishInteraction(w, r, nil, testError)
		return
	}
	methods := map[string]string{
		form.InteractionMethod: d.discharger.HostRelativeURL("/form", r),
		"othermethod":          d.discharger.HostRelativeURL("/visit", r),
	}
	if d.formUnsupported {
		delete(methods, form.InteractionMethod)
	}
	httprequest.WriteJSON(w, http.StatusOK, methods)
}

func (d *formDischarger) form(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		if d.getError {
			httprequest.WriteJSON(w, http.StatusInternalServerError, testError)
			d.discharger.FinishInteraction(w, r, nil, testError)
			return
		}
		var sr form.SchemaResponse
		if !d.emptySchema {
			sr.Schema = environschema.Fields{
				"username": environschema.Attr{
					Type: environschema.Tstring,
				},
				"password": environschema.Attr{
					Type:   environschema.Tstring,
					Secret: true,
				},
			}
		}
		httprequest.WriteJSON(w, http.StatusOK, sr)
		return
	}
	if r.Method != "POST" {
		d.errorf(w, r, "bad method %q", r.Method)
		return
	}
	if d.postError {
		httprequest.WriteJSON(w, http.StatusInternalServerError, testError)
		d.discharger.FinishInteraction(w, r, nil, testError)
		return
	}
	var lr form.LoginRequest
	err := httprequest.Unmarshal(httprequest.Params{Request: r}, &lr)
	if err != nil {
		d.errorf(w, r, "bad visit request: %s", err)
		return
	}
	d.discharger.FinishInteraction(w, r, nil, nil)
}

func (d *formDischarger) errorf(w http.ResponseWriter, r *http.Request, s string, p ...interface{}) {
	err := &httpbakery.Error{
		Code:    httpbakery.ErrBadRequest,
		Message: fmt.Sprintf(s, p...),
	}
	d.discharger.FinishInteraction(w, r, nil, err)
}

var testError = &httpbakery.Error{
	Message: "test error",
}

type fillerFunc func(esform.Form) (map[string]interface{}, error)

func (f fillerFunc) Fill(form esform.Form) (map[string]interface{}, error) {
	return f(form)
}

var defaultFiller = fillerFunc(func(esform.Form) (map[string]interface{}, error) {
	return map[string]interface{}{"test": 1}, nil
})

type testLocator struct {
	loc     string
	locator bakery.PublicKeyLocator
}

func (l testLocator) PublicKeyForLocation(loc string) (*bakery.PublicKey, error) {
	return l.locator.PublicKeyForLocation(l.loc)
}

type titleTestFiller struct {
	title string
}

func (f *titleTestFiller) Fill(form esform.Form) (map[string]interface{}, error) {
	f.title = form.Title
	return map[string]interface{}{"test": 1}, nil
}
