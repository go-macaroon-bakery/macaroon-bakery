package form_test

import (
	"net/http"

	jujutesting "github.com/juju/testing"
	"github.com/juju/testing/httptesting"
	"golang.org/x/net/context"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/httprequest.v1"
	"gopkg.in/juju/environschema.v1"
	esform "gopkg.in/juju/environschema.v1/form"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2/bakery/identchecker"
	"gopkg.in/macaroon-bakery.v2/bakerytest"
	"gopkg.in/macaroon-bakery.v2/httpbakery"
	"gopkg.in/macaroon-bakery.v2/httpbakery/form"
)

type formSuite struct {
	jujutesting.LoggingSuite
}

var reqServer = httprequest.Server{
	ErrorMapper: httpbakery.ErrorToResponse,
}

var _ = gc.Suite(&formSuite{})

var formLoginTests = []struct {
	about        string
	filler       fillerFunc
	expectError  string
	noFormMethod bool
	getForm      func() (environschema.Fields, error)
	postForm     func(values map[string]interface{}) (*httpbakery.DischargeToken, error)
}{{
	about: "complete visit",
	getForm: func() (environschema.Fields, error) {
		return userPassForm, nil
	},
	postForm: func(values map[string]interface{}) (*httpbakery.DischargeToken, error) {
		return &httpbakery.DischargeToken{
			Kind:  "form",
			Value: []byte("ok"),
		}, nil
	},
}, {
	about: "error getting schema",
	getForm: func() (environschema.Fields, error) {
		return nil, errgo.Newf("some error")
	},
	expectError: `cannot get discharge from ".*": cannot get schema: Get https://.*/form: some error`,
}, {
	about:        "form visit method not supported",
	noFormMethod: true,
	expectError:  `cannot get discharge from ".*": cannot start interactive session: no supported interaction method`,
}, {
	about: "error submitting form",
	getForm: func() (environschema.Fields, error) {
		return userPassForm, nil
	},
	postForm: func(values map[string]interface{}) (*httpbakery.DischargeToken, error) {
		return nil, errgo.Newf("some error")
	},
	expectError: `cannot get discharge from ".*": cannot submit form.*: some error`,
}, {
	about: "no schema",
	getForm: func() (environschema.Fields, error) {
		return nil, nil
	},
	expectError: `cannot get discharge from ".*": invalid schema: no fields found`,
}, {
	about: "filler error",
	getForm: func() (environschema.Fields, error) {
		return userPassForm, nil
	},
	filler: func(esform.Form) (map[string]interface{}, error) {
		return nil, errgo.Newf("test error")
	},
	expectError: `cannot get discharge from ".*": cannot handle form: test error`,
}, {
	about: "invalid token returned from form submission",
	getForm: func() (environschema.Fields, error) {
		return userPassForm, nil
	},
	postForm: func(values map[string]interface{}) (*httpbakery.DischargeToken, error) {
		return &httpbakery.DischargeToken{
			Kind:  "other",
			Value: []byte("something"),
		}, nil
	},
	expectError: `cannot get discharge from ".*": Post .*: cannot discharge: invalid token .*`,
}}

