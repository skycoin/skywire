package appcommon

import "github.com/SkycoinProject/dmsg/cipher"

// Key is an app key to authenticate within the
// app server.
type Key string

// GenerateAppKey generates new app key.
func GenerateAppKey() Key {
	raw, _ := cipher.GenerateKeyPair()
	return Key(raw.Hex())
}
