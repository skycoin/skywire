// Package visor pkg/visor/api_app_settings.go
//
// The visor half of the live app-settings surface: a process-scoped map of
// tuning knobs per running app, held on the proc manager and PULLED by the app
// on a tick it already runs (pkg/app/appserver/app_settings.go explains why a
// pull and not a push).
//
// The knobs themselves are opaque here — name → int64, exactly as
// pkg/skysocks/skysettings encodes them. The visor carries them; the app's own
// registry owns their meaning, their kinds and their defaults, so a knob can be
// added to the client without touching this file or the wire.
package visor

import "errors"

// AppSettings is the CLI's read of an app's live tuning knobs: the values the
// visor intends, the version they carry, and the version the app last reported
// having installed. Version != Applied is a change still in flight — the app
// installs it on its next pull (one tunnel.probe_interval, 5 s by default).
type AppSettings struct {
	AppName string           `json:"app_name"`
	Values  map[string]int64 `json:"values,omitempty"`
	Version uint64           `json:"version"`
	Applied uint64           `json:"applied"`
}

// ErrProcManagerNotAvailable is returned when the app settings are asked for on
// a visor with no proc manager (a hypervisor-only process, or a unit test).
var ErrProcManagerNotAvailable = errors.New("proc manager not available")

// GetAppSettings returns the live tuning knobs held for appName.
func (v *Visor) GetAppSettings(appName string) (AppSettings, error) {
	if v.procM == nil {
		return AppSettings{}, ErrProcManagerNotAvailable
	}
	vals, version, applied := v.procM.AppSettingsState(appName)
	return AppSettings{AppName: appName, Values: vals, Version: version, Applied: applied}, nil
}

// SetAppSettings replaces the whole intended knob set for appName and returns
// the state that results, so a caller sees the new version without a second
// round-trip. An empty or nil map is a full reset: a knob the map does not name
// goes back to the value the app compiled with.
//
// Not persisted. The values live for as long as the app process does, matching
// what `proxy mux cap/width/standby` already do on the router side — a bench
// sweep must never be something a restart silently inherits.
func (v *Visor) SetAppSettings(appName string, vals map[string]int64) (AppSettings, error) {
	if v.procM == nil {
		return AppSettings{}, ErrProcManagerNotAvailable
	}
	version := v.procM.SetAppSettings(appName, vals)
	_, _, applied := v.procM.AppSettingsState(appName)
	return AppSettings{AppName: appName, Values: vals, Version: version, Applied: applied}, nil
}
