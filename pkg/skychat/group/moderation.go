// Package group pkg/skychat/group/moderation.go c4-app-chat
// signed envelopes for the moderation half of admission control:
// ban / unban, mute / unmute, and the group-wide read-only toggle.
//
// Third member of the gossip family alongside RosterMutation (who is
// in the group) and AdminMutation (who has authority). Same shape,
// same authority gate on apply — only a current admin's mutation is
// applied — and its own domain tag in the canonical bytes so a
// signature over a roster or admin envelope can never be replayed as
// a ban. See the domainRoster/domainAdmin/domainMod block in gossip.go.
//
// # Where each control is actually enforced
//
// The group topology is federated: every member publishes to their own
// CXO feed and the others subscribe directly. There is no chokepoint a
// server could filter at, so the enforcement point differs per control
// and it's worth being precise about which are structural and which
// are cooperative.
//
//	control     | enforced by                                  | strength
//	------------|----------------------------------------------|----------
//	not member  | publisher's SubscriberAllowlist refuses the   | structural
//	            | CXO subscribe; not in Members so nobody       |
//	            | subscribes to their feed                      |
//	ban         | all of the above, PLUS the join-request gate  | structural
//	            | refuses re-entry, PLUS reader-side drop       |
//	mute        | reader-side drop of their msgs/ leaves +      | cooperative
//	            | local composer lock                           |
//	read-only   | reader-side drop of non-admin msgs/ leaves +  | cooperative
//	            | local composer lock                           |
//
// "Cooperative" means a patched client can still emit bytes onto its
// own feed. It cannot make anyone render them: the mute/read-only
// state is admin-signed, converges to every member through this
// envelope, and every honest reader drops the leaf before it reaches
// the inbox. Muting is a moderation control, not a capability
// revocation — banning is the capability revocation, and it is
// structural precisely because it takes the allowlist seat away.
//
// Making mute structural would mean routing every message through an
// admin again, which is the single-point-of-failure the federated
// design exists to remove. Not worth it to harden a control whose
// realistic adversary is a spammer with a stock client.
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

// ModerationPathPrefix is the CXO path prefix for signed moderation
// envelopes on an admin's feed: "<ModerationPathPrefix>/<seq>".
// Parallel to RosterPathPrefix / AdminPathPrefix.
const ModerationPathPrefix = "mod"

// ModOp is the kind of moderation mutation. Small integer for
// canonical-bytes compactness, same as RosterOp / AdminOp.
//
// Numbering starts where it does (not at 1) purely for readability in
// logs; the domain tag — not the op value — is what keeps these from
// colliding with the other envelope families.
type ModOp uint8

const (
	// ModOpBan bars PeerPK from the group: drops the allowlist seat,
	// removes from Members, refuses future join requests, and drops
	// any in-flight leaves. Idempotent.
	ModOpBan ModOp = 1

	// ModOpUnban lifts a ban. Does NOT re-admit — the PK must ask to
	// join again, which is the point: unban means "you may ask", not
	// "you're back in".
	ModOpUnban ModOp = 2

	// ModOpMute restricts PeerPK from posting while leaving read
	// access intact. Idempotent.
	ModOpMute ModOp = 3

	// ModOpUnmute lifts a mute. Idempotent.
	ModOpUnmute ModOp = 4

	// ModOpReadOnly suspends posting for every non-admin. Group-scope:
	// PeerPK is the zero value, which the validators permit for this
	// op alone.
	ModOpReadOnly ModOp = 5

	// ModOpReadWrite lifts read-only. Group-scope, PeerPK zero.
	ModOpReadWrite ModOp = 6

	// ModOpPeerBackfillOn allows any online member to serve the group's
	// history and messages, not just admins. Group-scope, PeerPK zero.
	// See Record.PeerBackfillDisabled for the trade an admin is making.
	ModOpPeerBackfillOn ModOp = 7

	// ModOpPeerBackfillOff restricts serving to admins only — the
	// original topology. Group-scope, PeerPK zero.
	ModOpPeerBackfillOff ModOp = 8
)

// isGroupScoped reports whether op targets the group as a whole rather
// than a specific peer — the ops for which a zero PeerPK is legal.
func (o ModOp) isGroupScoped() bool {
	return o == ModOpReadOnly || o == ModOpReadWrite ||
		o == ModOpPeerBackfillOn || o == ModOpPeerBackfillOff
}

// isValid reports whether op is a defined moderation op.
func (o ModOp) isValid() bool {
	return o >= ModOpBan && o <= ModOpPeerBackfillOff
}

