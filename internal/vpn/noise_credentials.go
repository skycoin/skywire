package vpn

import (
	"fmt"
	"time"

	"github.com/skycoin/dmsg/cipher"
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

// NewNoiseCredentials creates creds out of sec key and pub key pair.
func NewNoiseCredentials(sk cipher.SecKey, pk cipher.PubKey) NoiseCredentials {
	return NoiseCredentials{
		PK: pk,
		SK: sk,
	}
}

// NewNoiseCredentialsFromSK creates creds out of sec key deriving pub key.
func NewNoiseCredentialsFromSK(sk cipher.SecKey) (NoiseCredentials, error) {
	pk, err := sk.PubKey()
	if err != nil {
		return NoiseCredentials{}, fmt.Errorf("error deriving pub key from sec key: %w", err)
	}

	return NewNoiseCredentials(sk, pk), nil
}

// IsValid returns true only if PK and SK are valid.
func (c NoiseCredentials) IsValid() bool {
	return !c.PK.Null() && !c.SK.Null()
}
