package vpn

import "github.com/skycoin/skywire-utilities/pkg/cipher"

// ClientConfig is a configuration for VPN client.
type ClientConfig struct {
	ServerPK cipher.PubKey
}
