//go:build windows
// +build windows

// Package cipher - Windows implementation (pure Go, no CGO)
package cipher

import (
	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/secp256k1-go"
)

// VerifyPubKeySignedHashLight uses standard Skycoin implementation on Windows
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

// CleanupCGO is a no-op on Windows
func CleanupCGO() {
	// No-op for Windows builds
}

// UsingCGO returns false on Windows
func UsingCGO() bool {
	return false
}
