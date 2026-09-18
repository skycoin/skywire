// Package appserver pkg/app/appserver/app_settings.go
//
// Per-app live tuning settings — the one visor→app value channel the mux bench
// needs, built the only way the existing app RPC allows.
//
// The app is the RPC client and the visor the server (rpc_ingress_gateway.go),
// so there is no way to PUSH a value into a running app. Instead the visor
// holds a map per app name and the app PULLS it on a tick it already runs
// (skysocks-client's keepalive loop). A version counter makes the steady state
// an empty round-trip: the app reports the version it has applied, and the
// gateway answers with values only when the visor's is newer.
//
// The store is process-scoped and NOT persisted, matching what `proxy mux
// cap/width/standby` already do on the router side: a value survives for as
// long as the app runs and is cleared when it stops, so a restart is always a
// return to the compiled defaults.
package appserver

import "sync"

// appSettings is the visor's per-app knob store. Values are the int64 payloads
// of pkg/skysocks/skysettings — the gateway neither parses nor validates them,
// it just carries them; the app's own registry ignores names it does not know.
type appSettings struct {
	mx sync.RWMutex
	// values is the FULL intended set per app: a knob missing from it means
	// "compiled default", so a reset is a delete rather than a sentinel.
	values map[string]map[string]int64
	// version is bumped on every change, applied is the version the app last
	// told us it had installed. version != applied is the CLI's "pending".
	version map[string]uint64
	applied map[string]uint64
}

func newAppSettings() *appSettings {
	return &appSettings{
		values:  make(map[string]map[string]int64),
		version: make(map[string]uint64),
		applied: make(map[string]uint64),
	}
}

// pull answers the app's poll: it records the version the app says it has
// applied and returns the current values plus the version they carry. The
// values are nil when the app is already up to date, so a steady-state poll
// costs an empty gob round-trip.
func (s *appSettings) pull(appName string, applied uint64) (map[string]int64, uint64) {
	s.mx.Lock()
	defer s.mx.Unlock()
	s.applied[appName] = applied
	v := s.version[appName]
	if applied == v {
		return nil, v
	}
	return copyValues(s.values[appName]), v
}

// set replaces the whole intended set for appName and returns the new version.
// An empty or nil map is a full reset.
func (s *appSettings) set(appName string, vals map[string]int64) uint64 {
	s.mx.Lock()
	defer s.mx.Unlock()
	if len(vals) == 0 {
		delete(s.values, appName)
	} else {
		s.values[appName] = copyValues(vals)
	}
	s.version[appName]++
	return s.version[appName]
}

// state is the CLI's read: the intended values, the version they carry, and
// the version the app last reported applying.
func (s *appSettings) state(appName string) (map[string]int64, uint64, uint64) {
	s.mx.RLock()
	defer s.mx.RUnlock()
	return copyValues(s.values[appName]), s.version[appName], s.applied[appName]
}

// clear drops everything for an app that stopped. The next run starts from the
// compiled defaults; nothing here outlives the process either way.
func (s *appSettings) clear(appName string) {
	s.mx.Lock()
	defer s.mx.Unlock()
	delete(s.values, appName)
	delete(s.version, appName)
	delete(s.applied, appName)
}

func copyValues(in map[string]int64) map[string]int64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// AppSettingsResp is what an app gets back from the AppSettings ingress call.
type AppSettingsResp struct {
	// Version is the visor's current version for this app. The app stores it
	// and reports it as Applied on the next poll.
	Version uint64 `json:"version"`
	// Values is the full intended set, or nil when Applied already matched
	// Version (nothing to do) — NOT "reset everything", which is an empty
	// non-nil map carried at a NEW version.
	Values map[string]int64 `json:"values,omitempty"`
	// Changed distinguishes the two nil cases above.
	Changed bool `json:"changed"`
}

// AppSettingsReq is an app polling for its settings, reporting the version it
// currently has installed.
type AppSettingsReq struct {
	Applied uint64 `json:"applied"`
}

// SetAppSettings replaces the live tuning knobs held for appName and returns
// the new version. Implements ProcManager.
func (m *procManager) SetAppSettings(appName string, vals map[string]int64) uint64 {
	return m.settings.set(appName, vals)
}

// AppSettings answers an app's poll for its knobs. Implements ProcManager.
func (m *procManager) AppSettings(appName string, applied uint64) (map[string]int64, uint64) {
	return m.settings.pull(appName, applied)
}

// AppSettingsState reports the intended values, their version, and the version
// the app last acknowledged. Implements ProcManager.
func (m *procManager) AppSettingsState(appName string) (map[string]int64, uint64, uint64) {
	return m.settings.state(appName)
}
