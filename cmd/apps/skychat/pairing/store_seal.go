// Package pairing cmd/apps/skychat/pairing/store_seal.go c4-app-chat
// sealing a pair's ratchet secrets at rest.
//
// The pair store gained something worth protecting when the ratchet
// landed. Before it, a Record held only metadata — peer PK, port, status,
// timestamps — and the one secret in the system (the static pair key) was
// never stored at all because it could be recomputed from the visor's
// identity key on demand. Recomputable was the problem the ratchet exists
// to fix, and the fix means the secrets now have to live somewhere.
//
// What is at stake if they are stored in the clear: the current ratchet
// SECRET (opens the current epoch, and every future one until the next
// rotation) and the epoch-key ring (opens up to ratchetRingCap epochs of
// feed history). Writing those as plaintext JSON would hand an attacker
// with read access to pairs.db exactly what the ratchet was built to deny
// them, without needing the identity key at all — a strictly worse
// position than before the feature.
//
// Same construction as the group store (cmd/apps/skychat/group/
// store_seal.go), deliberately, so there is one at-rest idiom in this app:
// HKDF-SHA256 from the visor's secret key into a ChaCha20-Poly1305 key,
// per-record AAD binding a blob to the slot it belongs in, and a
// best-effort open so a record whose secrets won't decrypt still loads
// its metadata.
//
// Same honest limit, too: the sealing key derives from the visor SK,
// which lives in the config file on the same disk. This raises the bar
// from "read one file" to "read two specific files on the same host". It
// is not disk encryption and not a passphrase.
package pairing

import (
	gocipher "crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"

	"github.com/skycoin/skywire/pkg/cipher"
)

// storeSealInfo is the HKDF info string. Versioned, and distinct from the
// group store's, so the two stores never derive the same key from the
// same identity.
const storeSealInfo = "skychat:pair-store-seal:v1"

// ErrStoreSealKeyRequired is returned by OpenStore when handed a zero
// secret key. Refusing is the point: silently falling back to plaintext
// would leave the ratchet secrets exposed while the caller believes they
// are sealed.
var ErrStoreSealKeyRequired = errors.New("pairing: OpenStore: a visor secret key is required to seal pair secrets at rest")

// PersistedEpoch is one epoch key as stored. Key is plaintext in memory
// and cleared in favor of Sealed on disk.
type PersistedEpoch struct {
	ID      EpochID   `json:"id"`
	Key     pairKey   `json:"key,omitempty"`
	Sealed  []byte    `json:"key_sealed,omitempty"`
	AddedAt time.Time `json:"added_at"`
}

// RatchetState is the persisted form of a pair's ratchet. Lives on the
// Record so a restart resumes on the same epoch.
//
// Every field is `json:"-"`: the wire form is ratchetStateJSON below,
// via MarshalJSON. Two reasons, and the first is a correctness bug
// rather than taste. cipher.PubKey and cipher.SecKey are fixed-size
// ARRAYS, so `omitempty` does nothing for them — a zero SecKey (which is
// exactly what RatchetSK holds after sealing clears it) marshals to
// sixty-four zeroes and then FAILS to unmarshal with "Invalid secret
// key", taking the whole record with it. The second: routing through an
// explicit wire struct makes it structurally impossible for the
// plaintext secret to reach the file, because there is no field for it
// there at all.
type RatchetState struct {
	// Generation / RatchetPK / RatchetSK are our current ratchet
	// keypair. RatchetSK is sealed on disk (RatchetSKSealed).
	Generation uint64        `json:"-"`
	RatchetPK  cipher.PubKey `json:"-"`
	RatchetSK  cipher.SecKey `json:"-"`

	// RatchetSKSealed is the on-disk form of RatchetSK.
	RatchetSKSealed []byte `json:"-"`

	// PeerGeneration / PeerRatchetPK track the newest announcement we
	// accepted from the peer. Public data — not sealed.
	PeerGeneration uint64        `json:"-"`
	PeerRatchetPK  cipher.PubKey `json:"-"`

	// Current is the epoch Send seals under.
	Current EpochID `json:"-"`

	// Ring holds the epoch keys we can still open history with.
	Ring []PersistedEpoch `json:"-"`

	RotatedAt time.Time `json:"-"`
	Sealed    uint64    `json:"-"`
}

