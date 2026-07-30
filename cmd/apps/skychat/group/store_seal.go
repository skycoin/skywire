// Package group cmd/apps/skychat/group/store_seal.go c4-app-chat
// encryption at rest for the one part of a group record that is actually
// a secret: its key material.
//
// # What this fixes
//
// groups.db held every private group's AES key as plaintext base64 in a
// JSON record. Any read of that file — a backup, a synced folder, a
// support bundle, a stale volume on a decommissioned host, another
// process running as the same user — handed over the ability to decrypt
// every message on every private group's feed, past and future.
//
// That defeats the controls built on top of it. Rotating the key on
// eviction (keyrotate.go) buys nothing if the new key is written back in
// the clear a millisecond later: whoever can read the file just reads the
// new one. The same goes for any future control that treats the group key
// as the thing being protected.
//
// So the key fields are sealed before they are marshaled and opened after
// they are unmarshaled. Everything else in the record — roster, admins,
// moderation state, timestamps — stays readable, deliberately: none of it
// is secret from someone holding the file, all of it converges publicly
// through signed gossip anyway, and keeping it queryable is what makes
// the store usable.
//
// # What it does NOT fix, stated plainly
//
// The sealing key is derived from the visor's own secret key, and that
// secret key lives in the visor's config file on the same disk. An
// attacker who takes BOTH files can derive the sealing key and open
// everything. This is not a substitute for disk encryption and it is not
// a passphrase.
//
// What it does buy is that groups.db alone is inert, and groups.db is the
// file that travels: it gets copied into backups, attached to bug
// reports, left behind in container volumes, and synced to places the
// config never goes. Raising the requirement from "read one file" to
// "read two specific files on the same host" is a real reduction in
// exposure, and it is the most that can be done without asking an
// operator for a passphrase on every visor start. A passphrase- or
// keychain-derived key would fit behind exactly this interface.
//
// # Construction
//
// HKDF-SHA256 over the visor secret key with a fixed info string, then
// ChaCha20-Poly1305 per field with a fresh random nonce prepended —
// the same AEAD the pairing feeds and the key wraps use, so there is one
// sealing idiom in this app rather than three.
//
// Each sealed blob is bound to where it belongs with AEAD additional
// data: the group ID for the current key, and group ID plus epoch for a
// ring entry. Without that binding, someone with write access to the file
// could move a sealed blob from one group's record into another's — or
// from one ring slot to another — and the store would open it happily,
// silently swapping which key a group encrypts with.
package group

import (
	stdcipher "crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"

	"github.com/skycoin/skywire/pkg/cipher"
)

// storeSealInfo is the HKDF info string. Versioned so a future change of
// construction can be told apart from this one rather than producing
// undecryptable records.
const storeSealInfo = "skychat:group-store-seal:v1"

// ErrStoreSealKeyRequired is returned by OpenStore when handed a zero
// secret key. Refusing is deliberate: silently falling back to plaintext
// keys on disk is exactly the state this file exists to end, and a caller
// with no identity to derive from has a configuration bug rather than a
// weaker security posture.
var ErrStoreSealKeyRequired = errors.New("group: store: a visor secret key is required to seal group keys at rest")

// recordSealer seals and opens the key material in a Record.
type recordSealer struct {
	aead stdcipher.AEAD
}

// newRecordSealer derives the at-rest key from the visor's secret key.
//
// Deterministic — the same visor reopening the same file derives the same
// key, with nothing extra to store or back up. The flip side is that the
// records are bound to that identity: a groups.db from another visor
// cannot be opened, which is the intended behavior rather than a
// limitation to work around.
func newRecordSealer(sk cipher.SecKey) (*recordSealer, error) {
	if sk == (cipher.SecKey{}) {
		return nil, ErrStoreSealKeyRequired
	}
	key := make([]byte, chacha20poly1305.KeySize)
	kdf := hkdf.New(sha256.New, sk[:], nil, []byte(storeSealInfo))
	if _, err := kdf.Read(key); err != nil {
		return nil, fmt.Errorf("group: store: derive seal key: %w", err)
	}
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, fmt.Errorf("group: store: aead init: %w", err)
	}
	return &recordSealer{aead: aead}, nil
}

// seal returns [nonce | ct+tag] over plain, authenticated with aad.
func (s *recordSealer) seal(plain, aad []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("group: store: seal nonce: %w", err)
	}
	out := make([]byte, s.aead.NonceSize(), s.aead.NonceSize()+len(plain)+s.aead.Overhead())
	copy(out, nonce)
	return s.aead.Seal(out, nonce, plain, aad), nil
}

