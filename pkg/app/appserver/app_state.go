// Package appserver pkg/app/appserver/app_state.go
package appserver

import "github.com/skycoin/skywire/pkg/routing"

// AppStatus defines running status of an App.
type AppStatus int

const (
	// AppStatusStopped represents status of a stopped App.
	AppStatusStopped AppStatus = iota

	// AppStatusRunning represents status of a running App.
	AppStatusRunning

	// AppStatusErrored represents status of an errored App.
	AppStatusErrored

	// AppStatusStarting represents status of an app starting.
	AppStatusStarting
)

// String returns a string representation of the AppStatus.
func (s AppStatus) String() string {
	switch s {
	case AppStatusStopped:
		return "stopped"
	case AppStatusRunning:
		return "running"
	case AppStatusErrored:
		return "errored"
	case AppStatusStarting:
		return "starting"
	default:
		return "unknown"
	}
}

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

// AppState defines state parameters for a registered App.
type AppState struct {
	AppConfig
	Status         AppStatus `json:"status"`
	DetailedStatus string    `json:"detailed_status"`
}

// AppDetailedStatus is a app's detailed status.
type AppDetailedStatus string

const (
	// AppDetailedStatusStarting is set during app initialization process.
	AppDetailedStatusStarting = "Starting"

	// AppDetailedStatusRunning is set when the app is running.
	AppDetailedStatusRunning = "Running"

	// AppDetailedStatusVPNConnecting is set during VPN-client session establishment (including handshake).
	AppDetailedStatusVPNConnecting = "Connecting"

	// AppDetailedStatusReconnecting is set after connection failure, during reconnection.
	AppDetailedStatusReconnecting = "Connection failed, reconnecting"

	// AppDetailedStatusShuttingDown is set during shutdown.
	AppDetailedStatusShuttingDown = "Shutting down"

	// AppDetailedStatusStopped is set after shutdown.
	AppDetailedStatusStopped = "Stopped"
)
