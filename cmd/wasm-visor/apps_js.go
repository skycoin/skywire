//go:build js && wasm

// Package main — the browser visor's in-process app registry, surfaced through
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
)

const (
	appSkychat    = "skychat"
	appSkycoinWeb = "skycoin-web"
)

// appAutoStart holds the per-session autostart preference. A tab has no
// on-disk config, so this is best-effort for the current boot (SetAutoStart
// updates it; it isn't persisted). Seeded with each app's default.
var (
	appAutoMu    sync.Mutex
	appAutoStart = map[string]bool{
		appSkychat:    true,
		appSkycoinWeb: false,
	}
)

func autoStartPref(name string) bool {
	appAutoMu.Lock()
	defer appAutoMu.Unlock()
	return appAutoStart[name]
}

// appStateJSON is the native appserver.AppState wire shape the Angular Apps
// tab reads (the subset the tab renders; no `binary` → internal app).
type appStateJSON struct {
	Name           string   `json:"name"`
	Args           []string `json:"args,omitempty"`
	AutoStart      bool     `json:"auto_start"`
	Port           uint16   `json:"port"`
	LauncherMode   string   `json:"launcher_mode"`
	Status         int      `json:"status"`
	DetailedStatus string   `json:"detailed_status"`
}

func skychatRunning() bool {
	if procM == nil {
		return false
	}
	_, ok := procM.ProcByName(appSkychat)
	return ok
}

// SelfApps returns the tab's in-process apps as the native []AppState JSON so
// the shared Apps tab lists + controls them like a native visor.
func (visorSelf) SelfApps() []byte {
	status := 0
	detail := "Stopped"
	if skychatRunning() {
		status, detail = 1, "Running"
	}
	apps := []appStateJSON{
		{
			Name: appSkychat, Port: skychatPort, LauncherMode: "internal",
			AutoStart: autoStartPref(appSkychat), Status: status, DetailedStatus: detail,
		},
		{
			// The bundled skycoin-web wallet, served at /wallet/ (always
			// reachable in the tab). Args carries the "open" address the UI
			// resolves — the wasm/native-aligned wallet URL.
			Name: appSkycoinWeb, Args: []string{"url:/wallet/"}, Port: 0, LauncherMode: "internal",
			AutoStart: autoStartPref(appSkycoinWeb), Status: 1, DetailedStatus: "Running",
		},
	}
	b, err := json.Marshal(apps)
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
	case appSkycoinWeb:
		return nil // the wallet is always served at /wallet/
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
	case appSkycoinWeb:
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
