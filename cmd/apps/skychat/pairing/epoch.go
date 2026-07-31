// Package pairing cmd/apps/skychat/pairing/epoch.go c4-app-chat
// forward-secret epoch keys for a chat pair.
//
// # What was wrong with one key per pair
//
// crypto.go derives the pair key as ECDH(my_sk, peer_pk). That is a pure
// function of two long-term identity keys, so it is the SAME key for
// every message the pair will ever exchange, and it can be recomputed
// from either side's identity secret at any point in the future. The
// consequences are worse than "no forward secrecy" in the abstract:
//
//   - Pair feeds are CXO trees. Every ciphertext ever published stays in
//     the tree and is served to anyone the allowlist admits. An observer
//     only has to capture the feed once.
//   - A visor's identity secret sits in its config file and is the same
//     key used for dmsg, transports, and RPC auth — a broad surface with
//     many ways to leak that have nothing to do with chat.
//   - Put together: leaking that one file, ever, retroactively decrypts
//     every DM the visor ever sent or received. Rotating it doesn't help
//     either, because the old key still opens the old feed.
//
// # Epochs
//
// Both sides now publish a short-lived RATCHET public key on their own
// feed (ratchet.go) and re-publish it periodically. The key for an epoch
// is derived from the two current ratchet keys:
//
//	epochSecret = ECDH(my_ratchet_sk, peer_ratchet_pk)
//	epochKey    = HKDF-SHA256(epochSecret, salt=epochID, info="skychat:pair-epoch:v1")
//
// ECDH's symmetry gives both sides the same secret from their own secret
// half and the other's public half, exactly as before — but now neither
// half is a long-term identity key. When a side rotates its ratchet key
// it DELETES the old secret, and from that moment the epochs that used it
// cannot be re-derived by anyone, including the parties themselves. The
// identity keys are no longer sufficient to open past traffic, which is
// the whole point.
//
// This is the DH half of a Double Ratchet and not the symmetric half:
// there is no per-message chain key, so within one epoch a compromise
// still reveals that epoch. Bounding an epoch is what ratchetMaxAge and
// ratchetMaxMessages are for. A per-message chain would need in-order
// delivery guarantees the CXO feed does not offer (leaves arrive in tree
// order after a resync, not send order), so the epoch is the unit here.
//
// # Why HKDF here and not in crypto.go
//
// The legacy path used the raw ECDH output as the AEAD key, justified by
// there being exactly one use per derived secret. That stops being true
// with epochs: the same (my_ratchet, peer_ratchet) pair identifies an
// epoch, and mixing the epoch ID in through a KDF is what keeps the key
// bound to it. It also gives a versioned info string, so a future change
// of construction is a different key rather than a silent reinterpretation
// of the same bytes.
package pairing

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"

	skycipher "github.com/skycoin/skycoin/src/cipher"
	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"

	"github.com/skycoin/skywire/pkg/cipher"
)

// epochKDFInfo is the HKDF info string. Versioned so a future change to
// the derivation is a distinct key rather than a reinterpretation of the
// same secret.
const epochKDFInfo = "skychat:pair-epoch:v1"

// EpochIDLen is the length of the epoch identifier carried on every
// message.
//
// Eight bytes: long enough that two live epochs of one pair colliding is
// not a thing that happens (a pair would need ~2^32 epochs for an even
// chance, against a ring that holds 256), short enough to be cheap on
// every leaf. It is not a security parameter — a collision costs one
// failed AEAD open and a fall through to the next candidate key, because
// the tag is what actually authenticates.
const EpochIDLen = 8

// EpochID names one (my ratchet key, peer ratchet key) combination.
type EpochID [EpochIDLen]byte

// String renders the ID as hex, for logs and for the operator-facing
// epoch label in the UI.
func (e EpochID) String() string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 0, EpochIDLen*2)
	for _, b := range e {
		out = append(out, hexdigits[b>>4], hexdigits[b&0x0f])
	}
	return string(out)
}

// IsZero reports whether this is the zero ID, which is what a legacy
// (pre-ratchet) message carries.
func (e EpochID) IsZero() bool { return e == EpochID{} }

// MarshalText / UnmarshalText render an EpochID as hex wherever
// encoding/json touches one, so a persisted ring reads as
// "3f1c…" instead of the array of 8 integers a [8]byte marshals to by
// default. Same form the wire envelope carries, so there is one
// rendering of an epoch across logs, disk, and the feed.
func (e EpochID) MarshalText() ([]byte, error) { return []byte(e.String()), nil }

// UnmarshalText parses the hex form.
func (e *EpochID) UnmarshalText(b []byte) error {
	id, err := parseEpochID(string(b))
	if err != nil {
		return err
	}
	*e = id
	return nil
}

// computeEpochID derives the stable identifier for the epoch formed by
// two ratchet public keys.
//
// Sorted before hashing so both sides compute the same ID without having
// to agree on who is "first" — the two ends see the same pair of keys in
// opposite roles (mine/theirs), and any ordering rule based on role would
// give them different IDs for the same epoch.
func computeEpochID(a, b cipher.PubKey) EpochID {
	h := sha256.New()
	first, second := a, b
	if bytes.Compare(a[:], b[:]) > 0 {
		first, second = b, a
	}
	_, _ = h.Write([]byte(epochKDFInfo)) //nolint:errcheck // hash.Write never errors
	_, _ = h.Write(first[:])             //nolint:errcheck
	_, _ = h.Write(second[:])            //nolint:errcheck
	var id EpochID
	copy(id[:], h.Sum(nil))
	return id
}

// deriveEpochKey computes the symmetric key for the epoch formed by our
// ratchet secret and the peer's ratchet public key, along with that
// epoch's ID.
//
// Symmetric in exactly the way the legacy pair key was: the peer calls
// this with ITS ratchet secret and OUR ratchet public key and gets the
// same 32 bytes, because ECDH(a_sk, b_pk) == ECDH(b_sk, a_pk).
func deriveEpochKey(myRatchetSK cipher.SecKey, myRatchetPK, peerRatchetPK cipher.PubKey) (EpochID, pairKey, error) {
	if peerRatchetPK == (cipher.PubKey{}) {
		return EpochID{}, nil, errNoPeerRatchet
	}
	shared, err := skycipher.ECDH(skycipher.PubKey(peerRatchetPK), skycipher.SecKey(myRatchetSK))
	if err != nil {
		return EpochID{}, nil, fmt.Errorf("pairing: epoch: derive ECDH: %w", err)
	}
	id := computeEpochID(myRatchetPK, peerRatchetPK)
	key := make([]byte, chacha20poly1305.KeySize)
	// Salting with the epoch ID binds the key to the exact pair of
	// ratchet keys it came from, so a peer that replays an old ratchet
	// announcement cannot make us reuse a key under a new ID.
	r := hkdf.New(sha256.New, shared, id[:], []byte(epochKDFInfo))
	if _, err := io.ReadFull(r, key); err != nil {
		return EpochID{}, nil, fmt.Errorf("pairing: epoch: hkdf: %w", err)
	}
	return id, key, nil
}