func (s *formSuite) TestFormLogin(c *gc.C) {
	var (
		getForm      func() (environschema.Fields, error)
		postForm     func(values map[string]interface{}) (*httpbakery.DischargeToken, error)
		noFormMethod bool
	)

	discharger := bakerytest.NewDischarger(nil)
	defer discharger.Close()
	discharger.AddHTTPHandlers(FormHandlers(FormHandler{
		getForm: func() (environschema.Fields, error) {
			return getForm()
		},
		postForm: func(values map[string]interface{}) (*httpbakery.DischargeToken, error) {
			return postForm(values)
		},
	}))
	discharger.Checker = httpbakery.ThirdPartyCaveatCheckerFunc(func(ctx context.Context, req *http.Request, info *bakery.ThirdPartyCaveatInfo, token *httpbakery.DischargeToken) ([]checkers.Caveat, error) {
		if token != nil {
			if token.Kind != "form" || string(token.Value) != "ok" {
				return nil, errgo.Newf("invalid token %#v", token)
			}
			return nil, nil
		}
		err := httpbakery.NewInteractionRequiredError(nil, req)
		if noFormMethod {
			err.SetInteraction("notform", "value")
		} else {
			err.SetInteraction("form", form.InteractionInfo{
				URL: "/form",
			})
		}
		return nil, err
	})
	b := bakery.New(bakery.BakeryParams{
		Key:     bakery.MustGenerateKey(),
		Locator: discharger,
	})
	for i, test := range formLoginTests {
		c.Logf("\ntest %d: %s", i, test.about)
		getForm = test.getForm
		postForm = test.postForm
		noFormMethod = test.noFormMethod

		m, err := b.Oven.NewMacaroon(context.TODO(), bakery.LatestVersion, []checkers.Caveat{{
			Location:  discharger.Location(),
			Condition: "test condition",
		}}, identchecker.LoginOp)
		c.Assert(err, gc.Equals, nil)

		client := httpbakery.NewClient()
		filler := defaultFiller
		if test.filler != nil {
			filler = test.filler
		}
		client.AddInteractor(form.Interactor{
			Filler: filler,
		})
		ms, err := client.DischargeAll(context.Background(), m)
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
	discharger := bakerytest.NewDischarger(nil)
	defer discharger.Close()
	discharger.AddHTTPHandlers(FormHandlers(FormHandler{
		getForm: func() (environschema.Fields, error) {
			return userPassForm, nil
		},
		postForm: func(values map[string]interface{}) (*httpbakery.DischargeToken, error) {
			return &httpbakery.DischargeToken{
				Kind:  "form",
				Value: []byte("ok"),
			}, nil
		},
	}))
	discharger.Checker = httpbakery.ThirdPartyCaveatCheckerFunc(func(ctx context.Context, req *http.Request, info *bakery.ThirdPartyCaveatInfo, token *httpbakery.DischargeToken) ([]checkers.Caveat, error) {
		if token != nil {
			return nil, nil
		}
		err := httpbakery.NewInteractionRequiredError(nil, req)
		err.SetInteraction("form", form.InteractionInfo{
			URL: "/form",
		})
		return nil, err
	})
	b := identchecker.NewBakery(identchecker.BakeryParams{
		Key: bakery.MustGenerateKey(),
		Locator: testLocator{
			loc:     discharger.Location(),
			locator: discharger,
		},
	})
	for i, test := range formTitleTests {
		c.Logf("test %d: %s", i, test.host)
		m, err := b.Oven.NewMacaroon(context.TODO(), bakery.LatestVersion, []checkers.Caveat{{
			Location:  "https://" + test.host,
			Condition: "test condition",
		}}, identchecker.LoginOp)
		c.Assert(err, gc.Equals, nil)
		client := httpbakery.NewClient()
		c.Logf("match %v; replace with %v", test.host, discharger.Location())
		client.Client.Transport = httptesting.URLRewritingTransport{
			MatchPrefix:  "https://" + test.host,
			Replace:      discharger.Location(),
			RoundTripper: http.DefaultTransport,
		}
		var f titleTestFiller
		client.AddInteractor(form.Interactor{
			Filler: &f,
		})

		ms, err := client.DischargeAll(context.Background(), m)
		c.Assert(err, gc.IsNil)
		c.Assert(len(ms), gc.Equals, 2)
		c.Assert(f.title, gc.Equals, test.expect)
	}
}

func FormHandlers(h FormHandler) []httprequest.Handler {
	return reqServer.Handlers(func(p httprequest.Params) (formHandlers, context.Context, error) {
		return formHandlers{h}, p.Context, nil
	})
}

type FormHandler struct {
	getForm  func() (environschema.Fields, error)
	postForm func(values map[string]interface{}) (*httpbakery.DischargeToken, error)
}

type formHandlers struct {
	h FormHandler
}

type schemaRequest struct {
	httprequest.Route `httprequest:"GET /form"`
}

func (d formHandlers) GetForm(*schemaRequest) (*form.SchemaResponse, error) {
	schema, err := d.h.getForm()
	if err != nil {
		return nil, errgo.Mask(err)
	}
	return &form.SchemaResponse{schema}, nil
}

type loginRequest struct {
	httprequest.Route `httprequest:"POST /form"`
	form.LoginRequest
}

func (d formHandlers) PostForm(req *loginRequest) (*form.LoginResponse, error) {
	token, err := d.h.postForm(req.Body.Form)
	if err != nil {
		return nil, errgo.Mask(err)
	}
	return &form.LoginResponse{
		Token: token,
	}, nil
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
	locator bakery.ThirdPartyLocator
}

func (l testLocator) ThirdPartyInfo(ctx context.Context, loc string) (bakery.ThirdPartyInfo, error) {
	return l.locator.ThirdPartyInfo(ctx, l.loc)
}

type titleTestFiller struct {
	title string
}

func (f *titleTestFiller) Fill(form esform.Form) (map[string]interface{}, error) {
	f.title = form.Title
	return map[string]interface{}{"test": 1}, nil
}

var userPassForm = environschema.Fields{
	"username": environschema.Attr{
		Type: environschema.Tstring,
	},
	"password": environschema.Attr{
		Type:   environschema.Tstring,
		Secret: true,
	},
}
