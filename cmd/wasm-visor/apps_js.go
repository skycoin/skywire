//go:build js && wasm

// Package main cmd/wasm-visor/apps_js.go c3-vis-wasm
// the SAME hypervisor Apps tab as a native visor (pkg/wasmhv.SelfProvider's
// SelfApps + StartApp/StopApp/SetAutoStart). A tab has no child processes, so
// "start/stop" toggles the in-tab AppFunc/route rather than spawning a
// subprocess, and "open" (the app's UI) is a JS-side action on the app's
// advertised address — not an OS port. This is what closes the biggest
// wasm↔native management gap: the wallet (skycoin-web) and skychat become
// first-class, controllable apps in the tab, exactly like on the host visor.
package main

import (
	"encoding/json"
	"errors"
	"sync"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/wasmhv"
)

const appSkychat = "skychat"

// The wallet is intentionally NOT an app here. It's a built-in hypervisor UI
// feature (served at /wallet/, reached via the ☰ menu / wallet tab), not a
// process — so it doesn't belong in the Apps list. The skycoin-web *server*
// (disk wallets, server-side multi-coin) is the opt-in backend app, and a tab
// has no server, so it's absent here. See docs/design/gui-app-serving-modes.md.

// appAutoStart holds the per-session autostart preference. A tab has no
// on-disk config, so this is best-effort for the current boot (SetAutoStart
// updates it; it isn't persisted). Seeded with each app's default.
var (
	appAutoMu    sync.Mutex
	appAutoStart = map[string]bool{
		appSkychat:     true,
		defaultProxyID: true, // default skysocks-client-lite auto-selects an exit
	}
)

func autoStartPref(name string) bool {
	appAutoMu.Lock()
	defer appAutoMu.Unlock()
	return appAutoStart[name]
}

func skychatRunning() bool {
	if procM == nil {
		return false
	}
	_, ok := procM.ProcByName(appSkychat)
	return ok
}

// selfAppStates builds the tab's in-process apps in the native AppState shape.
// Shared by SelfApps() (the /apps control endpoint) and SelfOverview (which
// stamps them into overview.apps — the field the hvui Apps tab actually renders
// from). Keeping one builder means the list + control endpoint can't drift.
func selfAppStates() []wasmhv.AppState {
	status, detail := 0, "Stopped"
	if skychatRunning() {
		status, detail = 1, "Running"
	}
	states := []wasmhv.AppState{
		{
			AppConfig: wasmhv.AppConfig{
				Name: appSkychat, Port: skychatPort, LauncherMode: "internal",
				AutoStart: autoStartPref(appSkychat),
			},
			Status: status, DetailedStatus: detail,
		},
	}
	// skysocks-client-lite proxy instances (skysocks-lite-as-app P1) — listed
	// under the same native app name so the shared Apps tab renders + controls
	// them like a native visor's skysocks-client.
	for _, inst := range proxyInstancesSnapshot() {
		states = append(states, wasmhv.AppState{
			AppConfig: wasmhv.AppConfig{
				Name: inst.ID, Port: uint16(skysocksPort), LauncherMode: "internal",
				AutoStart: autoStartPref(inst.ID),
			},
			Status: inst.Status, DetailedStatus: inst.Detail,
		})
	}
	return states
}

// SelfApps returns the tab's in-process apps as the native []AppState JSON so
// the shared Apps tab lists + controls them like a native visor.
func (visorSelf) SelfApps() []byte {
	b, err := json.Marshal(selfAppStates())
	if err != nil {
		return nil
	}
	return b
}

// StartApp starts an in-process app by name (browser = re-launch the AppFunc).
func (visorSelf) StartApp(name string) error {
	switch name {
	case appSkychat:
		if skychatRunning() {
			return nil
		}
		if procM == nil {
			return errors.New("proc manager not ready")
		}
		_, err := procM.Start(appcommon.ProcConfig{
			AppName:     appSkychat,
			ProcKey:     appcommon.RandProcKey(),
			VisorPK:     selfPK,
			RoutingPort: skychatPort,
			RunFunc:     runBrowserSkychat,
			RunMode:     appcommon.RunModeInternal,
		})
		return err
	}
	// A proxy instance "starts" by establishing its route/session to its exit
	// (pre-dial), so status flips to running. If no exit is selected yet, the
	// auto-selector is still working — report that rather than a hard error.
	if isProxyInstance(name) {
		exit, ok := proxyInstanceExit(name)
		if !ok {
			return errors.New("no exit selected yet (auto-selection in progress)")
		}
		_, err := skysocksSession(name, exit)
		return err
	}
	return errors.New("unknown app: " + name)
}

// StopApp stops an in-process app by name.
func (visorSelf) StopApp(name string) error {
	switch name {
	case appSkychat:
		if procM == nil {
			return nil
		}
		return procM.Stop(appSkychat)
	}
	// A proxy instance "stops" by severing all sessions to its exit.
	if isProxyInstance(name) {
		if exit, ok := proxyInstanceExit(name); ok {
			closeSkysocksExit(exit)
		}
		return nil
	}
	return errors.New("unknown app: " + name)
}

// SetAutoStart records the per-session autostart preference (best-effort — a
// tab has no on-disk config to persist it to).
func (visorSelf) SetAutoStart(name string, on bool) error {
	appAutoMu.Lock()
	defer appAutoMu.Unlock()
	if _, ok := appAutoStart[name]; !ok {
		return errors.New("unknown app: " + name)
	}
	appAutoStart[name] = on
	return nil
}
