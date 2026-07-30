// Package group cmd/apps/skychat/group/keyrotate.go c4-app-chat
// distributing a new group key to the members who are still entitled to
// it — the fourth signed-gossip family, alongside roster/, admin/ and
// mod/.
//
// # The shape of the problem
//
// A rotation has to hand one secret to N specific recipients over a
// medium every member can read. The CXO feed is exactly that: the leaf
// sits on the rotating admin's feed and every subscriber sees the bytes.
// So the key cannot be in the leaf — it has to be in the leaf N times,
// each copy readable by exactly one recipient.
//
// Hence one KeyWrap per remaining member, each sealed under a key derived
// from ECDH(recipient_pk, issuer_sk). The recipient recomputes the same
// secret as ECDH(issuer_pk, recipient_sk) — the symmetry that makes this
// work without any prior shared state — and opens its own wrap. Everyone
// else's wraps are opaque to it. The evicted PK gets no wrap at all,
// which is the entire point: it is not merely disconnected, it is not a
// recipient.
//
// secp256k1 + ChaCha20-Poly1305, not NaCl box: skywire identities are
// secp256k1, and skycoin/cipher.ECDH already does the scalar-mult and
// SHA256s the shared point into exactly 32 bytes. The same construction
// the pairing package uses for pair feeds (cmd/apps/skychat/pairing/
// crypto.go) — deliberately, so there is one ECDH-sealing idiom in this
// app rather than two.
//
// # What this does and does not protect
//
// It gives forward secrecy against an evicted member: messages published
// after the rotation are sealed under a key that member never receives.
// It does NOT give backward secrecy — the evicted member keeps whatever
// it already read, and no protocol can take that back.
//
// It also does not hide the SHAPE of the group: the wrap list names every
// recipient PK in the clear, so an observer who can read the feed learns
// the roster. That is not new information — Members already converges to
// every member through roster/ gossip — so hiding it here would buy
// nothing.
//
// # Authority
//
// A rotation is an admin action, gated exactly like every other mutation
// in this family: signature-valid, issued by a CURRENT admin, and fresh
// by the replay guard. Without the admin gate any member could mint a key
// and, if others accepted it, split the group into two unreadable halves;
// without the freshness gate a replayed old rotation would roll the group
// back onto a key an evicted member still holds.
package group

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	skycipher "github.com/skycoin/skycoin/src/cipher"
	"golang.org/x/crypto/chacha20poly1305"

	"github.com/skycoin/skywire/pkg/cipher"
)

// KeyPathPrefix is the CXO path prefix for signed key-rotation envelopes
// on an admin's feed: "<KeyPathPrefix>/<seq>". Parallel to
// RosterPathPrefix / AdminPathPrefix / ModerationPathPrefix.
const KeyPathPrefix = "keys"

// Sentinel errors for the rotation path.
var (
	// ErrKeyRotationNotEncrypted is returned when a rotation is requested
	// for a group whose bodies are plaintext. There is no key to rotate,
	// and pretending otherwise would suggest a protection the group does
	// not have.
	ErrKeyRotationNotEncrypted = errors.New("group: key rotation: group is not encrypted")

	// errNoGroupKey means we hold no key at all for an encrypted group —
	// a joiner whose admission response is still in flight.
	errNoGroupKey = errors.New("group: no group key available")
)

// KeyWrap is the group key sealed for exactly one recipient.
type KeyWrap struct {
	// RecipientPK is the member this copy is for. In the clear: the
	// recipient has to find its own wrap without trial-decrypting every
	// one, and the roster is not a secret from members anyway.
	RecipientPK cipher.PubKey `json:"recipient_pk"`

	// Sealed is [12-byte nonce | ChaCha20-Poly1305 ciphertext + tag]
	// over the 32-byte group key, under the ECDH secret shared by the
	// issuer and this recipient.
	Sealed []byte `json:"sealed"`
}

// KeyMutation is one signed key rotation: a new epoch plus the sealed
// copies of its key.
//
// Same envelope shape as the other three families — GroupID, IssuedAt,
// IssuerPK, Signature, and a ParentSeq that stays present-but-unused for
// the reason given in replay_guard.go.
type KeyMutation struct {
	GroupID uuid.UUID `json:"group_id"`

	// Epoch is the new key's generation, always greater than the epoch
	// the issuer held when it rotated.
	Epoch uint64 `json:"epoch"`

	// Wraps is one entry per recipient. Order is not significant: the
	// canonical bytes sort by recipient, so a reordered wire copy still
	// verifies.
	Wraps []KeyWrap `json:"wraps"`

	ParentSeq uint64        `json:"parent_seq"`
	IssuedAt  time.Time     `json:"issued_at"`
	IssuerPK  cipher.PubKey `json:"issuer_pk"`
	Signature cipher.Sig    `json:"signature"`
}

