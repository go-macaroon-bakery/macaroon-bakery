package agent_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"

	"github.com/juju/loggo"
	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon.v2-unstable"

	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery/agent"
)

var logger = loggo.GetLogger("httpbakery")

type discharge struct {
	cavId []byte
	c     chan error
}

type Discharger struct {
	Bakery       *bakery.Service
	URL          string
	LoginHandler func(*Discharger, http.ResponseWriter, *http.Request)

	mu      sync.Mutex
	waiting []discharge
}

func (d *Discharger) Serve() *httptest.Server {
	s := httptest.NewServer(d.serveMux())
	d.URL = s.URL
	return s
}

func (d *Discharger) serveMux() *http.ServeMux {
	mux := http.NewServeMux()
	httpbakery.AddDischargeHandler(mux, "/", d.Bakery, d.checker)
	mux.Handle("/login", http.HandlerFunc(d.login))
	mux.Handle("/wait", http.HandlerFunc(d.wait))
	mux.Handle("/", http.HandlerFunc(d.notfound))
	return mux
}

func (d *Discharger) getAgentLogin(r *http.Request) (*agent.AgentLogin, error) {
	var b []byte
	if r.Method == "POST" {
		data, err := ioutil.ReadAll(r.Body)
		if err != nil {
			return nil, errgo.Mask(err)
		}
		b = data
	} else {
		c, err := r.Cookie("agent-login")
		if err != nil {
			return nil, errgo.Notef(err, "cannot find cookie")
		}
		data, err := base64.StdEncoding.DecodeString(c.Value)
		if err != nil {
			return nil, errgo.Notef(err, "cannot decode cookie")
		}
		b = data
	}
	var al agent.AgentLogin
	if err := json.Unmarshal(b, &al); err != nil {
		return nil, errgo.Notef(err, "cannot unmarshal cookie")
	}
	return &al, nil
}

func (d *Discharger) finishWait(w http.ResponseWriter, r *http.Request, err error) {
	r.ParseForm()
	id, err := strconv.Atoi(r.Form.Get("waitid"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, httpbakery.Error{
			Message: fmt.Sprintf("cannot read waitid: %s", err),
		})
		return
	}
	d.waiting[id].c <- err
	return
}

func (d *Discharger) checker(req *http.Request, ci *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
	d.mu.Lock()
	id := len(d.waiting)
	d.waiting = append(d.waiting, discharge{ci.MacaroonId, make(chan error, 1)})
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
	waitId := r.Form.Get("waitid")
	if waitId == "" {
		writeJSON(w, http.StatusBadRequest, httpbakery.Error{
			Message: fmt.Sprintf("no waitid in login request"),
		})
		return
	}

	if r.Method == "GET" && r.Header.Get("Accept") == "application/json" {
		// It's a request for the set of login methods.
		writeJSON(w, http.StatusOK, map[string]string{
			"agent": fmt.Sprintf("%s/login?waitid=%s", d.URL, waitId),
		})
		return
	}
	al, err := d.getAgentLogin(r)
	if err != nil {
		logger.Infof("Discharger.login: cannot read agent login: %v", err)
		writeJSON(w, http.StatusBadRequest, httpbakery.Error{
			Message: fmt.Sprintf("cannot read agent login: %s", err),
		})
		return
	}
	logger.Infof("Discharger.login: checking request")
	_, err = httpbakery.CheckRequest(d.Bakery, r, nil, nil)
	if err == nil {
		d.finishWait(w, r, nil)
		writeJSON(w, http.StatusOK, agent.AgentResponse{
			AgentLogin: true,
		})
		return
	}
	m, err := d.Bakery.NewMacaroon([]checkers.Caveat{
		bakery.LocalThirdPartyCaveat(al.PublicKey),
	})

	if err != nil {
		writeJSON(w, http.StatusInternalServerError, httpbakery.Error{
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
		writeJSON(w, http.StatusBadRequest, httpbakery.Error{
			Message: fmt.Sprintf("cannot read waitid: %s", err),
		})
		return
	}
	err = <-d.waiting[id].c
	if err != nil {
		writeJSON(w, http.StatusForbidden, err)
		return
	}
	m, err := d.Bakery.Discharge(
		bakery.ThirdPartyCheckerFunc(
			func(*bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
				return nil, nil
			},
		),
		d.waiting[id].cavId,
	)
	if err != nil {
		writeJSON(w, http.StatusForbidden, err)
		return
	}
	writeJSON(
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
	writeJSON(w, http.StatusNotFound, httpbakery.Error{
		Message: fmt.Sprintf("cannot find %s", r.URL.String()),
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) error {
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
