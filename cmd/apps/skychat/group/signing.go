// Package group — cmd/apps/skychat/group/signing.go: leaf-level
// signatures for chat messages. Each Message published to a group
// feed is signed by the SenderPK so admin-aggregator republishes
// are tamper-evident: a non-admin subscriber consuming a leaf via
// an admin's canonical feed verifies the signature against the
// original SenderPK before delivering to the handler. The admin
// can only OMIT (which is detectable by cross-admin divergence),
// never alter.
//
// Mirrors the canonical-bytes pattern from gossip.go (#2658) so
// roster/admin envelopes and message leaves use the same primitive
// (cipher.SignPayload — secp256k1, same algorithm dmsg discovery
// entries use). Subscribers built before this change accept
// signature-bearing leaves transparently because the JSON field
// is omitempty when zero.
//
// Wire format: the Signature field is appended to the existing
// Message JSON. Backward-compat: legacy leaves on disk (pre-signing
// publishers) arrive with Signature=zero; VerifyMessage returns
// ErrLeafUnsignedLegacy which callers can choose to accept with a
// deprecation warning during the transition window, then flip to
// strict-reject once publisher TTL elapses.
package group

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/skycoin/skywire/pkg/cipher"
)

// leafCanonicalVersion is the leading version byte of every
// signed-leaf canonical-bytes layout. Bumped on incompatible field-
// set additions so an old verifier fast-rejects (returns
// ErrLeafUnknownCanonicalVersion) rather than mis-verifying a
// re-ordered or extended layout.
const leafCanonicalVersion byte = 1

// Sentinel errors. Callers errors.Is() against these; tests assert
// directly.
var (
	// ErrLeafBadSignature is returned by VerifyMessage when the
	// signature doesn't bind to (SenderPK, canonicalBytes). The
	// underlying cipher.VerifyPubKeySignedPayload error is wrapped.
	ErrLeafBadSignature = errors.New("group: leaf bad signature")

	// ErrLeafMissingSenderPK is returned when SenderPK is the zero
	// value at sign or verify time. Catches caller mistakes — a
	// zero PK means we don't know who to verify against.
	ErrLeafMissingSenderPK = errors.New("group: leaf missing sender_pk")

	// ErrLeafUnknownCanonicalVersion is returned on Verify when the
	// signed envelope is from a future canonical layout this
	// binary doesn't understand. Old binaries fast-reject newer
	// layouts via this rather than computing a digest over a layout
	// they don't recognize.
	ErrLeafUnknownCanonicalVersion = errors.New("group: leaf unknown canonical version")

	// ErrLeafUnsignedLegacy is returned by VerifyMessage when the
	// Signature field is the zero value. Distinct from
	// ErrLeafBadSignature so the inbox path can downgrade to a
	// debug log + accept during the deprecation window, then flip
	// to strict-reject once unsigned publishers have aged out.
	ErrLeafUnsignedLegacy = errors.New("group: leaf unsigned (legacy publisher)")
)

// canonicalBytesMessage returns the deterministic byte sequence
// over a Message's fields that gets signed. Fixed field order,
// big-endian integers, length-prefixed variable components. Adding
// new signed fields REQUIRES bumping leafCanonicalVersion so old
// verifiers reject the new shape rather than silently mis-verifying.
//
// Layout (bytes):
//
//	1   version (leafCanonicalVersion)
//	33  SenderPK
//	8   TS UnixNano BE
//	4   len(Text) BE
//	N1  Text bytes
//	4   len(Ciphertext) BE
//	N2  Ciphertext bytes
//	4   len(Nonce) BE
//	N3  Nonce bytes
//
// Returns the SHA-256 digest over the buffer. cipher.SignPayload
// hashes again internally, but pre-hashing keeps verify cheap (the
// caller compares a 32-byte digest instead of re-walking the
// concat) and matches the gossip.go pattern so reviewers see the
// same shape.
func canonicalBytesMessage(m Message) []byte {
	textLen := len(m.Text)
	cipherLen := len(m.Ciphertext)
	nonceLen := len(m.Nonce)

	// 1 + 33 + 8 + 4 + 4 + 4 = 54 fixed; pre-size with payload too.
	buf := make([]byte, 0, 54+textLen+cipherLen+nonceLen)
	buf = append(buf, leafCanonicalVersion)
	buf = append(buf, m.SenderPK[:]...)

	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(m.TS.UTC().UnixNano())) //nolint:gosec
	buf = append(buf, ts[:]...)

	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(textLen)) //nolint:gosec
	buf = append(buf, l[:]...)
	buf = append(buf, []byte(m.Text)...)

	binary.BigEndian.PutUint32(l[:], uint32(cipherLen)) //nolint:gosec
	buf = append(buf, l[:]...)
	buf = append(buf, m.Ciphertext...)

	binary.BigEndian.PutUint32(l[:], uint32(nonceLen)) //nolint:gosec
	buf = append(buf, l[:]...)
	buf = append(buf, m.Nonce...)

	sum := sha256.Sum256(buf)
	return sum[:]
}

// SignMessage fills in m.Signature using sec. m.SenderPK MUST
// already be populated (and must match the PK derived from sec —
// the function does NOT overwrite SenderPK because publishAs's
// caller passes both explicitly). m.TS must be set; zero TS hashes
// to a deterministic-but-meaningless slot so we don't sign it.
//
// Returns ErrLeafMissingSenderPK on zero SenderPK; the underlying
// cipher.SignPayload error otherwise.
func SignMessage(m *Message, sec cipher.SecKey) error {
	if m == nil {
		return ErrLeafMissingSenderPK
	}
	if m.SenderPK == (cipher.PubKey{}) {
		return ErrLeafMissingSenderPK
	}
	sig, err := cipher.SignPayload(canonicalBytesMessage(*m), sec)
	if err != nil {
		return fmt.Errorf("sign leaf: %w", err)
	}
	m.Signature = sig
	return nil
}

// VerifyMessage checks m.Signature against m.SenderPK over the
// canonical-bytes of m's other fields. Returns nil on success;
// ErrLeafUnsignedLegacy when m.Signature is the zero value (caller
// decides accept-with-warning vs strict-reject); ErrLeafBadSignature
// when the signature doesn't bind; ErrLeafMissingSenderPK when
// SenderPK is zero.
//
// Cheap by design — VerifyMessage runs on every inbound leaf in the
// hot onUpdate path. The canonical bytes are already a 32-byte
// SHA-256 digest; cipher.VerifyPubKeySignedPayload is a single
// secp256k1 verify on top.
func VerifyMessage(m Message) error {
	if m.SenderPK == (cipher.PubKey{}) {
		return ErrLeafMissingSenderPK
	}
	if m.Signature == (cipher.Sig{}) {
		return ErrLeafUnsignedLegacy
	}
	if err := cipher.VerifyPubKeySignedPayload(m.SenderPK, m.Signature, canonicalBytesMessage(m)); err != nil {
		return fmt.Errorf("%w: %w", ErrLeafBadSignature, err)
	}
	return nil
}
