package bakerytest_test

import (
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	jujutesting "github.com/juju/testing"
	"github.com/julienschmidt/httprouter"
	"golang.org/x/net/context"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"
	"gopkg.in/httprequest.v1"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2/bakerytest"
	"gopkg.in/macaroon-bakery.v2/httpbakery"
)

type suite struct {
	jujutesting.LoggingSuite
	client *httpbakery.Client
}

func (s *suite) SetUpTest(c *gc.C) {
	s.LoggingSuite.SetUpTest(c)
	s.client = httpbakery.NewClient()
}

var _ = gc.Suite(&suite{})

var dischargeOp = bakery.Op{"thirdparty", "x"}

func (s *suite) TestDischargerSimple(c *gc.C) {
	d := bakerytest.NewDischarger(nil)
	defer d.Close()

	b := bakery.New(bakery.BakeryParams{
		Location: "here",
		Locator:  d,
		Key:      bakery.MustGenerateKey(),
	})
	m, err := b.Oven.NewMacaroon(context.Background(), bakery.LatestVersion, []checkers.Caveat{{
		Location:  d.Location(),
		Condition: "something",
	}}, dischargeOp)

	c.Assert(err, gc.IsNil)
	ms, err := s.client.DischargeAll(context.Background(), m)
	c.Assert(err, gc.IsNil)
	c.Assert(ms, gc.HasLen, 2)

	_, err = b.Checker.Auth(ms).Allow(context.Background(), dischargeOp)
	c.Assert(err, gc.IsNil)
}

func (s *suite) TestDischargerTwoLevels(c *gc.C) {
	d1checker := func(cond, arg string) ([]checkers.Caveat, error) {
		if cond != "xtrue" {
			return nil, fmt.Errorf("caveat refused")
		}
		return nil, nil
	}
	d1 := bakerytest.NewDischarger(nil)
	d1.CheckerP = bakerytest.ConditionParser(d1checker)
	defer d1.Close()
	d2checker := func(cond, arg string) ([]checkers.Caveat, error) {
		return []checkers.Caveat{{
			Location:  d1.Location(),
			Condition: "x" + cond,
		}}, nil
	}
	d2 := bakerytest.NewDischarger(d1)
	d2.CheckerP = bakerytest.ConditionParser(d2checker)
	defer d2.Close()
	locator := bakery.NewThirdPartyStore()
	locator.AddInfo(d1.Location(), bakery.ThirdPartyInfo{
		PublicKey: d1.Key.Public,
		Version:   bakery.LatestVersion,
	})
	locator.AddInfo(d2.Location(), bakery.ThirdPartyInfo{
		PublicKey: d2.Key.Public,
		Version:   bakery.LatestVersion,
	})
	b := bakery.New(bakery.BakeryParams{
		Location: "here",
		Locator:  locator,
		Key:      bakery.MustGenerateKey(),
	})
	m, err := b.Oven.NewMacaroon(context.Background(), bakery.LatestVersion, []checkers.Caveat{{
		Location:  d2.Location(),
		Condition: "true",
	}}, dischargeOp)

	c.Assert(err, gc.IsNil)

	ms, err := s.client.DischargeAll(context.Background(), m)
	c.Assert(err, gc.IsNil)
	c.Assert(ms, gc.HasLen, 3)

	_, err = b.Checker.Auth(ms).Allow(context.Background(), dischargeOp)
	c.Assert(err, gc.IsNil)

	err = b.Oven.AddCaveat(context.Background(), m, checkers.Caveat{
		Location:  d2.Location(),
		Condition: "nope",
	})
	c.Assert(err, gc.IsNil)

	ms, err = s.client.DischargeAll(context.Background(), m)
	c.Assert(err, gc.ErrorMatches, `cannot get discharge from "https://[^"]*": third party refused discharge: cannot discharge: caveat refused`)
	c.Assert(ms, gc.HasLen, 0)
}

