// Package cliconfig cmd/skywire-cli/commands/config/gen_regen_settings_test.go c0-cli
package cliconfig

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// routing.router_settings, routing.router_app_settings and top-level
// app_settings are written by the VISOR, not by config gen:
// they are where `cli route settings` / `cli proxy settings` persist the
// live knob catalog (#5042/#5043) so a sweep survives a restart. config
// gen has no flag for any of them, so a regen that rebuilt Routing and
// Launcher from the defaults silently reset every knob an operator had
// set — and `skywire autoconfig` regenerates on every run, so the reset
// landed on every package update and (now) every dev-loop restart.
func TestRegenPreservesVisorPersistedSettings(t *testing.T) {
	restoreRegen, restoreOld, restoreConf := isRegen, oldConfCache, conf
	t.Cleanup(func() { isRegen, oldConfCache, conf = restoreRegen, restoreOld, restoreConf })

	isRegen = true
	oldConfCache = &visorconfig.V1{
		Routing: &visorconfig.Routing{
			RouterSettings:      map[string]string{"ecf_max_window": "8MiB"},
			RouterAppSettings:   map[string]map[string]string{"skysocks-client": {"dead_route_hold": "30s"}},
			TransportPreference: []string{"stcpr", "dmsg"},
		},
		Launcher: &visorconfig.Launcher{
			Apps: []appserver.AppConfig{{Name: "skysocks-client", AutoStart: true}},
		},
		AppSettings: map[string]visorconfig.AppSettingsEntry{"skysocks-client": {}},
	}
	conf = new(visorconfig.V1)

	configureRouting()
	require.Equal(t, "8MiB", conf.Routing.RouterSettings["ecf_max_window"],
		"router_settings must survive a regen")
	require.Equal(t, "30s", conf.Routing.RouterAppSettings["skysocks-client"]["dead_route_hold"],
		"router_app_settings must survive a regen")
	require.Equal(t, []string{"stcpr", "dmsg"}, conf.Routing.TransportPreference,
		"transport_preference must survive a regen")

	conf.Launcher = &visorconfig.Launcher{Apps: []appserver.AppConfig{{Name: "skysocks-client"}}}
	mergeExistingApps(logging.MustGetLogger("test"))
	require.Contains(t, conf.AppSettings, "skysocks-client",
		"app_settings must survive a regen")
	require.True(t, conf.Launcher.Apps[0].AutoStart,
		"an operator's autostart toggle must survive a regen (pre-existing contract)")
}

// The same call must be a no-op on a fresh (non-regen) generate, so a
// stale cache can never leak knobs into a brand-new config.
func TestFreshGenCarriesNoPersistedSettings(t *testing.T) {
	restoreRegen, restoreOld, restoreConf := isRegen, oldConfCache, conf
	t.Cleanup(func() { isRegen, oldConfCache, conf = restoreRegen, restoreOld, restoreConf })

	isRegen = false
	oldConfCache = &visorconfig.V1{
		Routing:     &visorconfig.Routing{RouterSettings: map[string]string{"ecf_max_window": "8MiB"}},
		AppSettings: map[string]visorconfig.AppSettingsEntry{"skysocks-client": {}},
	}
	conf = new(visorconfig.V1)

	configureRouting()
	conf.Launcher = &visorconfig.Launcher{}
	mergeExistingApps(logging.MustGetLogger("test"))

	require.Empty(t, conf.Routing.RouterSettings)
	require.Empty(t, conf.AppSettings)
}
