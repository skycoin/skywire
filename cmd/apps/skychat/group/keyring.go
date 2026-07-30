// Package group cmd/apps/skychat/group/keyring.go c4-app-chat
// the group's key history: which key encrypts new messages, and which
// older keys still have to open old ones.
//
// # Why a ring rather than a key
//
// A ban used to mean "loses network access": the PK lost its allowlist
// seat, left the roster, and was refused at the join gate. What it did
// NOT lose was the group key, which it had held since the day it was
// admitted. Anyone who kept a copy of the feed — or who could still
// reach any member's publisher through a third party — could keep
// reading everything published afterwards. The eviction was a perimeter
// control with the key still inside the perimeter.
//
// Rotating on eviction closes that: a fresh key is generated, sealed
// individually to each REMAINING member (see keyrotate.go), and used for
// every message from then on. The evicted PK is not a recipient, so its
// copy of the old key opens nothing new.
//
// That immediately creates the second problem this file exists for. If a
// visor held only the current key, rotation would erase the group's
// history for everyone at once — every message published before the
// rotation would stop opening. So a visor keeps every key it has ever
// held: KeyEpoch/AESKey is what encrypts, and KeyRing is what still
// decrypts. Forward-only, exactly like a mute: a restriction takes away
// what comes next, it does not rewrite what already happened.
//
// # Epochs, not versions
//
// Keys are numbered by epoch. Epoch 0 is the key a group was created
// with (and the one every record written before rotation existed
// carries, which is why the zero value is meaningful and needs no
// migration). Each rotation increments. Two admins rotating at the same
// moment can mint the same epoch number with different keys; both leaves
// are valid and every visor ends up holding both keys, so nothing
// becomes unreadable. Which one wins as "current" is settled by the
// replay guard's last-writer-wins rule on IssuedAt — see applyKeyLeaf.
package group

import (
	"bytes"
	"time"
)

// maxKeyRing caps how many superseded keys a record keeps.
//
// A bound is needed because the ring is attacker-influenced: an admin
// can rotate as often as it likes, and every rotation is another 32
// bytes plus a timestamp on every member's record forever. 32 is far
// more rotations than a group sees in practice (one per eviction) while
// keeping the record small.
//
// The cost of the bound is real and worth stating: once a group has
// rotated more than 32 times, messages encrypted under the oldest keys
// stop opening. That is a deliberate trade — bounded storage for
// unbounded-in-principle history — and it degrades gradually from the
// oldest end rather than failing all at once.
const maxKeyRing = 32

// GroupKey is one retired group key, kept so old messages still open.
type GroupKey struct {
	// Epoch is the key's generation. Matches the KeyMutation that
	// distributed it, or 0 for the group's create-time key.
	Epoch uint64 `json:"epoch"`

	// Key is the 32-byte AES-256-GCM key.
	Key []byte `json:"key"`

	// AddedAt is when THIS visor learned the key, not when it was
	// issued. Only used to order the ring for eviction, so a peer with a
	// skewed clock cannot make us drop a key we still need.
	AddedAt time.Time `json:"added_at"`
}

// KeyState is a snapshot of a record's key material, the shape the
// session hands to the Manager for persistence. Parallel to ModState.
type KeyState struct {
	Epoch   uint64
	Key     []byte
	KeyRing []GroupKey
}

// keyStateOf builds a snapshot from a Record.
func keyStateOf(r Record) KeyState {
	return KeyState{
		Epoch:   r.KeyEpoch,
		Key:     append([]byte(nil), r.AESKey...),
		KeyRing: copyKeyRing(r.KeyRing),
	}
}

// applyTo writes the snapshot onto a Record.
func (k KeyState) applyTo(r *Record) {
	r.KeyEpoch = k.Epoch
	r.AESKey = append([]byte(nil), k.Key...)
	r.KeyRing = copyKeyRing(k.KeyRing)
}