func (s *suite) TestInsecureSkipVerifyRestoration(c *gc.C) {
	d1 := bakerytest.NewDischarger(nil)
	d2 := bakerytest.NewDischarger(nil)
	d2.Close()
	c.Assert(http.DefaultTransport.(*http.Transport).TLSClientConfig.InsecureSkipVerify, gc.Equals, true)
	d1.Close()
	c.Assert(http.DefaultTransport.(*http.Transport).TLSClientConfig.InsecureSkipVerify, gc.Equals, false)

	// When InsecureSkipVerify is already true, it should not
	// be restored to false.
	http.DefaultTransport.(*http.Transport).TLSClientConfig.InsecureSkipVerify = true
	d3 := bakerytest.NewDischarger(nil)
	d3.Close()

	c.Assert(http.DefaultTransport.(*http.Transport).TLSClientConfig.InsecureSkipVerify, gc.Equals, true)
}

func (s *suite) TestConcurrentDischargers(c *gc.C) {
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			d := bakerytest.NewDischarger(nil)
			d.Close()
			wg.Done()
		}()
	}
	wg.Wait()
	c.Assert(http.DefaultTransport.(*http.Transport).TLSClientConfig.InsecureSkipVerify, gc.Equals, false)
}

func (s *suite) TestWithGlobalAllowInsecure(c *gc.C) {
	httpbakery.AllowInsecureThirdPartyLocator = true
	defer func() {
		httpbakery.AllowInsecureThirdPartyLocator = false
	}()
	s.TestDischargerSimple(c)
}

func (s *suite) TestInteractiveDischarger(c *gc.C) {
	d := bakerytest.NewDischarger(nil)
	defer d.Close()

	rendezvous := bakerytest.NewRendezvous()
	visited := false
	waited := false
	d.AddHTTPHandlers(VisitWaitHandlers(VisitWaiter{
		Visit: func(p httprequest.Params, dischargeId string) error {
			visited = true
			rendezvous.DischargeComplete(dischargeId, []checkers.Caveat{{
				Condition: "test pass",
			}})
			return nil
		},
		WaitToken: func(p httprequest.Params, dischargeId string) (*httpbakery.DischargeToken, error) {
			waited = true
			_, err := rendezvous.Await(dischargeId, 5*time.Second)
			if err != nil {
				return nil, errgo.Mask(err)
			}
			return rendezvous.DischargeToken(dischargeId), nil
		},
	}))

	d.Checker = httpbakery.ThirdPartyCaveatCheckerFunc(func(ctx context.Context, req *http.Request, cav *bakery.ThirdPartyCaveatInfo, token *httpbakery.DischargeToken) ([]checkers.Caveat, error) {
		if string(cav.Condition) != "something" {
			return nil, errgo.Newf("wrong condition")
		}
		if token != nil {
			return rendezvous.CheckToken(token, cav)
		}
		err := NewVisitWaitError(req, rendezvous.NewDischarge(cav))
		return nil, errgo.Mask(err, errgo.Any)
	})

	var r recordingChecker
	b := bakery.New(bakery.BakeryParams{
		Location: "here",
		Locator:  d,
		Checker:  &r,
		Key:      bakery.MustGenerateKey(),
	})
	m, err := b.Oven.NewMacaroon(context.Background(), bakery.LatestVersion, []checkers.Caveat{{
		Location:  d.Location(),
		Condition: "something",
	}}, dischargeOp)

	c.Assert(err, gc.IsNil)
	client := httpbakery.NewClient()
	client.AddInteractor(newTestInteractor())
	ms, err := client.DischargeAll(context.Background(), m)
	c.Assert(err, gc.IsNil)
	c.Assert(ms, gc.HasLen, 2)

	_, err = b.Checker.Auth(ms).Allow(context.Background(), dischargeOp)
	c.Assert(err, gc.IsNil)
	// First caveat is time-before caveat added by NewMacaroon.
	// Second is the one added by the discharger above.
	c.Assert(r.caveats, gc.HasLen, 1)
	c.Assert(r.caveats[0], gc.Equals, "test pass")

	c.Check(visited, gc.Equals, true)
	c.Check(waited, gc.Equals, true)
}

