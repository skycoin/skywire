// Package vpn pkg/vpn/server_config.go c4-app-vpn
package vpn

import "github.com/skycoin/skywire/pkg/cipher"

// ServerConfig is a configuration for VPN server.
type ServerConfig struct {
	Whitelist        []cipher.PubKey
	Secure           bool
	NetworkInterface string
}
