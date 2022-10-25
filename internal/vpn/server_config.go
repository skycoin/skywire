// Package vpn internal/vpn/server_config.go
package vpn

// ServerConfig is a configuration for VPN server.
type ServerConfig struct {
	Passcode         string
	Secure           bool
	NetworkInterface string
}
