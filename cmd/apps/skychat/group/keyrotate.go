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
// from ECDH(recipient_pk, ephemeral_sk). The recipient recomputes the same
// secret as ECDH(ephemeral_pk, recipient_sk) — the symmetry that makes
// this work without any prior shared state — and opens its own wrap.
// Everyone else's wraps are opaque to it. The evicted PK gets no wrap at
// all, which is the entire point: it is not merely disconnected, it is not
// a recipient.
//
// secp256k1 + ChaCha20-Poly1305, not NaCl box: skywire identities are
// secp256k1, and skycoin/cipher.ECDH already does the scalar-mult and
// SHA256s the shared point into exactly 32 bytes. The same construction
// the pairing package uses for pair feeds (cmd/apps/skychat/pairing/
// crypto.go) — deliberately, so there is one ECDH-sealing idiom in this
// app rather than two.
//
// # Why the wrap key is ephemeral
//
// The wrap secret used to be ECDH(recipient_pk, ISSUER_sk) — the issuer's
// long-term identity key. That made the epoch machinery decorative
// against the threat it looks like it addresses: every keys/<seq> leaf
// stays in the feed forever, so anyone who captured the feed and LATER
// obtained an admin's identity secret could unwrap every group key that
// admin had ever issued, all the way back to the group's first epoch.
// Rotating the group key while the key that unwraps it never changes
// bounds nothing.
//
// Now each rotation mints a throwaway keypair, seals every wrap under
// ECDH(recipient_pk, ephemeral_sk), publishes the ephemeral PUBLIC key in
// the leaf, and drops the ephemeral secret when buildKeyMutation returns.
// After that the issuer itself cannot re-derive the wrap — its identity
// key is no longer sufficient to open its own past rotations, so neither
// is a later compromise of it.
//
// # What this does and does not protect
//
// It gives forward secrecy against an evicted member: messages published
// after the rotation are sealed under a key that member never receives.
// It does NOT give backward secrecy — the evicted member keeps whatever
// it already read, and no protocol can take that back.
//
// Against a later identity-key compromise it protects the ISSUER side
// only. A recipient's wrap is still addressed to that recipient's
// long-term PK, so whoever obtains a member's identity secret can open
// every wrap ever addressed to that member. Closing that too would need
// per-member published prekeys and the round trip to negotiate them;
// what is here bounds the blast radius of an ADMIN key — the one that
// unlocks the whole group rather than one seat — and that is the wider
// hole of the two. Record.KeyRing's bounded retention (keyring.go) is the
// other half: keys age out of the ring, so even a full-disk compromise
// reaches back only so far.
//
// # Who the wraps are addressed to
//
// The wrap list used to name every recipient PK in the clear, on the
// argument that it was not new information: roster/ gossip published the
// same member list in plaintext anyway. Sealing the governance families
// (gossip_seal.go) removed that argument and left this as the ONE place
// an encrypted group still published its roster — and worse than the
// roster, because diffing the recipient sets of two consecutive rotations
// names exactly who was just evicted, which is the fact the moderation
// seal exists to hide.
//
// So a wrap is now addressed by a blinded Tag instead: a digest of the
// same ECDH secret that seals it, which only the intended recipient can
// recompute. It keeps the direct lookup (a recipient finds its own wrap
// without trial-decrypting the list) while telling an observer nothing
// about who is in the group. The tag changes every rotation, because the
// ephemeral half of the ECDH does, so tags cannot be correlated across
// epochs either.
//
// What remains visible is the COUNT of wraps — the group's size, and the
// fact that it grew or shrank. Hiding that needs padding to a fixed
// recipient count, which trades real bandwidth for a much weaker property
// than the one bought here, so it is left visible on purpose.
//
// Compatibility runs one way, and further than the governance seal does:
// a member on a build that predates blinding looks for its wrap BY NAME,
// finds none, and falls off the key schedule — it keeps the key it has
// and stops being able to read the group after the next rotation. That is
// the same upgrade window ephemeral wraps opened (a pre-ephemeral build
// cannot open a wrap sealed under a throwaway key either), and the two
// ship together, so blinding does not widen it. Receiving stays
// backward-compatible in the other direction: a pre-blinding leaf that
// names its recipient is still matched by name, forever.
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
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
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
	// RecipientPK named the member this copy is for, in the clear. Left
	// ZERO by every rotation this build issues — see the "Who the wraps
	// are addressed to" section above; Tag replaces it. Still read on
	// receive, because pre-blinding leaves live in feeds forever and
	// their recipient has no other way to find its wrap.
	//
	// Kept in the canonical bytes unconditionally (zero or not) so those
	// old signatures still verify byte for byte.
	RecipientPK cipher.PubKey `json:"recipient_pk"`

	// Tag identifies the recipient without naming it: a digest over the
	// same ECDH secret that seals this wrap, which only the holder of the
	// recipient secret key can recompute. Absent on pre-blinding leaves.
	//
	// Signed, like EphPK and for the same reason — an attacker who could
	// rewrite tags could make a member fail to find its own wrap and fall
	// silently off the key schedule.
	Tag []byte `json:"tag,omitempty"`

	// Sealed is [12-byte nonce | ChaCha20-Poly1305 ciphertext + tag]
	// over the 32-byte group key, under the ECDH secret shared by
	// EphPK's secret half and this recipient.
	Sealed []byte `json:"sealed"`

	// EphPK is the public half of the throwaway keypair this rotation
	// sealed with; its secret half was discarded the moment the leaf was
	// built. Same value on every wrap in one mutation — the wraps differ
	// because the RECIPIENT half of the ECDH differs.
	//
	// Zero on rotations from builds that predate ephemeral wraps, which
	// is why it is omitempty and why openGroupKey still accepts a sender
	// PK from the caller: a legacy leaf resolves to the issuer's identity
	// PK, and canonicalBytesKey folds this field in only when it is set
	// so those old signatures keep verifying byte for byte.
	EphPK cipher.PubKey `json:"eph_pk,omitempty"`
}

