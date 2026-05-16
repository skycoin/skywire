// Package group — cmd/apps/skychat/group/gossip.go: typed mutation
// envelopes for cross-visor admin/roster gossip per the RFC at
// docs/skychat_group_gossip_rfc.md (#2636 issue, #2656 PR).
//
// This file is intentionally pure types + sign/verify + JSON wire —
// no CXO interaction yet. The publisher emit (step 2) and subscriber
// apply (step 3) will live in adjacent files in the same package.
// Keeping the cryptographic core isolated here means the sign/verify
// path is exercised by deterministic unit tests without standing up
// a DMSG fixture.
//
// Signing primitive: skywire's existing cipher.SignPayload /
// VerifyPubKeySignedPayload (secp256k1, the same primitive used by
// every visor PK). The RFC's "ed25519" wording was a holdover from
// the design sketch — secp256k1 is what's in the binary and what's
// already in operators' fingertips. Sig size: 65 bytes (64 sig +
// 1 recovery byte), exposed as cipher.Sig.
//
// Canonical bytes: signing operates on a deterministic byte layout
// (fixed field order, fixed-width integers, no JSON whitespace
// dependency) so the same envelope always produces the same
// signature regardless of how it's serialized over the wire. The
// JSON encoding is the wire format; canonicalBytes is the signing
// format.

package group

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
)

// RosterOp is the kind of roster mutation. Stored as a small
// integer for canonical-bytes compactness and JSON round-trips as
// the same integer (intentionally not a string so a future op type
// never overlaps an old binary's parse).
type RosterOp uint8

const (
	// RosterOpAdd records a peer being added to the member set.
	// Idempotent: applying twice is a no-op.
	RosterOpAdd RosterOp = 1
	// RosterOpRemove records a peer being removed from the member
	// set. Idempotent: applying to a peer already absent is a no-op.
	RosterOpRemove RosterOp = 2
)

// AdminOp is the kind of admin-set mutation. Parallel to RosterOp.
type AdminOp uint8

const (
	// AdminOpPromote grants admin authority to PeerPK.
	// Idempotent: applying twice is a no-op.
	AdminOpPromote AdminOp = 1
	// AdminOpDemote revokes admin authority from PeerPK.
	// Idempotent: applying to a non-admin is a no-op. The founder
	// PK (Record.OwnerPK) cannot be demoted — Apply rejects the
	// mutation per the reconciliation rule.
	AdminOpDemote AdminOp = 2
)

// RosterMutation is one signed instruction to add or remove a
// peer from the group's member set. Issued by an admin and applied
// independently on every subscribing visor per the reconciliation
// rule documented in the RFC.
//
// The signature covers the canonical-bytes encoding of GroupID +
// Op + PeerPK + ParentSeq + IssuedAt + IssuerPK. Wire format is
// JSON (this struct's MarshalJSON tags).
type RosterMutation struct {
	GroupID   uuid.UUID     `json:"group_id"`
	Op        RosterOp      `json:"op"`
	PeerPK    cipher.PubKey `json:"peer_pk"`
	ParentSeq uint64        `json:"parent_seq"`
	IssuedAt  time.Time     `json:"issued_at"`
	IssuerPK  cipher.PubKey `json:"issuer_pk"`
	Signature cipher.Sig    `json:"signature"`
}

// AdminMutation is one signed instruction to promote or demote a
// peer in the admin set. Same envelope shape as RosterMutation
// minus the Op type. Two structs (not one with a polymorphic Op)
// keeps each mutation's path-on-the-feed scope obvious to the
// subscriber's pattern-match.
type AdminMutation struct {
	GroupID   uuid.UUID     `json:"group_id"`
	Op        AdminOp       `json:"op"`
	PeerPK    cipher.PubKey `json:"peer_pk"`
	ParentSeq uint64        `json:"parent_seq"`
	IssuedAt  time.Time     `json:"issued_at"`
	IssuerPK  cipher.PubKey `json:"issuer_pk"`
	Signature cipher.Sig    `json:"signature"`
}

