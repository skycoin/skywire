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

	// AppStatusReconnecting represents status of VPN client re-connecting.
	AppStatusReconnecting
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

	// AppDetailedStatusVPNConnecting is set during VPN-client session establishment (including handshake).
	AppDetailedStatusVPNConnecting = "Connecting"

	// AppDetailedStatusRunning is set when all establishment is done and / or app is running.
	AppDetailedStatusRunning = "Running"

	// AppDetailedStatusShuttingDown is set during shutdown.
	AppDetailedStatusShuttingDown = "Shutting down"

	// AppDetailedStatusVPNReconnecting is set after connection failure in VPN-client, during reconnection.
	AppDetailedStatusVPNReconnecting = "Connection failed, reconnecting"
)
