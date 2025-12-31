// Package noise pkg/noise/dh.go
package noise

import (
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
	// Use non-panic versions to handle invalid keys gracefully
	pubKey, err := cipher.NewPubKey(pk)
	if err != nil {
		// Return empty key on error to prevent panic
		// The handshake will fail with this invalid key
		return make([]byte, 33)
	}
	secKey, err := cipher.NewSecKey(sk)
	if err != nil {
		// Return empty key on error to prevent panic
		return make([]byte, 33)
	}
	ecdh, err := cipher.ECDH(pubKey, secKey)
	if err != nil {
		// Return empty key on error to prevent panic
		return make([]byte, 33)
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