// open is the inverse. A failure here means the file was written by a
// different visor, or tampered with, or a blob was moved out of the slot
// it was sealed for.
func (s *recordSealer) open(sealed, aad []byte) ([]byte, error) {
	if len(sealed) < s.aead.NonceSize()+s.aead.Overhead() {
		return nil, errors.New("group: store: sealed key blob too short")
	}
	pt, err := s.aead.Open(nil, sealed[:s.aead.NonceSize()], sealed[s.aead.NonceSize():], aad)
	if err != nil {
		return nil, fmt.Errorf("group: store: open sealed key: %w", err)
	}
	return pt, nil
}

// sealAAD binds a blob to its slot: the group it belongs to, and for a
// ring entry, which epoch.
func sealAAD(groupID string, epoch uint64, current bool) []byte {
	if current {
		return []byte("cur|" + groupID)
	}
	return []byte("ring|" + groupID + "|" + strconv.FormatUint(epoch, 10))
}

// encodeRecord is the ONLY way a record reaches the bytes a store writes.
// It moves the key material into the sealed fields, clears the plaintext
// ones so `omitempty` keeps them out of the JSON entirely, and marshals.
//
// Routing every write through here — Put and the update() setters alike —
// is what makes "no plaintext key on disk" a property of the store rather
// than a discipline each method has to remember.
func (s *Store) encodeRecord(r Record) ([]byte, error) {
	if s.sealer == nil {
		// Only reachable if a Store was constructed without OpenStore.
		return nil, ErrStoreSealKeyRequired
	}
	if len(r.AESKey) > 0 {
		sealed, err := s.sealer.seal(r.AESKey, sealAAD(r.ID, r.KeyEpoch, true))
		if err != nil {
			return nil, err
		}
		r.AESKeySealed = sealed
		r.AESKey = nil
	}
	if len(r.KeyRing) > 0 {
		ring := make([]GroupKey, len(r.KeyRing))
		for i, k := range r.KeyRing {
			ring[i] = GroupKey{Epoch: k.Epoch, AddedAt: k.AddedAt, Sealed: k.Sealed}
			if len(k.Key) == 0 {
				continue
			}
			sealed, err := s.sealer.seal(k.Key, sealAAD(r.ID, k.Epoch, false))
			if err != nil {
				return nil, err
			}
			ring[i].Sealed = sealed
		}
		r.KeyRing = ring
	}
	return json.Marshal(&r)
}

// decodeRecord unmarshals and restores the plaintext key material.
//
// Legacy records — written before sealing existed — carry the plaintext
// field and no sealed one. They are accepted as-is: refusing would lock
// an operator out of their own groups on upgrade. They stop being
// plaintext the next time anything writes them, and OpenStore re-seals
// the whole file on open so "the next write" is normally immediate.
//
// A sealed blob that will not open is NOT fatal to the record. The roster
// and moderation state are still worth having, and a private group whose
// key is unreadable surfaces as "cannot decrypt" rather than as a group
// that vanished — which is the more diagnosable failure. It is logged by
// the caller.
func (s *Store) decodeRecord(raw []byte, out *Record) error {
	var r Record
	if err := json.Unmarshal(raw, &r); err != nil {
		return err
	}
	if s.sealer == nil {
		return ErrStoreSealKeyRequired
	}
	var firstErr error
	if len(r.AESKeySealed) > 0 {
		plain, err := s.sealer.open(r.AESKeySealed, sealAAD(r.ID, r.KeyEpoch, true))
		if err != nil {
			firstErr = err
		} else {
			r.AESKey = plain
		}
		r.AESKeySealed = nil
	}
	for i, k := range r.KeyRing {
		if len(k.Sealed) == 0 {
			continue
		}
		plain, err := s.sealer.open(k.Sealed, sealAAD(r.ID, k.Epoch, false))
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		r.KeyRing[i].Key = plain
		r.KeyRing[i].Sealed = nil
	}
	*out = r
	return firstErr
}

// hasPlaintextKeys reports whether a record still carries key material in
// the clear — the migration predicate.
func hasPlaintextKeys(r Record) bool {
	if len(r.AESKey) > 0 && len(r.AESKeySealed) == 0 {
		return true
	}
	for _, k := range r.KeyRing {
		if len(k.Key) > 0 && len(k.Sealed) == 0 {
			return true
		}
	}
	return false
}
