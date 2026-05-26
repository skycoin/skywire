// Package spec is the WASM-clean schema half of pkg/app/appserver.
// Holds the AppConfig wire-format type that pkg/visor/visorconfig.V1's
// Launcher.Apps field embeds. Splitting it out of pkg/app/appserver
// keeps V1 importable from GOOS=js consumers without dragging in
// appserver's operational graph (which transitively pulls
// github.com/james-barrow/golang-ipc — the per-app IPC channel —
// and go.etcd.io/bbolt — the app-state store).
//
// pkg/app/appserver re-exports `AppConfig = spec.AppConfig` so
// existing callers compile unchanged.
package spec

import (
	"github.com/skycoin/skywire/pkg/routing"
)

// AppConfig defines app startup parameters.
//
// User/Group/WorkDir/Env are POSIX-only knobs that only take effect
// when the app runs as an external process (Binary set, no internal
// RunFunc registered). In-process apps share the visor's UID/GID by
// definition — credentials can't be dropped per-goroutine without
// breaking the runtime's thread-pinning assumptions, and the visor
// logs a warning if any of these fields are set on an internal app.
type AppConfig struct {
	Name      string       `json:"name"`
	Binary    string       `json:"binary,omitempty"`
	Args      []string     `json:"args,omitempty"`
	AutoStart bool         `json:"auto_start"`
	Port      routing.Port `json:"port"`
	// User overrides the UID the spawned process runs as (POSIX
	// setuid before exec). Requires the visor to have the
	// privileges to switch users (i.e. running as root, or with
	// CAP_SETUID). Empty = inherit visor UID.
	User string `json:"user,omitempty"`
	// Group overrides the GID. Empty = inherit visor GID, or the
	// primary group of User when User is set.
	Group string `json:"group,omitempty"`
	// WorkDir overrides the proc's working directory. Empty =
	// launcher's per-app default (lc.LocalPath/<app-name>).
	WorkDir string `json:"work_dir,omitempty"`
	// Env adds key=value pairs to the spawned process's
	// environment. Useful for HOME=/home/<user> when User is set.
	Env []string `json:"env,omitempty"`
	// LauncherMode persists the operator's launcher-mode preference
	// for this app: "internal" runs the app inside the visor process,
	// "external" spawns it as a child with optional User/Group/etc.
	// Empty = use whatever the visor's StartApp default chooses
	// (today: external when Binary is set, internal otherwise).
	// Honored by StartApp; StartAppWithMode still wins per-call.
	LauncherMode string `json:"launcher_mode,omitempty"`
}