func copyKeyRing(in []GroupKey) []GroupKey {
	if len(in) == 0 {
		return nil
	}
	out := make([]GroupKey, len(in))
	for i, k := range in {
		out[i] = GroupKey{Epoch: k.Epoch, Key: append([]byte(nil), k.Key...), AddedAt: k.AddedAt}
	}
	return out
}

// installKey adds a key to the record, promoting it to current when it
// supersedes what we hold. Reports whether anything changed.
//
// current is decided by epoch first and issue time second — the same
// order applyKeyLeaf's freshness gate uses, so a same-epoch race between
// two admins converges to one answer everywhere. A key that loses that
// comparison is still ADDED to the ring: the admin that minted it may
// already have published messages under it, and dropping the key would
// make those messages permanently unreadable on this visor while
// remaining readable on the admin's own.
//
// at is when we learned the key (used only for ring ordering).
func (r *Record) installKey(epoch uint64, key []byte, issuedAt, at time.Time) bool {
	if len(key) != 32 {
		return false
	}
	if r.holdsKey(epoch, key) {
		return false
	}
	supersedes := epoch > r.KeyEpoch ||
		(epoch == r.KeyEpoch && len(r.AESKey) == 32 && issuedAt.After(r.KeyIssuedAt))
	if !supersedes {
		// Loser branch, or a key from before our current epoch: keep it
		// for decryption only.
		r.ringPush(GroupKey{Epoch: epoch, Key: append([]byte(nil), key...), AddedAt: at.UTC()})
		return true
	}
	if len(r.AESKey) == 32 {
		r.ringPush(GroupKey{Epoch: r.KeyEpoch, Key: append([]byte(nil), r.AESKey...), AddedAt: at.UTC()})
	}
	r.AESKey = append([]byte(nil), key...)
	r.KeyEpoch = epoch
	r.KeyIssuedAt = issuedAt.UTC()
	return true
}

// holdsKey reports whether this exact (epoch, key) pair is already known,
// as the current key or in the ring. Compared on the key bytes as well as
// the epoch so two admins' same-numbered keys are both recognized as new.
func (r Record) holdsKey(epoch uint64, key []byte) bool {
	if r.KeyEpoch == epoch && bytes.Equal(r.AESKey, key) {
		return true
	}
	for _, k := range r.KeyRing {
		if k.Epoch == epoch && bytes.Equal(k.Key, key) {
			return true
		}
	}
	return false
}

// ringPush prepends to the ring (newest first) and trims to maxKeyRing,
// dropping the oldest.
func (r *Record) ringPush(k GroupKey) {
	r.KeyRing = append([]GroupKey{k}, r.KeyRing...)
	if len(r.KeyRing) > maxKeyRing {
		r.KeyRing = r.KeyRing[:maxKeyRing]
	}
}

// DecryptionKeys returns every key this visor can try, current first then
// the ring newest-first.
//
// Ordering is the whole optimization: steady-state traffic is encrypted
// under the current key, so the first attempt succeeds and the ring is
// never touched. Only a message from before a rotation — or from a peer
// that hasn't applied the rotation yet — walks further.
//
// Trying keys in turn is sound because AES-GCM is authenticated: a wrong
// key fails the tag check rather than yielding plausible garbage. It also
// means no key-epoch hint has to ride on Message, which keeps the signed
// canonical byte layout in signing.go untouched — an epoch field there
// would have invalidated every signature already on a feed.
func (r Record) DecryptionKeys() [][]byte {
	out := make([][]byte, 0, 1+len(r.KeyRing))
	if len(r.AESKey) == 32 {
		out = append(out, r.AESKey)
	}
	for _, k := range r.KeyRing {
		if len(k.Key) == 32 {
			out = append(out, k.Key)
		}
	}
	return out
}

// decryptWithRing opens a message body with the first key that works.
func decryptWithRing(keys [][]byte, ciphertext, nonce []byte) ([]byte, error) {
	var firstErr error
	for _, k := range keys {
		pt, err := Decrypt(k, ciphertext, nonce)
		if err == nil {
			return pt, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr == nil {
		firstErr = errNoGroupKey
	}
	return nil, firstErr
}
