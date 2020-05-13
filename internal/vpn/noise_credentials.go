package vpn

import "github.com/SkycoinProject/dmsg/cipher"

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