// ratchetStateJSON is the on-disk shape. Public keys are hex strings so
// an unset one is an empty string that omitempty actually drops, and
// there is deliberately no field for the plaintext secret.
type ratchetStateJSON struct {
	Generation      uint64           `json:"generation,omitempty"`
	RatchetPK       string           `json:"ratchet_pk,omitempty"`
	RatchetSKSealed []byte           `json:"ratchet_sk_sealed,omitempty"`
	PeerGeneration  uint64           `json:"peer_generation,omitempty"`
	PeerRatchetPK   string           `json:"peer_ratchet_pk,omitempty"`
	Current         string           `json:"current,omitempty"`
	Ring            []PersistedEpoch `json:"ring,omitempty"`
	RotatedAt       time.Time        `json:"rotated_at,omitempty"`
	Sealed          uint64           `json:"sealed,omitempty"`
}

func hexPK(pk cipher.PubKey) string {
	if pk == (cipher.PubKey{}) {
		return ""
	}
	return pk.Hex()
}

func parsePK(s string) (cipher.PubKey, error) {
	if s == "" {
		return cipher.PubKey{}, nil
	}
	var pk cipher.PubKey
	if err := pk.Set(s); err != nil {
		return cipher.PubKey{}, err
	}
	return pk, nil
}

// MarshalJSON writes the wire shape.
func (r RatchetState) MarshalJSON() ([]byte, error) {
	out := ratchetStateJSON{
		Generation:      r.Generation,
		RatchetPK:       hexPK(r.RatchetPK),
		RatchetSKSealed: r.RatchetSKSealed,
		PeerGeneration:  r.PeerGeneration,
		PeerRatchetPK:   hexPK(r.PeerRatchetPK),
		Ring:            r.Ring,
		RotatedAt:       r.RotatedAt,
		Sealed:          r.Sealed,
	}
	if !r.Current.IsZero() {
		out.Current = r.Current.String()
	}
	return json.Marshal(out)
}

// UnmarshalJSON reads it back.
func (r *RatchetState) UnmarshalJSON(b []byte) error {
	var in ratchetStateJSON
	if err := json.Unmarshal(b, &in); err != nil {
		return err
	}
	pk, err := parsePK(in.RatchetPK)
	if err != nil {
		return fmt.Errorf("pairing: ratchet state: ratchet_pk: %w", err)
	}
	peerPK, err := parsePK(in.PeerRatchetPK)
	if err != nil {
		return fmt.Errorf("pairing: ratchet state: peer_ratchet_pk: %w", err)
	}
	var current EpochID
	if in.Current != "" {
		if current, err = parseEpochID(in.Current); err != nil {
			return fmt.Errorf("pairing: ratchet state: current: %w", err)
		}
	}
	*r = RatchetState{
		Generation:      in.Generation,
		RatchetPK:       pk,
		RatchetSKSealed: in.RatchetSKSealed,
		PeerGeneration:  in.PeerGeneration,
		PeerRatchetPK:   peerPK,
		Current:         current,
		Ring:            in.Ring,
		RotatedAt:       in.RotatedAt,
		Sealed:          in.Sealed,
	}
	return nil
}

// recordSealer seals and opens the secret fields of a pair Record.
type recordSealer struct {
	aead gocipher.AEAD
}

// newRecordSealer derives the at-rest key from the visor's secret key.
// Deterministic per identity: nothing extra to back up, and a pairs.db
// copied off one visor will not open on another.
func newRecordSealer(sk cipher.SecKey) (*recordSealer, error) {
	if sk == (cipher.SecKey{}) {
		return nil, ErrStoreSealKeyRequired
	}
	key := make([]byte, chacha20poly1305.KeySize)
	r := hkdf.New(sha256.New, sk[:], nil, []byte(storeSealInfo))
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, fmt.Errorf("pairing: store seal: hkdf: %w", err)
	}
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, fmt.Errorf("pairing: store seal: aead: %w", err)
	}
	return &recordSealer{aead: aead}, nil
}