// ModerationMutation is one signed moderation instruction, issued by
// an admin and applied independently on every subscribing visor.
//
// PeerPK is the target for peer-scoped ops and the zero value for
// group-scoped ones (read-only toggles). The signature covers the
// canonical-bytes encoding including the moderation domain tag.
type ModerationMutation struct {
	GroupID   uuid.UUID     `json:"group_id"`
	Op        ModOp         `json:"op"`
	PeerPK    cipher.PubKey `json:"peer_pk"`
	ParentSeq uint64        `json:"parent_seq"`
	IssuedAt  time.Time     `json:"issued_at"`
	IssuerPK  cipher.PubKey `json:"issuer_pk"`
	Signature cipher.Sig    `json:"signature"`
}

// ErrGossipBanFounder is returned when a moderation mutation would ban
// or mute the group's founder. The founder is the recovery anchor for
// the whole record (immutable admin, invite trust root); letting a
// promoted admin silence them would be a takeover primitive.
var ErrGossipBanFounder = errors.New("gossip: cannot ban or mute founder")

// canonicalBytesMod returns the deterministic signed byte sequence for
// a moderation mutation. Layout mirrors the roster/admin canonicalizers
// with the moderation domain tag substituted; see canonicalBytesRoster
// for the field-ordering rationale.
func canonicalBytesMod(m ModerationMutation) []byte {
	buf := make([]byte, 0, 1+1+16+1+33+8+8+33)
	buf = append(buf, gossipCanonicalVersion, domainMod)
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

// modRequiredFieldsOK checks the zero-value rules shared by sign and
// verify: GroupID and IssuedAt always required, PeerPK required for
// peer-scoped ops and forbidden for group-scoped ones.
//
// Forbidding (not merely ignoring) a PeerPK on a group-scoped op
// matters: the field is inside the signed bytes, so tolerating a
// populated value would let two different envelopes — one naming a
// victim, one not — both be valid read-only toggles, which is exactly
// the kind of ambiguity a canonical encoding exists to prevent.
func modRequiredFieldsOK(m ModerationMutation) bool {
	if m.GroupID == (uuid.UUID{}) || m.IssuedAt.IsZero() {
		return false
	}
	if m.Op.isGroupScoped() {
		return m.PeerPK == (cipher.PubKey{})
	}
	return m.PeerPK != (cipher.PubKey{})
}

// SignMod fills in IssuerPK + Signature on m using sec. Caller owns
// GroupID / Op / PeerPK / ParentSeq / IssuedAt.
//
// Returns ErrGossipMissingFields when the zero-value rules are
// violated, ErrGossipUnknownOp for an undefined op, or the underlying
// cipher.SignPayload error.
func SignMod(m *ModerationMutation, sec cipher.SecKey) error {
	if m == nil {
		return ErrGossipMissingFields
	}
	if !m.Op.isValid() {
		return ErrGossipUnknownOp
	}
	if !modRequiredFieldsOK(*m) {
		return ErrGossipMissingFields
	}
	pk, err := sec.PubKey()
	if err != nil {
		return fmt.Errorf("derive issuer pubkey: %w", err)
	}
	m.IssuerPK = pk
	sig, err := cipher.SignPayload(canonicalBytesMod(*m), sec)
	if err != nil {
		return fmt.Errorf("sign moderation mutation: %w", err)
	}
	m.Signature = sig
	return nil
}

// VerifyMod checks that m.Signature binds m.IssuerPK to the canonical
// bytes. Stateless — it does NOT check that the issuer is a current
// admin (applyModLeaf does that) nor that ParentSeq is sensible.
func VerifyMod(m ModerationMutation) error {
	if !m.Op.isValid() {
		return ErrGossipUnknownOp
	}
	if !modRequiredFieldsOK(m) || m.IssuerPK == (cipher.PubKey{}) || m.Signature == (cipher.Sig{}) {
		return ErrGossipMissingFields
	}
	if err := cipher.VerifyPubKeySignedPayload(m.IssuerPK, m.Signature, canonicalBytesMod(m)); err != nil {
		return fmt.Errorf("%w: %v", ErrGossipBadSignature, err)
	}
	return nil
}

// MarshalMod returns the JSON wire encoding of m.
func MarshalMod(m ModerationMutation) ([]byte, error) { return json.Marshal(m) }

// UnmarshalMod decodes a JSON wire envelope into a ModerationMutation
// and verifies the signature.
func UnmarshalMod(data []byte) (ModerationMutation, error) {
	var m ModerationMutation
	if err := json.Unmarshal(data, &m); err != nil {
		return ModerationMutation{}, fmt.Errorf("decode moderation mutation: %w", err)
	}
	if err := VerifyMod(m); err != nil {
		return ModerationMutation{}, err
	}
	return m, nil
}
