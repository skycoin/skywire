// Package vpn pkg/vpn/server_config.go c3-app-vpn
package vpn

import (
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// ServerConfig is a configuration for VPN server.
type ServerConfig struct {
	Whitelist        []cipher.PubKey
	Secure           bool
	NetworkInterface string
	// Log receives the server's diagnostics. Nil uses the vpn_server module logger.
	Log logging.FieldLogger
}
