// Package visor pkg/visor/api_app_settings.go
//
// The visor half of the live app-settings surface: a map of tuning knobs per
// app, held on the proc manager and PULLED by the app on a tick it already
// runs (pkg/app/appserver/app_settings.go explains why a pull and not a push).
//
// The knobs themselves are opaque here — name → int64, plus name → string for
// the list ones, exactly as pkg/skysocks/skysettings encodes them. The visor
// carries them; the app's own registry owns their meaning, their kinds and
// their defaults, so a knob can be added to the client without touching this
// file or the wire.
//
// What the visor DOES own is how long a knob lives. It outlives the app
// process (the proc manager no longer clears the set on stop) and it outlives
// the visor: every change is written to visorconfig.V1.AppSettings and
// restored into the store at boot, the way routing.mux_fec is. An operator who
// set a value gets to keep it until they reset it.
package visor

import (
	"errors"

	"github.com/skycoin/skywire/pkg/app/appserver"
)

// AppSettings is the CLI's read of an app's live tuning knobs: the values the
// visor intends, the version they carry, and the version the app last reported
// having installed. Version != Applied is a change still in flight — the app
// installs it on its next pull (one tunnel.probe_interval, 5 s by default).
type AppSettings struct {
	AppName string           `json:"app_name"`
	Values  map[string]int64 `json:"values,omitempty"`
	// Text carries the LIST knobs (pool.exclude_pks, pool.require_tp_types),
	// whose payload is a comma-separated set of tokens rather than an int64.
	Text    map[string]string `json:"text,omitempty"`
	Version uint64            `json:"version"`
	Applied uint64            `json:"applied"`
}

// ErrProcManagerNotAvailable is returned when the app settings are asked for on
// a visor with no proc manager (a hypervisor-only process, or a unit test).
var ErrProcManagerNotAvailable = errors.New("proc manager not available")

// GetAppSettings returns the live tuning knobs held for appName.
func (v *Visor) GetAppSettings(appName string) (AppSettings, error) {
	if v.procM == nil {
		return AppSettings{}, ErrProcManagerNotAvailable
	}
	vals, text, version, applied := v.procM.AppSettingsState(appName)
	return AppSettings{AppName: appName, Values: vals, Text: text, Version: version, Applied: applied}, nil
}

// SetAppSettings replaces the whole intended knob set for appName and returns
// the state that results, so a caller sees the new version without a second
// round-trip. Empty or nil maps are a full reset: a knob the maps do not name
// goes back to the value the app compiled with.
//
// PERSISTED. The set is written to the visor config, so it survives both the
// app stopping and the visor restarting — the two silent resets that made
// every bench sweep re-apply itself. `proxy settings --reset` sends empty maps,
// which clears the app here and drops its entry from the config; the version
// counter still MOVES, so the running app learns of the reset on its next pull
// rather than keeping what it already installed.
func (v *Visor) SetAppSettings(appName string, vals map[string]int64, text map[string]string) (AppSettings, error) {
	if v.procM == nil {
		return AppSettings{}, ErrProcManagerNotAvailable
	}
	version := v.procM.SetAppSettings(appName, vals, text)
	v.persistAppSettings()
	_, _, _, applied := v.procM.AppSettingsState(appName)
	return AppSettings{AppName: appName, Values: vals, Text: text, Version: version, Applied: applied}, nil
}

// persistAppSettings writes the whole per-app knob set to the config file. A
// flush failure is logged, never returned: the value is already in force on the
// running app, and refusing the call would be a lie about what happened.
func (v *Visor) persistAppSettings() {
	if v.conf == nil || v.procM == nil {
		return
	}
	vals, text := v.procM.AllAppSettings()
	if err := v.conf.UpdateAppSettings(vals, text); err != nil {
		v.log.WithError(err).Warn("Failed to persist app_settings to config")
	}
}

// restoreAppSettings loads the persisted per-app knobs into the proc manager at
// boot, before any app starts, so an app's first pull already carries them.
func (v *Visor) restoreAppSettings(procM appserver.ProcManager) {
	if v.conf == nil || procM == nil || v.conf.AppSettings == nil {
		return
	}
	for app, s := range v.conf.AppSettings {
		if len(s.Values) == 0 && len(s.Text) == 0 {
			continue
		}
		procM.SetAppSettings(app, s.Values, s.Text)
		v.log.Infof("Restored %d live setting(s) for app %q from config",
			len(s.Values)+len(s.Text), app)
	}
}
