// Package noise pkg/noise/dh.go
package noise

import (
	"fmt"
	"io"

	"github.com/skycoin/noise"
	"github.com/skycoin/skycoin/src/cipher"
)

// Secp256k1 implements `noise.DHFunc`.
type Secp256k1 struct{}

// GenerateKeypair helps to implement `noise.DHFunc`.
func (Secp256k1) GenerateKeypair(_ io.Reader) (noise.DHKey, error) {
	pk, sk := cipher.GenerateKeyPair()
	return noise.DHKey{
		Private: sk[:],
		Public:  pk[:],
	}, nil
}

// DH helps to implement `noise.DHFunc`.
func (Secp256k1) DH(sk, pk []byte) []byte {
	pubKey, err := cipher.NewPubKey(pk)
	if err != nil {
		panic(fmt.Sprintf("noise DH: invalid public key: %v", err))
	}
	secKey, err := cipher.NewSecKey(sk)
	if err != nil {
		panic(fmt.Sprintf("noise DH: invalid secret key: %v", err))
	}
	ecdh, err := cipher.ECDH(pubKey, secKey)
	if err != nil {
		panic(fmt.Sprintf("noise DH: ECDH failed: %v", err))
	}
	return append(ecdh, byte(0))
}

// DHLen helps to implement `noise.DHFunc`.
func (Secp256k1) DHLen() int {
	return 33
}

// DHName helps to implement `noise.DHFunc`.
func (Secp256k1) DHName() string {
	return "Secp256k1"
}