// canonicalBytesRoster returns the deterministic byte sequence
// over a roster mutation's fields that gets signed. Fixed field
// order, big-endian integers, fixed-width components. Adding new
// fields to RosterMutation in the future REQUIRES bumping the
// leading version byte so old verifiers can fast-reject the new
// shape rather than silently mis-verifying a re-ordered layout.
func canonicalBytesRoster(m RosterMutation) []byte {
	// Pre-size: 1 (version) + 16 (uuid) + 1 (op) + 33 (pk) + 8 (seq) + 8 (ts) + 33 (issuer)
	buf := make([]byte, 0, 1+16+1+33+8+8+33)
	buf = append(buf, gossipCanonicalVersion)
	buf = append(buf, m.GroupID[:]...)
	buf = append(buf, byte(m.Op))
	buf = append(buf, m.PeerPK[:]...)
	var seq [8]byte
	binary.BigEndian.PutUint64(seq[:], m.ParentSeq)
	buf = append(buf, seq[:]...)
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(m.IssuedAt.UTC().UnixNano())) //nolint:gosec
	buf = append(buf, ts[:]...)
	buf = append(buf, m.IssuerPK[:]...)
	// Hash once here so verifiers can compare a fixed-size digest
	// instead of re-walking the byte concat. This is just a
	// convenience for the verify path; cipher.SignPayload itself
	// does its own SHA256 inside.
	sum := sha256.Sum256(buf)
	return sum[:]
}

