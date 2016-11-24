package agent_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"time"

	"golang.org/x/net/context"
	gc "gopkg.in/check.v1"
	"gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery/agent"
)

var ages = time.Now().Add(time.Hour)

type discharge struct {
	caveatInfo *bakery.ThirdPartyCaveatInfo
	c          chan bakery.Identity
}

var allCheckers = httpbakery.NewChecker()

type Discharger struct {
	*httptest.Server
	key          *bakery.KeyPair
	bakery       *bakery.Bakery
	LoginHandler func(*Discharger, http.ResponseWriter, *http.Request)

	mu      sync.Mutex
	waiting []discharge
}

func newDischarger(c *gc.C, locator *bakery.ThirdPartyStore) *Discharger {
	key, err := bakery.GenerateKey()
	c.Assert(err, gc.IsNil)
	d := &Discharger{
		key: key,
	}
	d.Server = httptest.NewServer(d.handler())

	d.bakery = bakery.New(bakery.BakeryParams{
		Key:            key,
		IdentityClient: idmClient{d.URL},
	})
	locator.AddInfo(d.URL, bakery.ThirdPartyInfo{
		PublicKey: key.Public,
		Version:   bakery.LatestVersion,
	})
	return d
}

func (d *Discharger) handler() http.Handler {
	mux := http.NewServeMux()
	discharger := httpbakery.NewDischarger(httpbakery.DischargerParams{
		Key:     d.key,
		Checker: d,
	})
	discharger.AddMuxHandlers(mux, "/")
	mux.Handle("/login", http.HandlerFunc(d.login))
	mux.Handle("/wait", http.HandlerFunc(d.wait))
	mux.Handle("/", http.HandlerFunc(d.notFound))
	return mux
}

func (d *Discharger) writeJSON(w http.ResponseWriter, status int, v interface{}) error {
	body, err := json.Marshal(v)
	if err != nil {
		return errgo.Notef(err, "cannot marshal v")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		return errgo.Notef(err, "cannot write response")
	}
	return nil
}

func (d *Discharger) GetAgentLogin(r *http.Request) (*agent.AgentLogin, error) {
	c, err := r.Cookie("agent-login")
	if err != nil {
		return nil, errgo.Notef(err, "cannot find cookie")
	}
	b, err := base64.StdEncoding.DecodeString(c.Value)
	if err != nil {
		return nil, errgo.Notef(err, "cannot decode cookie")
	}
	var al agent.AgentLogin
	if err := json.Unmarshal(b, &al); err != nil {
		return nil, errgo.Notef(err, "cannot unmarshal cookie")
	}
	return &al, nil
}

func (d *Discharger) finishWait(w http.ResponseWriter, r *http.Request, identity bakery.Identity) {
	r.ParseForm()
	id, err := strconv.Atoi(r.Form.Get("waitid"))
	if err != nil {
		d.writeJSON(w, http.StatusBadRequest, httpbakery.Error{
			Message: fmt.Sprintf("cannot read waitid: %s", err),
		})
		return
	}
	d.waiting[id].c <- identity
	return
}

func (d *Discharger) CheckThirdPartyCaveat(ctxt context.Context, req *http.Request, ci *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
	d.mu.Lock()
	id := len(d.waiting)
	d.waiting = append(d.waiting, discharge{
		caveatInfo: ci,
		c:          make(chan bakery.Identity, 1),
	})
	d.mu.Unlock()
	return nil, &httpbakery.Error{
		Code:    httpbakery.ErrInteractionRequired,
		Message: "test interaction",
		Info: &httpbakery.ErrorInfo{
			VisitURL: fmt.Sprintf("%s/login?waitid=%d", d.URL, id),
			WaitURL:  fmt.Sprintf("%s/wait?waitid=%d", d.URL, id),
		},
	}
}

func (d *Discharger) login(w http.ResponseWriter, r *http.Request) {
	// TODO take context from request.
	ctxt := httpbakery.ContextWithRequest(context.TODO(), r)
	r.ParseForm()
	if d.LoginHandler != nil {
		d.LoginHandler(d, w, r)
		return
	}
	username, userPublicKey, err := agent.LoginCookie(r)
	if err != nil {
		d.writeJSON(w, http.StatusBadRequest, httpbakery.Error{
			Message: fmt.Sprintf("cannot read agent login: %s", err),
		})
		return
	}
	authInfo, authErr := d.bakery.Checker.Auth(httpbakery.RequestMacaroons(r)...).Allow(ctxt, bakery.LoginOp)
	if authErr == nil {
		d.finishWait(w, r, authInfo.Identity)
		d.writeJSON(w, http.StatusOK, agent.AgentResponse{
			AgentLogin: true,
		})
		return
	}
	version := httpbakery.RequestVersion(r)
	m, err := d.bakery.Oven.NewMacaroon(ctxt, httpbakery.RequestVersion(r), ages, []checkers.Caveat{
		bakery.LocalThirdPartyCaveat(userPublicKey, version),
		checkers.DeclaredCaveat("username", username),
	}, bakery.LoginOp)

	if err != nil {
		d.writeJSON(w, http.StatusInternalServerError, httpbakery.Error{
			Message: fmt.Sprintf("cannot create macaroon: %s", err),
		})
		return
	}
	httpbakery.WriteDischargeRequiredError(w, m, "", authErr)
}

func (d *Discharger) wait(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	id, err := strconv.Atoi(r.Form.Get("waitid"))
	if err != nil {
		d.writeJSON(w, http.StatusBadRequest, httpbakery.Error{
			Message: fmt.Sprintf("cannot read waitid: %s", err),
		})
		return
	}
	identity := <-d.waiting[id].c
	if identity == nil {
		d.writeJSON(w, http.StatusForbidden, fmt.Errorf("login failed"))
		return
	}
	ci := d.waiting[id].caveatInfo
	m, err := bakery.Discharge(context.TODO(), bakery.DischargeParams{
		Id:     ci.Id,
		Caveat: ci.Caveat,
		Key:    d.bakery.Oven.Key(),
		Checker: bakery.ThirdPartyCaveatCheckerFunc(
			func(context.Context, *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
				return []checkers.Caveat{
					checkers.DeclaredCaveat("username", string(identity.(simpleIdentity))),
				}, nil
			},
		),
	})
	if err != nil {
		d.writeJSON(w, http.StatusForbidden, err)
		return
	}
	d.writeJSON(
		w,
		http.StatusOK,
		struct {
			Macaroon *bakery.Macaroon
		}{
			Macaroon: m,
		},
	)
}

func (d *Discharger) notFound(w http.ResponseWriter, r *http.Request) {
	d.writeJSON(w, http.StatusNotFound, httpbakery.Error{
		Message: fmt.Sprintf("cannot find %s", r.URL.String()),
	})
}
