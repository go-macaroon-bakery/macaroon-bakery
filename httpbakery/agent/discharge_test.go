package agent_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"

	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v1"

	"gopkg.in/macaroon-bakery.v1/bakery"
	"gopkg.in/macaroon-bakery.v1/bakery/checkers"
	"gopkg.in/macaroon-bakery.v1/httpbakery"
	"gopkg.in/macaroon-bakery.v1/httpbakery/agent"
)

type discharge struct {
	cavId string
	c     chan error
}

type Discharger struct {
	Bakery       *bakery.Service
	URL          string
	LoginHandler func(*Discharger, http.ResponseWriter, *http.Request)

	mu      sync.Mutex
	waiting []discharge
}

func (d *Discharger) ServeMux() *http.ServeMux {
	mux := http.NewServeMux()
	httpbakery.AddDischargeHandler(mux, "/", d.Bakery, d.checker)
	mux.Handle("/login", http.HandlerFunc(d.login))
	mux.Handle("/wait", http.HandlerFunc(d.wait))
	mux.Handle("/", http.HandlerFunc(d.notfound))
	return mux
}

func (d *Discharger) Serve() *httptest.Server {
	s := httptest.NewServer(d.ServeMux())
	d.URL = s.URL
	return s
}

func (d *Discharger) WriteJSON(w http.ResponseWriter, status int, v interface{}) error {
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

func (d *Discharger) FinishWait(w http.ResponseWriter, r *http.Request, err error) {
	r.ParseForm()
	id, err := strconv.Atoi(r.Form.Get("waitid"))
	if err != nil {
		d.WriteJSON(w, http.StatusBadRequest, httpbakery.Error{
			Message: fmt.Sprintf("cannot read waitid: %s", err),
		})
		return
	}
	d.waiting[id].c <- err
	return
}

func (d *Discharger) checker(req *http.Request, cavId, cav string) ([]checkers.Caveat, error) {
	d.mu.Lock()
	id := len(d.waiting)
	d.waiting = append(d.waiting, discharge{cavId, make(chan error, 1)})
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
	r.ParseForm()
	if d.LoginHandler != nil {
		d.LoginHandler(d, w, r)
		return
	}
	al, err := d.GetAgentLogin(r)
	if err != nil {
		d.WriteJSON(w, http.StatusBadRequest, httpbakery.Error{
			Message: fmt.Sprintf("cannot read agent login: %s", err),
		})
		return
	}
	_, err = httpbakery.CheckRequest(d.Bakery, r, nil, nil)
	if err == nil {
		d.FinishWait(w, r, nil)
		d.WriteJSON(w, http.StatusOK, agent.AgentResponse{
			AgentLogin: true,
		})
		return
	}
	m, err := d.Bakery.NewMacaroon("", nil, []checkers.Caveat{
		bakery.LocalThirdPartyCaveat(al.PublicKey),
	})
	if err != nil {
		d.WriteJSON(w, http.StatusInternalServerError, httpbakery.Error{
			Message: fmt.Sprintf("cannot create macaroon: %s", err),
		})
		return
	}
	httpbakery.WriteDischargeRequiredError(w, m, "", nil)
}

func (d *Discharger) wait(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	id, err := strconv.Atoi(r.Form.Get("waitid"))
	if err != nil {
		d.WriteJSON(w, http.StatusBadRequest, httpbakery.Error{
			Message: fmt.Sprintf("cannot read waitid: %s", err),
		})
		return
	}
	err = <-d.waiting[id].c
	if err != nil {
		d.WriteJSON(w, http.StatusForbidden, err)
		return
	}
	m, err := d.Bakery.Discharge(
		bakery.ThirdPartyCheckerFunc(
			func(cavId, caveat string) ([]checkers.Caveat, error) {
				return nil, nil
			},
		),
		d.waiting[id].cavId,
	)
	if err != nil {
		d.WriteJSON(w, http.StatusForbidden, err)
		return
	}
	d.WriteJSON(
		w,
		http.StatusOK,
		struct {
			Macaroon *macaroon.Macaroon
		}{
			Macaroon: m,
		},
	)
}

func (d *Discharger) notfound(w http.ResponseWriter, r *http.Request) {
	d.WriteJSON(w, http.StatusNotFound, httpbakery.Error{
		Message: fmt.Sprintf("cannot find %s", r.URL.String()),
	})
}