// sealerPK returns the public key whose secret half sealed this wrap:
// the rotation's ephemeral key, or the issuer's identity key for a
// pre-ephemeral leaf.
func (w KeyWrap) sealerPK(issuerPK cipher.PubKey) cipher.PubKey {
	if w.EphPK != (cipher.PubKey{}) {
		return w.EphPK
	}
	return issuerPK
}

// Ephemeral reports whether this rotation used a throwaway wrap key —
// i.e. whether a later compromise of the issuer's identity key can still
// open it. False for a leaf issued by a build that predates ephemeral
// wraps, which is the one case where it can.
func (m KeyMutation) Ephemeral() bool {
	if len(m.Wraps) == 0 {
		return false
	}
	for _, w := range m.Wraps {
		if w.EphPK == (cipher.PubKey{}) {
			return false
		}
	}
	return true
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

// wrapFor returns the legacy wrap addressed to pk by name. Only finds
// pre-blinding wraps; wrapForRecipient is the one the receive path uses.
func (m KeyMutation) wrapFor(pk cipher.PubKey) (KeyWrap, bool) {
	for _, w := range m.Wraps {
		if w.RecipientPK != (cipher.PubKey{}) && w.RecipientPK == pk {
			return w, true
		}
	}
	return KeyWrap{}, false
}

// wrapForRecipient returns the wrap this rotation addressed to us, by tag
// for a blinded leaf and by name for a pre-blinding one.
//
// The ECDH behind the tag is computed at most once per distinct sealer:
// one rotation mints one ephemeral key, so in practice that is a single
// scalar multiplication for the whole list however many members it has.
//
// A wrap whose tag we cannot compute (an unusable sealer PK) is skipped
// rather than failing the lookup — one malformed entry in someone else's
// wrap must not cost us our own key.
func (m KeyMutation) wrapForRecipient(mySK cipher.SecKey, myPK cipher.PubKey) (KeyWrap, bool) {
	tags := make(map[cipher.PubKey][]byte, 1)
	for _, w := range m.Wraps {
		if w.RecipientPK != (cipher.PubKey{}) {
			if w.RecipientPK == myPK {
				return w, true
			}
			continue
		}
		if len(w.Tag) == 0 {
			continue
		}
		sealer := w.sealerPK(m.IssuerPK)
		mine, ok := tags[sealer]
		if !ok {
			shared, err := deriveWrapKey(mySK, sealer)
			if err != nil {
				continue
			}
			mine = wrapTag(shared)
			tags[sealer] = mine
		}
		if bytes.Equal(w.Tag, mine) {
			return w, true
		}
	}
	return KeyWrap{}, false
}

// Recipients returns the PKs this rotation was addressed to, in wire
// order. Empty for a blinded rotation, which is the point of blinding —
// only the issuer knows the list, and it knows it from the roster it
// passed in. Operator-facing, and used by tests over legacy leaves.
func (m KeyMutation) Recipients() []cipher.PubKey {
	out := make([]cipher.PubKey, 0, len(m.Wraps))
	for _, w := range m.Wraps {
		if w.RecipientPK == (cipher.PubKey{}) {
			continue
		}
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
	// Sort on recipient AND tag. Recipient alone stopped being a unique
	// sort key the moment blinded wraps left it zero across the whole
	// list: sort.Slice is not stable, so an all-ties comparison could
	// permute the wraps and hash to a different digest on every call,
	// breaking the signature nondeterministically.
	sort.Slice(wraps, func(i, j int) bool {
		a := wraps[i].RecipientPK.Hex() + "|" + hex.EncodeToString(wraps[i].Tag)
		b := wraps[j].RecipientPK.Hex() + "|" + hex.EncodeToString(wraps[j].Tag)
		return a < b
	})
	wh := sha256.New()
	for _, w := range wraps {
		_, _ = wh.Write(w.RecipientPK[:]) //nolint:errcheck // hash.Write never errors
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(w.Sealed)))
		_, _ = wh.Write(n[:])     //nolint:errcheck
		_, _ = wh.Write(w.Sealed) //nolint:errcheck
		// EphPK joins the digest only when set, so a pre-ephemeral
		// mutation hashes to exactly what it did before this field
		// existed and its signature still verifies. Signing it matters:
		// it is the key half a recipient derives against, so an
		// unauthenticated EphPK would let anyone who can rewrite the
		// leaf swap in a value that silently makes the wrap unopenable.
		if w.EphPK != (cipher.PubKey{}) {
			_, _ = wh.Write(w.EphPK[:]) //nolint:errcheck
		}
		// Same conditional-fold rule as EphPK: a pre-blinding mutation
		// carries no tag and hashes to exactly what it did before this
		// field existed, so its signature keeps verifying.
		if len(w.Tag) > 0 {
			_, _ = wh.Write(w.Tag) //nolint:errcheck
		}
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
		// Addressed either way — by name on a pre-blinding leaf, by tag
		// on a current one. A wrap with neither is addressed to nobody.
		if w.RecipientPK == (cipher.PubKey{}) && len(w.Tag) == 0 {
			return false
		}
		if len(w.Sealed) == 0 {
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

// wrapTagLen is how many bytes of the tag digest ride on the wire. 16 is
// far past any collision concern for a list of at most a few hundred
// wraps, and keeps the leaf small.
const wrapTagLen = 16

// wrapTagLabel domain-separates the tag digest from any other use of the
// same ECDH secret — the AEAD key for the wrap itself, most immediately.
// Deriving both from one secret without separation is the classic way to
// turn two safe primitives into one unsafe one.
const wrapTagLabel = "skychat-group-wrap-tag-v1"

// wrapTag derives the public, non-identifying address of a wrap from the
// ECDH secret it is sealed under. Both sides compute the same value: the
// issuer from (ephemeral_sk, recipient_pk), the recipient from
// (recipient_sk, ephemeral_pk).
//
// An observer holding neither secret sees 16 bytes that are fresh every
// rotation — the ephemeral half changes — so tags reveal nothing about
// the recipient and cannot be linked across epochs.
func wrapTag(shared []byte) []byte {
	h := sha256.New()
	_, _ = h.Write([]byte(wrapTagLabel)) //nolint:errcheck // hash.Write never errors
	_, _ = h.Write(shared)               //nolint:errcheck
	return h.Sum(nil)[:wrapTagLen]
}

// sealGroupKey seals groupKey for recipientPK under sealerSK — the
// rotation's ephemeral secret, not the issuer's identity key. Layout:
// [12-byte nonce | ct+tag]. Returns the wrap's blinding tag alongside,
// derived from the same shared secret.
func sealGroupKey(sealerSK cipher.SecKey, recipientPK cipher.PubKey, groupKey []byte) ([]byte, []byte, error) {
	if len(groupKey) != 32 {
		return nil, nil, fmt.Errorf("group: key wrap: group key must be 32 bytes, got %d", len(groupKey))
	}
	wk, err := deriveWrapKey(sealerSK, recipientPK)
	if err != nil {
		return nil, nil, err
	}
	sealed, err := sealGroupKeyWith(wk, groupKey)
	if err != nil {
		return nil, nil, err
	}
	return sealed, wrapTag(wk), nil
}

// sealGroupKeyWith is sealGroupKey once the shared secret is already in
// hand, so a caller that also needs the tag pays for one ECDH, not two.
func sealGroupKeyWith(wk, groupKey []byte) ([]byte, error) {
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
// PK whose secret half sealed it — KeyWrap.sealerPK, i.e. the rotation's
// ephemeral key, or the issuer's identity key on a pre-ephemeral leaf.
func openGroupKey(mySK cipher.SecKey, sealerPK cipher.PubKey, sealed []byte) ([]byte, error) {
	wk, err := deriveWrapKey(mySK, sealerPK)
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
//
// One ephemeral keypair per rotation, not per recipient: the wraps
// already differ because the recipient half of each ECDH differs, so a
// second throwaway key per member would buy nothing and cost a PK in
// every wrap. ephSK is a local that goes out of scope here — that
// disposal IS the forward-secrecy property, so nothing may store or
// return it.
func buildKeyMutation(gid uuid.UUID, epoch uint64, key []byte, recipients []cipher.PubKey, mySK cipher.SecKey, at time.Time) (KeyMutation, []cipher.PubKey, error) {
	ephPK, ephSK := cipher.GenerateKeyPair()

	m := KeyMutation{GroupID: gid, Epoch: epoch, IssuedAt: at.UTC()}
	var skipped []cipher.PubKey
	for _, pk := range recipients {
		sealed, tag, err := sealGroupKey(ephSK, pk, key)
		if err != nil {
			skipped = append(skipped, pk)
			continue
		}
		// RecipientPK deliberately left zero: the tag is the address now.
		// Naming the member here would republish the roster in the clear
		// on every rotation and undo the governance seal.
		m.Wraps = append(m.Wraps, KeyWrap{Sealed: sealed, EphPK: ephPK, Tag: tag})
	}
	// Signed with the IDENTITY key: the ephemeral key proves nothing
	// about who may re-key the group, and applyKeyLeaf's admin gate reads
	// IssuerPK. Authority and confidentiality are separate jobs here.
	if err := SignKey(&m, mySK); err != nil {
		return KeyMutation{}, skipped, fmt.Errorf("group: key rotation: sign: %w", err)
	}
	return m, skipped, nil
}
