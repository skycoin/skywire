// Package vpn internal/vpn/client_config.go
package vpn

import "github.com/skycoin/skywire-utilities/pkg/cipher"

// ClientConfig is a configuration for VPN client.
type ClientConfig struct {
	Passcode   string
	Killswitch bool
	ServerPK   cipher.PubKey
	DNSAddr    string
}
