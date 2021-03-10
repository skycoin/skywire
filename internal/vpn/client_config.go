package vpn

import "github.com/skycoin/dmsg/cipher"

// ClientConfig is a configuration for VPN client.
type ClientConfig struct {
	Passcode   string
	Killswitch bool
	ServerPK   cipher.PubKey
}
