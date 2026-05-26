// Package keypair is a thin, WASM-safe wrapper around
// pkg/cipher.GenerateKeyPair. It exists so external consumers
// (TinyGo WASM, doc generators) that need to spin up a fresh visor
// identity don't have to import the full cipher package surface or
// understand its hex-formatting conventions.
//
// Security note: client-side keygen in a browser WASM is safe ONLY
// when the secret key never leaves the page. The render layer must
// not exfiltrate the SK over the network or place it in a clipboard
// the user might paste publicly. The Generate function returns the
// SK as a plain string — protecting it is the caller's job.
package keypair

import (
	"github.com/skycoin/skywire/pkg/cipher"
)

// Generate creates a fresh skywire-format keypair and returns
// (pkHex, skHex) — the same lowercase-hex format every other
// skywire surface accepts. PK is 66 chars (33-byte compressed
// secp256k1 point); SK is 64 chars (32-byte scalar).
//
// Entropy comes from crypto/rand on standard Go targets and from
// crypto.getRandomValues via the WASM runtime on js/wasm — both
// suitable for production identities.
func Generate() (pkHex, skHex string) {
	pk, sk := cipher.GenerateKeyPair()
	return pk.Hex(), sk.Hex()
}

// FromSecretKey derives the public key for an existing hex secret
// key. Returns "" + a non-nil error when skHex is malformed (wrong
// length, non-hex characters, or invalid scalar). Used by config
// flows where the operator pastes an existing SK and the form needs
// to display the corresponding PK as a sanity check.
func FromSecretKey(skHex string) (pkHex string, err error) {
	var sk cipher.SecKey
	if err := sk.Set(skHex); err != nil {
		return "", err
	}
	pk, err := sk.PubKey()
	if err != nil {
		return "", err
	}
	return pk.Hex(), nil
}
