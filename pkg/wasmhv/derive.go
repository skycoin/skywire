package wasmhv

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"github.com/skycoin/skywire/pkg/cipher"
)

// deriveLabel namespaces the standalone-hypervisor key derivation so the seed
// can't collide with any other use of the parent key.
const deriveLabel = "skywire-standalone-hypervisor:v1:"

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
	if parentSK.Null() {
		return cipher.PubKey{}, cipher.SecKey{}, fmt.Errorf("derive standalone key: parent secret key is null")
	}
	var idx [4]byte
	binary.BigEndian.PutUint32(idx[:], index)

	mac := hmac.New(sha256.New, parentSK[:])
	_, _ = mac.Write([]byte(deriveLabel)) //nolint:errcheck // hash.Write never errors
	_, _ = mac.Write(idx[:])              //nolint:errcheck
	seed := mac.Sum(nil)

	pk, sk, err := cipher.GenerateDeterministicKeyPair(seed)
	if err != nil {
		return cipher.PubKey{}, cipher.SecKey{}, fmt.Errorf("derive standalone key: %w", err)
	}
	return pk, sk, nil
}
