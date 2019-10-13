package app2

import "github.com/skycoin/dmsg/cipher"

type Key string

func GenerateAppKey() Key {
	raw, _ := cipher.GenerateKeyPair()
	return Key(raw.Hex())
}
