package visorconfig

import (
	"github.com/skycoin/skywire/pkg/cipher"
)

// KeyRingTypeDeterministic is the only KeyRing.Type today: keys are derived from
// the visor secret key (the implicit seed) by a one-way HMAC chain, in the
// spirit of a skycoin deterministic wallet.
const KeyRingTypeDeterministic = "deterministic"

// KeyEntry is one deterministically-derived keypair in the visor KeyRing — the
// derivation index, a purpose label, and the public/secret key. (No skycoin
// address: the keyring's only use is the standalone-hypervisor identity keypair,
// not a payment wallet.)
type KeyEntry struct {
	Index     uint32 `json:"index"`
	Label     string `json:"label,omitempty"`
	PublicKey string `json:"public_key"`
	SecretKey string `json:"secret_key"`
}

// KeyRing is a small wallet-like set of deterministically-derived keys recorded
// in the visor config, shaped loosely like a skycoin deterministic wallet (a
// type + tracked next-index + entries with public/secret keys).
//
// The keys are derived ONE-WAY from the visor secret key (the implicit seed) via
// cipher.DeriveChildKey, so they are regenerable from the one visor key (no
// separate backup) and a leaked child can't expose the visor key. This is used
// to mint standalone-hypervisor identities (cli hv gen) deterministically
// instead of random, unrelated keys — so the standalone hypervisor is provably a
// child of the visor and recoverable.
type KeyRing struct {
	Type      string     `json:"type"`
	NextIndex uint32     `json:"next_index"`
	Entries   []KeyEntry `json:"entries,omitempty"`
}

// deriveKeyEntry derives (read-only, no mutation) the entry for label+index from
// the visor secret key.
func (v1 *V1) deriveKeyEntry(label string, index uint32) (KeyEntry, error) {
	pk, sk, err := cipher.DeriveChildKey(v1.SK, label, index)
	if err != nil {
		return KeyEntry{}, err
	}
	return KeyEntry{
		Index:     index,
		Label:     label,
		PublicKey: pk.Hex(),
		SecretKey: sk.Hex(),
	}, nil
}

// DeriveKeyEntry derives the keyring entry for label+index from the visor secret
// key, deterministic and without mutating the config — for regenerating a known
// entry (same input → same key).
func (v1 *V1) DeriveKeyEntry(label string, index uint32) (KeyEntry, error) {
	v1.mu.RLock()
	defer v1.mu.RUnlock()
	return v1.deriveKeyEntry(label, index)
}

// MintKey derives the NEXT keyring entry (at KeyRing.NextIndex) for the given
// label, appends it, and advances NextIndex — the wallet "derive a new address"
// operation. The caller persists with Flush(). Returns the minted entry.
func (v1 *V1) MintKey(label string) (KeyEntry, error) {
	v1.mu.Lock()
	defer v1.mu.Unlock()
	if v1.KeyRing == nil {
		v1.KeyRing = &KeyRing{Type: KeyRingTypeDeterministic}
	}
	entry, err := v1.deriveKeyEntry(label, v1.KeyRing.NextIndex)
	if err != nil {
		return KeyEntry{}, err
	}
	v1.KeyRing.Entries = append(v1.KeyRing.Entries, entry)
	v1.KeyRing.NextIndex++
	return entry, nil
}
