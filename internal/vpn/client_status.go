package vpn

type ClientStatus string

const (
	ClientStatusConnecting   = "Connecting"
	ClientStatusRunning      = "Running"
	ClientStatusShuttingDown = "Shutting down"
	ClientStatusReconnecting = "Connection failed, reconnecting"
)
