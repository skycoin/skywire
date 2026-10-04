// Package logserver pkg/visor/logserver/uptime.go c3-vis-core
// pkg/serviceuptime HTTP handlers. Wires /uptime/* onto the visor's
// existing logserver router so a CLI client can render the visor's
// session history without going through the unix-socket RPC.
package logserver

import (
	"net/http"

	"github.com/skycoin/skywire/pkg/serviceuptime"
)

// SetUptimeRecorder wires the recorder into the log server. Safe
// to call before or after handler registration; the routes nil-check
// at request time, so a recorder attached after some early traffic
// still works for subsequent requests.
func (api *API) SetUptimeRecorder(r *serviceuptime.Recorder) {
	api.uptimeRecorder = r
}

// registerUptimeRoutes mounts /uptime/* through route, which adds the
// auth check. Each handler answers 503 while api.uptimeRecorder is nil.
func (api *API) registerUptimeRoutes(route func(string, http.HandlerFunc)) {
	route("GET /uptime/now", func(w http.ResponseWriter, req *http.Request) {
		if api.uptimeRecorder == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		serviceuptime.CurrentSessionHandler(api.uptimeRecorder).ServeHTTP(w, req)
	})
	route("GET /uptime/sessions", func(w http.ResponseWriter, req *http.Request) {
		if api.uptimeRecorder == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		serviceuptime.SessionsHandler(api.uptimeRecorder.Store()).ServeHTTP(w, req)
	})
	route("GET /uptime/timeline", func(w http.ResponseWriter, req *http.Request) {
		if api.uptimeRecorder == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		serviceuptime.TimelineHandler(api.uptimeRecorder.Store()).ServeHTTP(w, req)
	})
	route("GET /uptime/dates", func(w http.ResponseWriter, req *http.Request) {
		if api.uptimeRecorder == nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		serviceuptime.DatesHandler(api.uptimeRecorder.Store()).ServeHTTP(w, req)
	})
}