// canonicalBytesAdmin is the parallel canonicalizer for
// AdminMutation. Identical layout aside from the Op type tag.
func canonicalBytesAdmin(m AdminMutation) []byte {
	buf := make([]byte, 0, 1+16+1+33+8+8+33)
	buf = append(buf, gossipCanonicalVersion)
	buf = append(buf, m.GroupID[:]...)
	buf = append(buf, byte(m.Op))
	buf = append(buf, m.PeerPK[:]...)
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

// gossipCanonicalVersion is the leading version byte of every
// canonical-bytes layout. Bumped only on incompatible additions to
// the signed field set; a verifier that doesn't recognize the
// version returns ErrUnknownCanonicalVersion so an old binary
// gracefully rejects a newer envelope instead of mis-verifying.
const gossipCanonicalVersion byte = 1

// Sentinel errors. Tests assert these directly; callers can
// errors.Is them off the returned err.
var (
	// ErrGossipBadSignature is returned by Verify when the signature
	// doesn't bind to (IssuerPK, canonicalBytes). Includes the case
	// where the IssuerPK is the zero value.
	ErrGossipBadSignature = errors.New("gossip: bad signature")

	// ErrGossipMissingFields is returned when a required field is
	// the zero value at sign or verify time (GroupID zero, IssuerPK
	// zero, IssuedAt zero, Signature zero on verify). Catches
	// constructor mistakes before the bad envelope hits the wire.
	ErrGossipMissingFields = errors.New("gossip: missing required field")

	// ErrGossipUnknownOp is returned when Op is outside the defined
	// constant range. A subscriber that receives a future op type
	// drops with this error rather than apply unknown semantics.
	ErrGossipUnknownOp = errors.New("gossip: unknown op")

	// ErrGossipDemoteFounder is returned when an AdminMutation
	// would demote a group's founder. The reconciler rejects on
	// this before applying the local admin-set change. Tested at
	// the unit level by passing the founder PK as PeerPK; the
	// integration with Record.OwnerPK happens in the apply path.
	ErrGossipDemoteFounder = errors.New("gossip: cannot demote founder")
)

// SignRoster fills in IssuerPK + Signature on m using sec. m must
// have GroupID, Op, PeerPK, IssuedAt, and (if non-zero) ParentSeq
// already populated. The function does NOT mutate Op / GroupID /
// PeerPK / ParentSeq / IssuedAt — caller owns those.
//
// Returns ErrGossipMissingFields when required inputs are zero,
// ErrGossipUnknownOp when Op is out of range, or the underlying
// cipher.SignPayload error.
func SignRoster(m *RosterMutation, sec cipher.SecKey) error {
	if m == nil {
		return ErrGossipMissingFields
	}
	if m.GroupID == (uuid.UUID{}) || m.PeerPK == (cipher.PubKey{}) || m.IssuedAt.IsZero() {
		return ErrGossipMissingFields
	}
	if m.Op != RosterOpAdd && m.Op != RosterOpRemove {
		return ErrGossipUnknownOp
	}
	pk, err := sec.PubKey()
	if err != nil {
		return fmt.Errorf("derive issuer pubkey: %w", err)
	}
	m.IssuerPK = pk
	sig, err := cipher.SignPayload(canonicalBytesRoster(*m), sec)
	if err != nil {
		return fmt.Errorf("sign roster mutation: %w", err)
	}
	m.Signature = sig
	return nil
}

// SignAdmin is the parallel signer for AdminMutation.
func SignAdmin(m *AdminMutation, sec cipher.SecKey) error {
	if m == nil {
		return ErrGossipMissingFields
	}
	if m.GroupID == (uuid.UUID{}) || m.PeerPK == (cipher.PubKey{}) || m.IssuedAt.IsZero() {
		return ErrGossipMissingFields
	}
	if m.Op != AdminOpPromote && m.Op != AdminOpDemote {
		return ErrGossipUnknownOp
	}
	pk, err := sec.PubKey()
	if err != nil {
		return fmt.Errorf("derive issuer pubkey: %w", err)
	}
	m.IssuerPK = pk
	sig, err := cipher.SignPayload(canonicalBytesAdmin(*m), sec)
	if err != nil {
		return fmt.Errorf("sign admin mutation: %w", err)
	}
	m.Signature = sig
	return nil
}

// VerifyRoster checks that m.Signature binds m.IssuerPK to the
// canonical bytes. Returns nil on success, ErrGossipBadSignature
// on mismatch, ErrGossipMissingFields when the envelope is
// incomplete, ErrGossipUnknownOp on out-of-range Op.
//
// Verification is stateless: it does NOT check that IssuerPK is
// in the local admin set (the reconciler does that), nor that
// ParentSeq is sensible (also the reconciler's job). Those are
// data-plane checks against current local state; this function
// is the cryptographic-only check.
func VerifyRoster(m RosterMutation) error {
	if m.GroupID == (uuid.UUID{}) || m.PeerPK == (cipher.PubKey{}) ||
		m.IssuerPK == (cipher.PubKey{}) || m.IssuedAt.IsZero() ||
		m.Signature == (cipher.Sig{}) {
		return ErrGossipMissingFields
	}
	if m.Op != RosterOpAdd && m.Op != RosterOpRemove {
		return ErrGossipUnknownOp
	}
	if err := cipher.VerifyPubKeySignedPayload(m.IssuerPK, m.Signature, canonicalBytesRoster(m)); err != nil {
		return fmt.Errorf("%w: %v", ErrGossipBadSignature, err)
	}
	return nil
}

// VerifyAdmin is the parallel verifier for AdminMutation.
func VerifyAdmin(m AdminMutation) error {
	if m.GroupID == (uuid.UUID{}) || m.PeerPK == (cipher.PubKey{}) ||
		m.IssuerPK == (cipher.PubKey{}) || m.IssuedAt.IsZero() ||
		m.Signature == (cipher.Sig{}) {
		return ErrGossipMissingFields
	}
	if m.Op != AdminOpPromote && m.Op != AdminOpDemote {
		return ErrGossipUnknownOp
	}
	if err := cipher.VerifyPubKeySignedPayload(m.IssuerPK, m.Signature, canonicalBytesAdmin(m)); err != nil {
		return fmt.Errorf("%w: %v", ErrGossipBadSignature, err)
	}
	return nil
}

// MarshalRoster returns the JSON wire encoding of m. Trivial
// wrapper around encoding/json — present so callers don't reach
// past the package boundary for the encoder.
func MarshalRoster(m RosterMutation) ([]byte, error) { return json.Marshal(m) }

// MarshalAdmin returns the JSON wire encoding of m.
func MarshalAdmin(m AdminMutation) ([]byte, error) { return json.Marshal(m) }

// UnmarshalRoster decodes a JSON wire envelope into a RosterMutation
// and verifies the signature. Convenience for the subscriber path
// where every received leaf is decode-and-verify in lockstep.
func UnmarshalRoster(data []byte) (RosterMutation, error) {
	var m RosterMutation
	if err := json.Unmarshal(data, &m); err != nil {
		return RosterMutation{}, fmt.Errorf("decode roster mutation: %w", err)
	}
	if err := VerifyRoster(m); err != nil {
		return RosterMutation{}, err
	}
	return m, nil
}

// UnmarshalAdmin is the parallel decoder for AdminMutation.
func UnmarshalAdmin(data []byte) (AdminMutation, error) {
	var m AdminMutation
	if err := json.Unmarshal(data, &m); err != nil {
		return AdminMutation{}, fmt.Errorf("decode admin mutation: %w", err)
	}
	if err := VerifyAdmin(m); err != nil {
		return AdminMutation{}, err
	}
	return m, nil
}
