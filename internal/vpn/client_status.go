package vpn

// ClientStatus is a VPN client app detailed status.
type ClientStatus string

const (
	// ClientStatusConnecting is set during VPN session establishment (including handshake).
	ClientStatusConnecting = "Connecting"

	// ClientStatusRunning is set when all establishment is done.
	ClientStatusRunning = "Running"

	// ClientStatusShuttingDown is set during shutdown.
	ClientStatusShuttingDown = "Shutting down"

	// ClientStatusReconnecting is set after connection failure, during reconnection.
	ClientStatusReconnecting = "Connection failed, reconnecting"
)
