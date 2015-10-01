package form_test

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/juju/httprequest"
	gc "gopkg.in/check.v1"
	"gopkg.in/juju/environschema.v1"

	"gopkg.in/macaroon-bakery.v1/bakery"
	"gopkg.in/macaroon-bakery.v1/bakery/checkers"
	"gopkg.in/macaroon-bakery.v1/bakerytest"
	"gopkg.in/macaroon-bakery.v1/httpbakery"
	"gopkg.in/macaroon-bakery.v1/httpbakery/form"
)

type formSuite struct{}

var _ = gc.Suite(&formSuite{})

var formLoginTests = []struct {
	about       string
	opts        dischargeOptions
	filler      fillerFunc
	fallback    func(*url.URL) error
	expectError string
}{{
	about: "complete login",
}, {
	about: "login error",
	opts: dischargeOptions{
		loginError: true,
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: cannot get login methods: GET .*: httprequest: test error`,
}, {
	about: "login methods not supported",
	opts: dischargeOptions{
		ignoreAccept: true,
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: cannot get login methods: GET .*: unexpected content type text/plain; want application/json; content: OK`,
}, {
	about: "form login method not supported",
	opts: dischargeOptions{
		formUnsupported: true,
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: form login not supported`,
}, {
	about: "error getting schema",
	opts: dischargeOptions{
		getError: true,
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: cannot get schema: GET .*: httprequest: test error`,
}, {
	about: "error submitting form",
	opts: dischargeOptions{
		postError: true,
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: cannot submit form: POST .*: httprequest: test error`,
}, {
	about: "no schema",
	opts: dischargeOptions{
		emptySchema: true,
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: invalid schema: no fields found`,
}, {
	about: "filler error",
	filler: func(environschema.Fields) (map[string]interface{}, error) {
		return nil, testError
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: cannot handle form: test error`,
}, {
	about: "login methods fallback success",
	opts: dischargeOptions{
		ignoreAccept: true,
	},
	fallback: func(u *url.URL) error {
		resp, err := http.Get(u.String() + "&fallback=OK")
		if err == nil {
			resp.Body.Close()
		}
		return err
	},
}, {
	about: "login methods fallback failure",
	opts: dischargeOptions{
		ignoreAccept: true,
	},
	fallback: func(u *url.URL) error {
		return testError
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: test error`,
}, {
	about: "form not suppoorted fallback success",
	opts: dischargeOptions{
		formUnsupported: true,
	},
	fallback: func(u *url.URL) error {
		resp, err := http.Get(u.String() + "&fallback=OK")
		if err == nil {
			resp.Body.Close()
		}
		return err
	},
}, {
	about: "form not supported fallback failure",
	opts: dischargeOptions{
		formUnsupported: true,
	},
	fallback: func(u *url.URL) error {
		return testError
	},
	expectError: `cannot get discharge from ".*": cannot start interactive session: test error`,
}}

func (s *formSuite) TestFormLogin(c *gc.C) {
	d := &formDischarger{}
	d.discharger = bakerytest.NewInteractiveDischarger(nil, http.HandlerFunc(d.login))
	defer d.discharger.Close()
	d.discharger.Mux.Handle("/form", http.HandlerFunc(d.form))
	svc, err := bakery.NewService(bakery.NewServiceParams{
		Locator: d.discharger,
	})
	c.Assert(err, gc.IsNil)
	for i, test := range formLoginTests {
		c.Logf("%d. %s", i, test.about)
		d.dischargeOptions = test.opts
		m, err := svc.NewMacaroon("", nil, []checkers.Caveat{{
			Location:  d.discharger.Location(),
			Condition: "test condition",
		}})
		c.Assert(err, gc.Equals, nil)
		client := httpbakery.NewClient()
		h := defaultFiller
		if test.filler != nil {
			h = test.filler
		}
		client.VisitWebPage = test.fallback
		form.SetUpAuth(client, h)

		ms, err := client.DischargeAll(m)
		if test.expectError != "" {
			c.Assert(err, gc.ErrorMatches, test.expectError)
			continue
		}
		c.Assert(err, gc.IsNil)
		c.Assert(len(ms), gc.Equals, 2)
	}
}

func (s *formSuite) TestFormLoginNewRequestError(c *gc.C) {
	client := httpbakery.NewClient()
	form.SetUpAuth(client, defaultFiller)
	u := url.URL{
		Scheme: ":",
	}
	err := client.VisitWebPage(&u)
	c.Assert(err, gc.ErrorMatches, "cannot create request: parse :://: missing protocol scheme")
}

type dischargeOptions struct {
	ignoreAccept    bool
	loginError      bool
	formUnsupported bool
	getError        bool
	postError       bool
	emptySchema     bool
}

type formDischarger struct {
	discharger *bakerytest.InteractiveDischarger
	dischargeOptions
}

func (d *formDischarger) login(w http.ResponseWriter, r *http.Request) {
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
	if d.loginError {
		httprequest.WriteJSON(w, http.StatusInternalServerError, testError)
		d.discharger.FinishInteraction(w, r, nil, testError)
		return
	}
	methods := map[string]string{}
	if !d.formUnsupported {
		r.ParseForm()
		methods["form"] = d.discharger.URL("/form", r)
	}
	httprequest.WriteJSON(w, http.StatusOK, methods)
}

func (d *formDischarger) form(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	if r.Form.Get("waitid") == "" {
		d.errorf(w, r, "no waitid")
		return
	}
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
		d.errorf(w, r, "bad login request: %s", err)
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

type fillerFunc func(environschema.Fields) (map[string]interface{}, error)

func (h fillerFunc) Fill(f environschema.Fields) (map[string]interface{}, error) {
	return h(f)
}

var defaultFiller = fillerFunc(func(environschema.Fields) (map[string]interface{}, error) {
	return map[string]interface{}{"test": 1}, nil
})
