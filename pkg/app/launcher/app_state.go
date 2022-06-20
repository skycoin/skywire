package launcher

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

// AppState defines state parameters for a registered App.
type AppState struct {
	AppConfig
	Status         AppStatus `json:"status"`
	DetailedStatus string    `json:"detailed_status"`
}

// AppDetailedStatus is a app's detailed status.
type AppDetailedStatus string

const (
	// AppDetailedStatusStarting is set during app initilization process.
	AppDetailedStatusStarting = "Starting"

	// AppDetailedStatusRunning is set when the app is running.
	AppDetailedStatusRunning = "Running"

	// AppDetailedStatusVPNConnecting is set during VPN-client session establishment (including handshake).
	AppDetailedStatusVPNConnecting = "Connecting"

	// AppDetailedStatusReconnecting is set after connection failure, during reconnection.
	AppDetailedStatusReconnecting = "Connection failed, reconnecting"

	// AppDetailedStatusShuttingDown is set during shutdown.
	AppDetailedStatusShuttingDown = "Shutting down"
)