func (s *suite) TestLoginDischargerError(c *gc.C) {
	d := bakerytest.NewDischarger(nil)
	defer d.Close()

	rendezvous := bakerytest.NewRendezvous()
	d.AddHTTPHandlers(VisitWaitHandlers(VisitWaiter{
		Visit: func(p httprequest.Params, dischargeId string) error {
			rendezvous.DischargeFailed(dischargeId, errgo.Newf("test error"))
			return nil
		},
		WaitToken: func(p httprequest.Params, dischargeId string) (*httpbakery.DischargeToken, error) {
			_, err := rendezvous.Await(dischargeId, 5*time.Second)
			if err != nil {
				return nil, errgo.Mask(err)
			}
			return nil, errgo.Newf("await succeeded unexpectedly")
		},
	}))
	d.Checker = httpbakery.ThirdPartyCaveatCheckerFunc(func(ctx context.Context, req *http.Request, cav *bakery.ThirdPartyCaveatInfo, token *httpbakery.DischargeToken) ([]checkers.Caveat, error) {
		if string(cav.Condition) != "something" {
			return nil, errgo.Newf("wrong condition")
		}
		if token != nil {
			return nil, errgo.Newf("token received unexpectedly")
		}
		err := NewVisitWaitError(req, rendezvous.NewDischarge(cav))
		return nil, errgo.Mask(err, errgo.Any)
	})

	b := bakery.New(bakery.BakeryParams{
		Location: "here",
		Locator:  d,
		Key:      bakery.MustGenerateKey(),
	})
	m, err := b.Oven.NewMacaroon(context.Background(), bakery.LatestVersion, []checkers.Caveat{{
		Location:  d.Location(),
		Condition: "something",
	}}, dischargeOp)

	c.Assert(err, gc.IsNil)
	client := httpbakery.NewClient()
	client.AddInteractor(newTestInteractor())
	_, err = client.DischargeAll(context.Background(), m)
	c.Assert(err, gc.ErrorMatches, `cannot get discharge from ".*": cannot acquire discharge token: test error`)
}

func (s *suite) TestInteractiveDischargerRedirection(c *gc.C) {
	d := bakerytest.NewDischarger(nil)
	defer d.Close()
	rendezvous := bakerytest.NewRendezvous()
	d.AddHTTPHandlers(VisitWaitHandlers(VisitWaiter{
		Visit: func(p httprequest.Params, dischargeId string) error {
			http.Redirect(p.Response, p.Request,
				d.Location()+"/redirect?dischargeid="+dischargeId,
				http.StatusFound,
			)
			return nil
		},
		WaitToken: func(p httprequest.Params, dischargeId string) (*httpbakery.DischargeToken, error) {
			_, err := rendezvous.Await(dischargeId, 5*time.Second)
			if err != nil {
				return nil, errgo.Mask(err)
			}
			return rendezvous.DischargeToken(dischargeId), nil
		},
	}))
	d.Mux.GET("/redirect", func(w http.ResponseWriter, req *http.Request, p httprouter.Params) {
		req.ParseForm()
		rendezvous.DischargeComplete(req.Form.Get("dischargeid"), []checkers.Caveat{{
			Condition: "condition",
		}})
	})
	d.Checker = httpbakery.ThirdPartyCaveatCheckerFunc(func(ctx context.Context, req *http.Request, cav *bakery.ThirdPartyCaveatInfo, token *httpbakery.DischargeToken) ([]checkers.Caveat, error) {
		if string(cav.Condition) != "something" {
			return nil, errgo.Newf("wrong condition")
		}
		if token != nil {
			return rendezvous.CheckToken(token, cav)
		}
		err := NewVisitWaitError(req, rendezvous.NewDischarge(cav))
		return nil, errgo.Mask(err, errgo.Any)
	})
	var r recordingChecker
	b := bakery.New(bakery.BakeryParams{
		Location: "here",
		Locator:  d,
		Key:      bakery.MustGenerateKey(),
		Checker:  &r,
	})
	m, err := b.Oven.NewMacaroon(context.Background(), bakery.LatestVersion, []checkers.Caveat{{
		Location:  d.Location(),
		Condition: "something",
	}}, dischargeOp)

	c.Assert(err, gc.IsNil)
	client := httpbakery.NewClient()
	client.AddInteractor(newTestInteractor())

	ms, err := client.DischargeAll(context.Background(), m)
	c.Assert(err, gc.IsNil)
	c.Assert(ms, gc.HasLen, 2)

	_, err = b.Checker.Auth(ms).Allow(context.Background(), dischargeOp)
	c.Assert(err, gc.IsNil)

	c.Assert(r.caveats, gc.DeepEquals, []string{"condition"})
}