// wrapFor returns the wrap addressed to pk.
func (m KeyMutation) wrapFor(pk cipher.PubKey) (KeyWrap, bool) {
	for _, w := range m.Wraps {
		if w.RecipientPK == pk {
			return w, true
		}
	}
	return KeyWrap{}, false
}

// Recipients returns the PKs this rotation was addressed to, in wire
// order. Operator-facing (an admin can see who was cut off) and used by
// tests.
func (m KeyMutation) Recipients() []cipher.PubKey {
	out := make([]cipher.PubKey, 0, len(m.Wraps))
	for _, w := range m.Wraps {
		out = append(out, w.RecipientPK)
	}
	return out
}

// canonicalBytesKey returns the deterministic signed byte sequence for a
// key rotation. Mirrors the roster/admin/mod canonicalizers with the key
// domain tag substituted.
//
// The wrap list is folded into a digest rather than appended field by
// field, because it is variable-length: hashing (recipient || len ||
// sealed) with an explicit length keeps two different wrap lists from
// ever producing the same bytes, which a bare concatenation would allow.
// Sorting by recipient first makes the signature independent of wire
// order, so a relaying admin can re-serialize the JSON without breaking
// verification.
func canonicalBytesKey(m KeyMutation) []byte {
	wraps := make([]KeyWrap, len(m.Wraps))
	copy(wraps, m.Wraps)
	sort.Slice(wraps, func(i, j int) bool {
		return wraps[i].RecipientPK.Hex() < wraps[j].RecipientPK.Hex()
	})
	wh := sha256.New()
	for _, w := range wraps {
		_, _ = wh.Write(w.RecipientPK[:]) //nolint:errcheck // hash.Write never errors
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(w.Sealed)))
		_, _ = wh.Write(n[:])     //nolint:errcheck
		_, _ = wh.Write(w.Sealed) //nolint:errcheck
	}

	buf := make([]byte, 0, 1+1+16+8+32+8+8+33)
	buf = append(buf, gossipCanonicalVersion, domainKey)
	buf = append(buf, m.GroupID[:]...)
	var epoch [8]byte
	binary.BigEndian.PutUint64(epoch[:], m.Epoch)
	buf = append(buf, epoch[:]...)
	buf = append(buf, wh.Sum(nil)...)
	var seq [8]byte
	binary.BigEndian.PutUint64(seq[:], m.ParentSeq)
	buf = append(buf, seq[:]...)
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(m.IssuedAt.UTC().UnixNano())) //nolint:gosec
	buf = append(buf, ts[:]...)
	buf = append(buf, m.IssuerPK[:]...)
	sum := sha256.Sum256(buf)
	return sum[:]
}

// keyRequiredFieldsOK checks the zero-value rules shared by sign and
// verify. A rotation with no wraps is refused: it would be a key nobody
// can open, which is indistinguishable from destroying the group.
func keyRequiredFieldsOK(m KeyMutation) bool {
	if m.GroupID == (uuid.UUID{}) || m.IssuedAt.IsZero() || len(m.Wraps) == 0 {
		return false
	}
	for _, w := range m.Wraps {
		if w.RecipientPK == (cipher.PubKey{}) || len(w.Sealed) == 0 {
			return false
		}
	}
	return true
}

// SignKey fills in IssuerPK + Signature on m using sec. Caller owns
// GroupID / Epoch / Wraps / ParentSeq / IssuedAt.
func SignKey(m *KeyMutation, sec cipher.SecKey) error {
	if m == nil {
		return ErrGossipMissingFields
	}
	if !keyRequiredFieldsOK(*m) {
		return ErrGossipMissingFields
	}
	pk, err := sec.PubKey()
	if err != nil {
		return fmt.Errorf("derive issuer pubkey: %w", err)
	}
	m.IssuerPK = pk
	sig, err := cipher.SignPayload(canonicalBytesKey(*m), sec)
	if err != nil {
		return fmt.Errorf("sign key mutation: %w", err)
	}
	m.Signature = sig
	return nil
}

// VerifyKey checks that m.Signature binds m.IssuerPK to the canonical
// bytes. Stateless — it does NOT check that the issuer is a current admin
// (applyKeyLeaf does that) nor that the epoch is an advance.
func VerifyKey(m KeyMutation) error {
	if !keyRequiredFieldsOK(m) || m.IssuerPK == (cipher.PubKey{}) || m.Signature == (cipher.Sig{}) {
		return ErrGossipMissingFields
	}
	if err := cipher.VerifyPubKeySignedPayload(m.IssuerPK, m.Signature, canonicalBytesKey(m)); err != nil {
		return fmt.Errorf("%w: %v", ErrGossipBadSignature, err)
	}
	return nil
}

