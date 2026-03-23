// Package vpn internal/vpn/server_config.go
package vpn

import "github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"

// ServerConfig is a configuration for VPN server.
type ServerConfig struct {
	Whitelist        []cipher.PubKey
	Secure           bool
	NetworkInterface string
}
