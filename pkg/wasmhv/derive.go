// Package wasmhv pkg/wasmhv/derive.go c3-vis-wasm
package wasmhv

import (
	"github.com/skycoin/skywire/pkg/cipher"
)

// StandaloneKeyLabel namespaces the standalone-hypervisor key derivation so the
// seed can't collide with any other use of the parent key. It is the label
// passed to cipher.DeriveChildKey; a visor KeyRing minting standalone-HV
// identities uses the same label so its entries match DeriveStandaloneKey.
const StandaloneKeyLabel = "skywire-standalone-hypervisor:v1:"

// DeriveStandaloneKey deterministically derives a standalone-hypervisor keypair
// from the parent (visor / hypervisor) secret key.
//
// The derivation is ONE-WAY: the seed is HMAC-SHA256(parentSK, label||index), a
// PRF whose output reveals nothing about parentSK. So a compromised standalone
// key (e.g. a cracked password on a generated hypervisor.html) can never be used
// to recover the parent — the worst case is "remove that one PK from your
// visors' hypervisors[]".
//
// It is also deterministic and indexed: the same parentSK + index always yields
// the same keypair (regenerable from the one root key — no separate secret to
// back up), and distinct indices mint distinct standalone identities (one per
// device / tab) from the single root.
func DeriveStandaloneKey(parentSK cipher.SecKey, index uint32) (cipher.PubKey, cipher.SecKey, error) {
	return cipher.DeriveChildKey(parentSK, StandaloneKeyLabel, index)
}
