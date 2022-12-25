// Package appserver pkg/app/appserver/app_state.go
package appserver



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
