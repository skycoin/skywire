//go:build !cgo
// +build !cgo

// Package cipher - Standard Go implementation (fallback)
package cipher

import (
	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/secp256k1-go"
)

// VerifyPubKeySignedHashLight uses standard Skycoin implementation
// This is your original optimized version that skips pubkey recovery
func VerifyPubKeySignedHashLight(pubkey cipher.PubKey, sig cipher.Sig, hash cipher.SHA256) error {
	// Validate pubkey format (fast)
	if secp256k1.VerifyPubkey(pubkey[:]) != 1 {
		return cipher.ErrInvalidSigInvalidPubKey
	}

	// Validate signature format (fast)
	if secp256k1.VerifySignatureValidity(sig[:]) != 1 {
		return cipher.ErrInvalidSigValidity
	}

	// Verify signature (expensive, but still faster than full recovery)
	if secp256k1.VerifySignature(hash[:], sig[:], pubkey[:]) != 1 {
		return cipher.ErrInvalidSigForMessage
	}

	return nil
}

// CleanupCGO is a no-op when CGO is disabled
func CleanupCGO() {
	// No-op for non-CGO builds
}

// UsingCGO returns false when CGO optimization is disabled
func UsingCGO() bool {
	return false
}