type recordingChecker struct {
	caveats []string
}

func (c *recordingChecker) CheckFirstPartyCaveat(ctx context.Context, caveat string) error {
	c.caveats = append(c.caveats, caveat)
	return nil
}

func (c *recordingChecker) Namespace() *checkers.Namespace {
	return nil
}

func newTestInteractor() httpbakery.WebBrowserInteractor {
	return httpbakery.WebBrowserInteractor{
		OpenWebBrowser: func(u *url.URL) error {
			resp, err := http.Get(u.String())
			if err != nil {
				return errgo.Mask(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return errgo.Newf("unexpected status %q", resp.Status)
			}
			return nil
		},
	}
}

// NewVisitWaitError returns a new interaction-required error
// that
func NewVisitWaitError(req *http.Request, dischargeId string) *httpbakery.Error {
	err := httpbakery.NewInteractionRequiredError(nil, req)
	visitURL := "/visit?dischargeid=" + dischargeId
	httpbakery.SetWebBrowserInteraction(err, visitURL, "/wait-token?dischargeid="+dischargeId)
	httpbakery.SetLegacyInteraction(err, visitURL, "/wait?dischargeid="+dischargeId)
	return err
}

// VisitWaiter represents a handler for visit-wait interactions.
// Each member corresponds to an HTTP endpoint,
type VisitWaiter struct {
	Visit     func(p httprequest.Params, dischargeId string) error
	Wait      func(p httprequest.Params, dischargeId string) (*bakery.Macaroon, error)
	WaitToken func(p httprequest.Params, dischargeId string) (*httpbakery.DischargeToken, error)
}

var reqServer = httprequest.Server{
	ErrorMapper: httpbakery.ErrorToResponse,
}

func VisitWaitHandlers(vw VisitWaiter) []httprequest.Handler {
	return reqServer.Handlers(func(p httprequest.Params) (visitWaitHandlers, context.Context, error) {
		return visitWaitHandlers{vw}, p.Context, nil
	})
}

type visitWaitHandlers struct {
	vw VisitWaiter
}

type visitRequest struct {
	httprequest.Route `httprequest:"GET /visit"`
	DischargeId       string `httprequest:"dischargeid,form"`
}

func (h visitWaitHandlers) Visit(p httprequest.Params, r *visitRequest) error {
	if h.vw.Visit == nil {
		return errgo.Newf("visit not implemented")
	}
	return h.vw.Visit(p, r.DischargeId)
}

type waitTokenRequest struct {
	httprequest.Route `httprequest:"GET /wait-token"`
	DischargeId       string `httprequest:"dischargeid,form"`
}

func (h visitWaitHandlers) WaitToken(p httprequest.Params, r *waitTokenRequest) (*httpbakery.WaitTokenResponse, error) {
	if h.vw.WaitToken == nil {
		return nil, errgo.Newf("wait-token not implemented")
	}
	token, err := h.vw.WaitToken(p, r.DischargeId)
	if err != nil {
		return nil, errgo.Mask(err, errgo.Any)
	}
	return &httpbakery.WaitTokenResponse{
		Kind:  token.Kind,
		Token: string(token.Value),
	}, nil
}

type waitRequest struct {
	httprequest.Route `httprequest:"GET /wait"`
	DischargeId       string `httprequest:"dischargeid,form"`
}

func (h visitWaitHandlers) Wait(p httprequest.Params, r *waitRequest) (*httpbakery.WaitResponse, error) {
	if h.vw.Wait == nil {
		return nil, errgo.Newf("wait not implemented")
	}
	m, err := h.vw.Wait(p, r.DischargeId)
	if err != nil {
		return nil, errgo.Mask(err, errgo.Any)
	}
	return &httpbakery.WaitResponse{
		Macaroon: m,
	}, nil
}
