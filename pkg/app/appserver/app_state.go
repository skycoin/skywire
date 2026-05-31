// Package appserver pkg/app/appserver/app_state.go
package appserver

import (
	"github.com/skycoin/skywire/pkg/app/appserver/spec"
)

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
// AppConfig is the wire-format type for an app's launcher entry.
// Aliased from pkg/app/appserver/spec so existing callers writing
// `appserver.AppConfig{...}` keep compiling, while the canonical
// (WASM-clean) definition lives in the spec leaf package.
type AppConfig = spec.AppConfig

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
