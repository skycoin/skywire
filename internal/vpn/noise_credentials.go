package vpn

import (
	"time"

	"github.com/SkycoinProject/dmsg/cipher"
)

const (
	HSTimeout = 5 * time.Second
)

type NoiseCredentials struct {
	PK cipher.PubKey
	SK cipher.SecKey
}

func (c NoiseCredentials) PKIsNil() bool {
	return c.PK == cipher.PubKey{}
}

func (c NoiseCredentials) SKIsNil() bool {
	return c.SK == cipher.SecKey{}
}