// seal returns [nonce | ct+tag] over plaintext, bound to aad.
func (s *recordSealer) seal(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("pairing: store seal: nonce: %w", err)
	}
	out := make([]byte, len(nonce), len(nonce)+len(plaintext)+s.aead.Overhead())
	copy(out, nonce)
	return s.aead.Seal(out, nonce, plaintext, aad), nil
}

// open is the inverse.
func (s *recordSealer) open(sealed, aad []byte) ([]byte, error) {
	if len(sealed) < s.aead.NonceSize()+s.aead.Overhead() {
		return nil, errors.New("pairing: store seal: sealed blob too short")
	}
	pt, err := s.aead.Open(nil, sealed[:s.aead.NonceSize()], sealed[s.aead.NonceSize():], aad)
	if err != nil {
		return nil, fmt.Errorf("pairing: store seal: open: %w", err)
	}
	return pt, nil
}

// sealAAD binds a blob to the exact slot it belongs in, so a sealed value
// cannot be moved between pairs or between ring entries — a swap that
// would otherwise be undetectable, since every blob is sealed under the
// same key.
func sealAAD(peerPK cipher.PubKey, slot string) []byte {
	return []byte(slot + "|" + peerPK.Hex())
}

// encodeRecord marshals r with every secret sealed and its plaintext
// counterpart cleared, so no secret reaches the JSON.
func (s *recordSealer) encodeRecord(r Record) ([]byte, error) {
	if r.Ratchet != nil {
		rt := *r.Ratchet
		if rt.RatchetSK != (cipher.SecKey{}) {
			sealed, err := s.seal(rt.RatchetSK[:], sealAAD(r.PeerPK, "ratchet-sk"))
			if err != nil {
				return nil, err
			}
			rt.RatchetSKSealed = sealed
			rt.RatchetSK = cipher.SecKey{}
		}
		ring := make([]PersistedEpoch, 0, len(rt.Ring))
		for _, e := range rt.Ring {
			if len(e.Key) == 0 {
				ring = append(ring, e)
				continue
			}
			sealed, err := s.seal(e.Key, sealAAD(r.PeerPK, "epoch|"+e.ID.String()))
			if err != nil {
				return nil, err
			}
			ring = append(ring, PersistedEpoch{ID: e.ID, Sealed: sealed, AddedAt: e.AddedAt})
		}
		rt.Ring = ring
		r.Ratchet = &rt
	}
	return json.Marshal(r)
}

// decodeRecord is the inverse. A blob that will not open is NOT fatal:
// the record still loads with its metadata intact so the pair keeps
// working (on the legacy key, or on a fresh ratchet), and the first error
// is returned for the caller to log. Refusing the whole record would turn
// one unreadable epoch into a pair that vanishes from the contact list.
func (s *recordSealer) decodeRecord(raw []byte) (Record, error) {
	var r Record
	if err := json.Unmarshal(raw, &r); err != nil {
		return Record{}, fmt.Errorf("pairing: decode record: %w", err)
	}
	if r.Ratchet == nil {
		return r, nil
	}
	var firstErr error
	if len(r.Ratchet.RatchetSKSealed) > 0 {
		pt, err := s.open(r.Ratchet.RatchetSKSealed, sealAAD(r.PeerPK, "ratchet-sk"))
		switch {
		case err != nil:
			firstErr = err
		case len(pt) != len(cipher.SecKey{}):
			firstErr = fmt.Errorf("pairing: store seal: ratchet secret is %d bytes, want %d", len(pt), len(cipher.SecKey{}))
		default:
			copy(r.Ratchet.RatchetSK[:], pt)
			r.Ratchet.RatchetSKSealed = nil
		}
	}
	ring := make([]PersistedEpoch, 0, len(r.Ratchet.Ring))
	for _, e := range r.Ratchet.Ring {
		if len(e.Sealed) == 0 {
			ring = append(ring, e)
			continue
		}
		pt, err := s.open(e.Sealed, sealAAD(r.PeerPK, "epoch|"+e.ID.String()))
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		ring = append(ring, PersistedEpoch{ID: e.ID, Key: pt, AddedAt: e.AddedAt})
	}
	r.Ratchet.Ring = ring
	return r, firstErr
}
