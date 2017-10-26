package bakerytest

import (
	"bytes"
	"fmt"
	"sync"
	"time"

	errgo "gopkg.in/errgo.v1"

	"gopkg.in/macaroon-bakery.v2/bakery"
	"gopkg.in/macaroon-bakery.v2/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2/httpbakery"
)

// Rendezvous implements a place where discharge information
// can be stored, recovered and waited for.
type Rendezvous struct {
	mu      sync.Mutex
	maxId   int
	waiting map[string]*dischargeFuture
}

func NewRendezvous() *Rendezvous {
	return &Rendezvous{
		waiting: make(map[string]*dischargeFuture),
	}
}

type dischargeFuture struct {
	info    *bakery.ThirdPartyCaveatInfo
	done    chan struct{}
	caveats []checkers.Caveat
	err     error
}

// NewDischarge creates a new discharge in the rendezvous
// associated with the given caveat information.
// It returns an identifier for the discharge that can
// later be used to complete the discharge or find
// out the information again.
func (r *Rendezvous) NewDischarge(cav *bakery.ThirdPartyCaveatInfo) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	dischargeId := fmt.Sprintf("%d", r.maxId)
	r.maxId++
	r.waiting[dischargeId] = &dischargeFuture{
		info: cav,
		done: make(chan struct{}),
	}
	return dischargeId
}

// Info returns information on the given discharge id
// and reports whether the information has been found.
func (r *Rendezvous) Info(dischargeId string) (*bakery.ThirdPartyCaveatInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.waiting[dischargeId]
	if d == nil {
		return nil, false
	}
	return d.info, true
}

// DischargeComplete marks the discharge with the given id
// as completed with the given caveats,
// which will be associated with the given discharge id
// and returned from Await.
func (r *Rendezvous) DischargeComplete(dischargeId string, caveats []checkers.Caveat) {
	r.dischargeDone(dischargeId, caveats, nil)
}

// DischargeFailed marks the discharge with the given id
// as failed with the given error, which will be
// returned from Await or CheckToken when they're
// called with that id.
func (r *Rendezvous) DischargeFailed(dischargeId string, err error) {
	r.dischargeDone(dischargeId, nil, err)
}

func (r *Rendezvous) dischargeDone(dischargeId string, caveats []checkers.Caveat, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.waiting[dischargeId]
	if d == nil {
		panic(errgo.Newf("invalid discharge id %q", dischargeId))
	}
	select {
	case <-d.done:
		panic(errgo.Newf("DischargeComplete called twice"))
	default:
	}
	d.caveats, d.err = caveats, err
	close(d.done)
}

// Await waits for DischargeComplete or DischargeFailed to be called,
// and returns either the caveats passed to DischargeComplete
// or the error passed to DischargeFailed.
//
// It waits for at least the given duration. If timeout is zero,
// it returns the information only if it is already available.
func (r *Rendezvous) Await(dischargeId string, timeout time.Duration) ([]checkers.Caveat, error) {
	r.mu.Lock()
	d := r.waiting[dischargeId]
	r.mu.Unlock()
	if d == nil {
		return nil, errgo.Newf("invalid discharge id %q", dischargeId)
	}
	if timeout == 0 {
		select {
		case <-d.done:
		default:
			return nil, errgo.New("rendezvous has not completed")
		}
	} else {
		select {
		case <-d.done:
		case <-time.After(timeout):
			return nil, errgo.New("timeout waiting for rendezvous to complete")
		}
	}
	if d.err != nil {
		return nil, errgo.Mask(d.err, errgo.Any)
	}
	return d.caveats, nil
}

func (r *Rendezvous) DischargeToken(dischargeId string) *httpbakery.DischargeToken {
	_, err := r.Await(dischargeId, 0)
	if err != nil {
		panic(errgo.Notef(err, "cannot obtain discharge token for %q", dischargeId))
	}
	return &httpbakery.DischargeToken{
		Kind:  "discharge-id",
		Value: []byte(dischargeId),
	}
}

// CheckToken checks that the given token is valid for discharging the
// given caveat, and returns any caveats passed to DischargeComplete
// if it is.
func (r *Rendezvous) CheckToken(token *httpbakery.DischargeToken, cav *bakery.ThirdPartyCaveatInfo) ([]checkers.Caveat, error) {
	if token.Kind != "discharge-id" {
		return nil, errgo.Newf("invalid discharge token kind %q", token.Kind)
	}
	info, ok := r.Info(string(token.Value))
	if !ok {
		return nil, errgo.Newf("discharge token %q not found", token.Value)
	}
	if !bytes.Equal(info.Caveat, cav.Caveat) {
		return nil, errgo.Newf("caveat provided to CheckToken does not match original")
	}
	if !bytes.Equal(info.Id, cav.Id) {
		return nil, errgo.Newf("caveat id provided to CheckToken does not match original")
	}
	caveats, err := r.Await(string(token.Value), 0)
	if err != nil {
		// Don't mask the error because we want the cause to remain
		// unchanged if it was passed to DischargeFailed.
		return nil, errgo.Mask(err, errgo.Any)
	}
	return caveats, nil
}
