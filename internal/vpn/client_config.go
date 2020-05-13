package vpn

import "github.com/SkycoinProject/dmsg/cipher"

// ClientConfig is a configuration for VPN client.
type ClientConfig struct {
	Passcode string
	// TODO: handle this properly
	EnableEncryption bool
	ServerPK         cipher.PubKey
	Credentials      NoiseCredentials
}
