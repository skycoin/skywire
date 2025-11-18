//go:build cgo && !windows
// +build cgo,!windows

// Package cipher - CGO optimized verification (3-5x faster)
package cipher

/*
#cgo LDFLAGS: -lsecp256k1
#include <secp256k1.h>
#include <secp256k1_recovery.h>
*/
import "C"
import (
	"unsafe"

	"github.com/skycoin/skycoin/src/cipher"
)

var secp256k1Ctx *C.secp256k1_context

func init() {
	secp256k1Ctx = C.secp256k1_context_create(
		C.SECP256K1_CONTEXT_VERIFY | C.SECP256K1_CONTEXT_SIGN,
	)
}

// VerifyPubKeySignedHashLight verifies signature using libsecp256k1 (3-5x faster than pure Go)
func VerifyPubKeySignedHashLight(pubkey cipher.PubKey, sig cipher.Sig, hash cipher.SHA256) error {
	if len(sig) != 65 {
		return cipher.ErrInvalidLengthSig
	}
	if len(pubkey) != 33 {
		return cipher.ErrInvalidLengthPubKey
	}

	if sig[64] >= 4 {
		return cipher.ErrInvalidSig
	}

	// Parse compressed public key
	var cPubkey C.secp256k1_pubkey
	pubkeyPtr := (*C.uchar)(unsafe.Pointer(&pubkey[0]))
	pubkeyLen := C.size_t(len(pubkey))

	if C.secp256k1_ec_pubkey_parse(secp256k1Ctx, &cPubkey, pubkeyPtr, pubkeyLen) != 1 {
		return cipher.ErrInvalidSigInvalidPubKey
	}

	// Parse signature (R,S from first 64 bytes)
	var cSig C.secp256k1_ecdsa_signature
	sigPtr := (*C.uchar)(unsafe.Pointer(&sig[0]))

	if C.secp256k1_ecdsa_signature_parse_compact(secp256k1Ctx, &cSig, sigPtr) != 1 {
		return cipher.ErrInvalidSigValidity
	}

	// Verify signature
	hashPtr := (*C.uchar)(unsafe.Pointer(&hash[0]))
	if C.secp256k1_ecdsa_verify(secp256k1Ctx, &cSig, hashPtr, &cPubkey) != 1 {
		return cipher.ErrInvalidSigForMessage
	}

	return nil
}

// CleanupCGO releases the secp256k1 context (call on shutdown)
func CleanupCGO() {
	if secp256k1Ctx != nil {
		C.secp256k1_context_destroy(secp256k1Ctx)
		secp256k1Ctx = nil
	}
}

// UsingCGO returns true when CGO optimization is enabled
func UsingCGO() bool {
	return true
}
