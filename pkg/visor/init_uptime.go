// Package visor — pkg/visor/init_uptime.go: glue for the
// service-self uptime recorder. The recorder itself is constructed
// in run() (so a panic during NewVisor still leaves a session row);
// this file owns the wireup that connects an existing recorder to
// the rest of the visor — logserver routes, RPC accessor, close
// stack — once NewVisor has succeeded.
package visor

import (
	"github.com/skycoin/skywire/pkg/serviceuptime"
)

// SetUptimeRecorder attaches a serviceuptime.Recorder to the visor.
// Idempotent re-attach is a no-op (first writer wins). Wires:
//
//   - the running recorder onto the logserver so /uptime/* routes
//     surface live data over HTTP.
//
// The recorder's keepalive lifecycle is owned by the caller; this
// method only stores the handle and registers it with surfaces that
// need to read from it.
func (v *Visor) SetUptimeRecorder(r *serviceuptime.Recorder) {
	if r == nil {
		return
	}
	v.initLock.Lock()
	if v.uptimeRecorder != nil {
		v.initLock.Unlock()
		return
	}
	v.uptimeRecorder = r
	v.initLock.Unlock()

	// Wire BOTH logservers — initDmsg builds two independent
	// instances: the auth'd DMSG-port one (.api, served on
	// DmsgHTTPPort with PK whitelist) and the localhost-only one
	// (.localAPI, no auth, used by `skywire cli` and local curl).
	// Each has its own *logserver.API value, so a single recorder
	// must be registered on both. Defensive nil-checks cover
	// init-order drift and the case where LocalAddr binding fails.
	v.initLock.RLock()
	lsAPI := v.logServer.api
	lsLocal := v.logServer.localAPI
	v.initLock.RUnlock()
	if lsAPI != nil {
		lsAPI.SetUptimeRecorder(r)
	}
	if lsLocal != nil {
		lsLocal.SetUptimeRecorder(r)
	}
}

// UptimeRecorder returns the wired recorder, or nil if SetUptimeRecorder
// was never called (e.g. recorder open failed at startup). RPC and
// inspection paths should nil-check.
func (v *Visor) UptimeRecorder() *serviceuptime.Recorder {
	v.initLock.RLock()
	defer v.initLock.RUnlock()
	return v.uptimeRecorder
}
