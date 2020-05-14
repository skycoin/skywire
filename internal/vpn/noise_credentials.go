package vpn

import (
	"time"

	"github.com/SkycoinProject/dmsg/cipher"
)

const (
	// HSTimeout is a timeout for noise handshake.
	HSTimeout = 5 * time.Second
)

// NoiseCredentials encapsulates sec and pub keys for noise usage.
type NoiseCredentials struct {
	PK cipher.PubKey
	SK cipher.SecKey
}

// IsValid returns true only if PK and SK are valid.
func (c NoiseCredentials) IsValid() bool {
	return !c.PK.Null() && !c.SK.Null()
}