// MarshalKey returns the JSON wire encoding of m.
func MarshalKey(m KeyMutation) ([]byte, error) { return json.Marshal(m) }

// UnmarshalKey decodes a JSON wire envelope into a KeyMutation and
// verifies the signature.
func UnmarshalKey(data []byte) (KeyMutation, error) {
	var m KeyMutation
	if err := json.Unmarshal(data, &m); err != nil {
		return KeyMutation{}, fmt.Errorf("decode key mutation: %w", err)
	}
	if err := VerifyKey(m); err != nil {
		return KeyMutation{}, err
	}
	return m, nil
}

// deriveWrapKey returns the 32-byte ECDH secret shared by mySK and
// peerPK. Symmetric: the peer derives the same bytes from their own SK
// and our PK, which is what lets a recipient open a wrap it never
// negotiated.
func deriveWrapKey(mySK cipher.SecKey, peerPK cipher.PubKey) ([]byte, error) {
	key, err := skycipher.ECDH(skycipher.PubKey(peerPK), skycipher.SecKey(mySK))
	if err != nil {
		return nil, fmt.Errorf("group: key wrap: derive ECDH: %w", err)
	}
	if len(key) != chacha20poly1305.KeySize {
		// skycoin/cipher.ECDH SHA256s the shared point, so this only
		// fires if upstream changed the digest.
		return nil, fmt.Errorf("group: key wrap: ECDH key length %d != %d",
			len(key), chacha20poly1305.KeySize)
	}
	return key, nil
}

// sealGroupKey seals groupKey for recipientPK. Layout: [12-byte nonce |
// ct+tag].
func sealGroupKey(mySK cipher.SecKey, recipientPK cipher.PubKey, groupKey []byte) ([]byte, error) {
	if len(groupKey) != 32 {
		return nil, fmt.Errorf("group: key wrap: group key must be 32 bytes, got %d", len(groupKey))
	}
	wk, err := deriveWrapKey(mySK, recipientPK)
	if err != nil {
		return nil, err
	}
	aead, err := chacha20poly1305.New(wk)
	if err != nil {
		return nil, fmt.Errorf("group: key wrap: aead init: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("group: key wrap: nonce: %w", err)
	}
	out := make([]byte, aead.NonceSize(), aead.NonceSize()+len(groupKey)+aead.Overhead())
	copy(out, nonce)
	return aead.Seal(out, nonce, groupKey, nil), nil
}

// openGroupKey is the inverse: unseal a wrap addressed to us, given the
// issuer's PK.
func openGroupKey(mySK cipher.SecKey, issuerPK cipher.PubKey, sealed []byte) ([]byte, error) {
	wk, err := deriveWrapKey(mySK, issuerPK)
	if err != nil {
		return nil, err
	}
	aead, err := chacha20poly1305.New(wk)
	if err != nil {
		return nil, fmt.Errorf("group: key wrap: aead init: %w", err)
	}
	if len(sealed) < aead.NonceSize()+aead.Overhead() {
		return nil, errors.New("group: key wrap: sealed blob too short")
	}
	pt, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], nil)
	if err != nil {
		return nil, fmt.Errorf("group: key wrap: open: %w", err)
	}
	if len(pt) != 32 {
		return nil, fmt.Errorf("group: key wrap: unwrapped key is %d bytes, want 32", len(pt))
	}
	return pt, nil
}

// buildKeyMutation seals key for every recipient and signs the envelope.
// A recipient whose wrap cannot be built (an unusable PK) is skipped
// rather than failing the whole rotation — one bad roster entry must not
// be able to block an eviction — but the caller is told, because a
// rotation nobody can open is refused by SignKey.
func buildKeyMutation(gid uuid.UUID, epoch uint64, key []byte, recipients []cipher.PubKey, mySK cipher.SecKey, at time.Time) (KeyMutation, []cipher.PubKey, error) {
	m := KeyMutation{GroupID: gid, Epoch: epoch, IssuedAt: at.UTC()}
	var skipped []cipher.PubKey
	for _, pk := range recipients {
		sealed, err := sealGroupKey(mySK, pk, key)
		if err != nil {
			skipped = append(skipped, pk)
			continue
		}
		m.Wraps = append(m.Wraps, KeyWrap{RecipientPK: pk, Sealed: sealed})
	}
	if err := SignKey(&m, mySK); err != nil {
		return KeyMutation{}, skipped, fmt.Errorf("group: key rotation: sign: %w", err)
	}
	return m, skipped, nil
}
