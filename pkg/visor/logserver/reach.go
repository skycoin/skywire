// Package logserver pkg/visor/logserver/reach.go c3-vis-core
package logserver

import (
	"errors"
	"net/http"
)

// ErrNotReachable is what a ReachCardProvider returns while the visor is not
// advertising itself, the same choice that keeps it out of the address resolver.
var ErrNotReachable = errors.New("this visor does not advertise how to reach it")

// ReachCardProvider supplies this visor's signed reach card as a JSON body.
type ReachCardProvider interface {
	ReachCardBody() ([]byte, error)
}

// SetReachCardProvider installs the provider behind GET /reach.
func (api *API) SetReachCardProvider(p ReachCardProvider) {
	api.reachCardProvider = p
}

// reachCard serves the signed reach card to any caller, as the address
// resolver answers any visor. A visor that opted out answers 404.
func (api *API) reachCard(w http.ResponseWriter, _ *http.Request) {
	if api.reachCardProvider == nil {
		writeJSON(w, http.StatusNotFound, jsonObj{"error": ErrNotReachable.Error()})
		return
	}
	body, err := api.reachCardProvider.ReachCardBody()
	if errors.Is(err, ErrNotReachable) {
		writeJSON(w, http.StatusNotFound, jsonObj{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, jsonObj{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "max-age=60")
	_, _ = w.Write(body) //nolint:errcheck
}
