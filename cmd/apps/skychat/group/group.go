// Package group — cmd/apps/skychat/group: D1 owner-centric group chat
// built on top of skywire's existing CXO TreeStore feeds.
//
// Architecture (D1, "owner-centric single feed"):
//
//   - One member ("owner") publishes the group's single CXO feed.
//     The feed's SubscriberAllowlist enumerates every group member so
//     only they can subscribe.
//   - Other members subscribe to that feed. They receive messages but
//     don't publish; outgoing messages are POSTed to the owner over
//     the existing 1:1 skychat /message endpoint with a group_id tag,
//     and the owner relays them into the feed.
//   - Owner offline → group is read-only until owner comes back.
//     Acceptable v1 limit. Phase 2 (project memo) shifts to
//     distributed publishers so this isn't a SPOF.
//
// Coexistence of public + private groups: the Mode field on Record
// drives whether message bodies are AES-GCM-encrypted on the feed.
//
//   - Public: body is plaintext JSON on the feed. Anyone with the
//     groupID and an allowlist seat can read.
//   - Private: owner generates a random 32-byte AES-256-GCM key at
//     create time. The key travels in the invite link, so any
//     possessor of the link can join AND decrypt history. Removing
//     a member from a private group requires re-keying — for v1
//     that means creating a fresh group. Acceptable tradeoff.
//
// Why not per-recipient ECIES of the key (so we can revoke):
// adds a real crypto surface (DH-derive + key-wrap layer) that
// belongs in a Phase 2 spec, not in v1. The "create a new group to
// revoke" workflow is bad UX but correct security and concrete.
package group

import (
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// Mode selects whether messages are encrypted on the feed.
type Mode string

const (
	// ModePublic — plaintext JSON message bodies on the feed.
	// Anyone the owner allowlists can read. Suited for semi-public
	// rooms (e.g. "operator support").
	ModePublic Mode = "public"

	// ModePrivate — AES-256-GCM-encrypted message bodies. Key in
	// the invite link; without the key, even an allowlisted
	// subscriber sees only ciphertext.
	ModePrivate Mode = "private"
)

// IsValid returns true for the two recognized modes.
func (m Mode) IsValid() bool {
	return m == ModePublic || m == ModePrivate
}

// Status enumerates a Record's lifecycle, mirroring pairing.Status
// where it makes sense.
type Status string

const (
	// StatusPending — we've registered the group locally but the
	// CXO publisher / subscriber haven't completed their handshake
	// yet (owner just created it, or member just joined and is
	// dialing). Treated as "almost active" by the UI.
	StatusPending Status = "pending"

	// StatusActive — owner's publisher is up, member's subscriber
	// is connected. Messages flow.
	StatusActive Status = "active"

	// StatusLeft — member side: the operator left this group. The
	// record stays for audit / re-join via the same invite. The
	// publisher / subscriber are torn down on transition.
	StatusLeft Status = "left"

	// StatusRevoked — owner side: the owner deleted the group. The
	// publisher is torn down; subscribers will see a stream end.
	// Kept in the local store so an operator can grep "what groups
	// did I run last quarter".
	StatusRevoked Status = "revoked"
)

// Role distinguishes the operator's relationship to this group.
type Role string

const (
	// RoleOwner — this visor publishes the feed. Outgoing messages
	// go straight to Publisher.Put.
	RoleOwner Role = "owner"

	// RoleMember — this visor subscribes to the owner's feed.
	// Outgoing messages are POSTed to the owner via the existing
	// 1:1 skychat /message with a group_id tag.
	RoleMember Role = "member"
)

// Record is the persisted form of a group as this visor knows it.
// Member visors and the owner visor store the same shape; only
// Role differs.
type Record struct {
	// ID is the group's UUID. Used as the bbolt bucket key.
	ID string `json:"id"`

	// Name is operator-supplied free-form. Not used as an identity
	// — just for display.
	Name string `json:"name"`

	// OwnerPK is the founding-creator visor's public key. Historically
	// this was the single PK with roster authority + the only PK that
	// could publish to the feed. As of the federated send change,
	// roster authority is on Admins (which always includes OwnerPK
	// implicitly), and every member publishes to their own feed. The
	// field name stays for backward compatibility with persisted
	// records + the invite-link schema; semantically it's now the
	// "founder" PK — immutable, used as the recovery anchor.
	OwnerPK cipher.PubKey `json:"owner_pk"`

	// Admins are the PKs with roster authority: add/remove members,
	// issue invites, change AES key, promote/demote other admins.
	// OwnerPK is always treated as an admin (the founder is immutable
	// admin); on-disk this slice may or may not include it explicitly
	// — IsAdmin handles either case.
	//
	// Empty/nil on legacy records — the migration path in store.Get
	// fills it with [OwnerPK] on first read so older groups inherit
	// founder-only admin authority. Operator-driven changes flow
	// through Manager.PromoteAdmin / DemoteAdmin which never permit
	// removing the founder (OwnerPK) from this list.
	Admins []cipher.PubKey `json:"admins,omitempty"`

	// Port is the DMSG port the owner's publisher listens on.
	// Chosen by the owner at create time (not deterministic from
	// the groupID — pair-allocator-style determinism doesn't help
	// when the owner is one fixed PK; an opaque port works fine
	// because the invite link distributes it).
	Port uint16 `json:"port"`

	// Mode is public vs private. Drives whether AESKey is present
	// + whether the body is encrypted on the feed.
	Mode Mode `json:"mode"`

	// AESKey is the 32-byte AES-256-GCM key. Set iff Mode ==
	// ModePrivate. Travels in the invite link; storing locally
	// avoids having to re-decode the invite every send.
	AESKey []byte `json:"aes_key,omitempty"`

	// Members enumerates every PK the owner has allowlisted. On
	// the owner side this drives Publisher.SetAllowlist; on the
	// member side it's display-only (the operator wants to know
	// who else is in the room).
	Members []cipher.PubKey `json:"members"`

	// Role tells lifecycle code whether to manage a Publisher
	// (owner) or a Subscriber (member).
	Role Role `json:"role"`

	Status        Status    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	JoinedAt      time.Time `json:"joined_at"`
	LastMessageAt time.Time `json:"last_message_at,omitempty"`
}

// IsFounder reports whether pk is the group's founder (the original
// creator). The founder is implicitly always an admin and cannot be
// demoted. Equality with OwnerPK is the source of truth.
func (r Record) IsFounder(pk cipher.PubKey) bool {
	return r.OwnerPK == pk
}

// IsAdmin reports whether pk has roster authority on this group —
// i.e. is the founder OR is explicitly listed in Admins. Used to gate
// AddMember / BuildInvite / promote-demote / delete operations.
//
// The founder check is OR'd in unconditionally so a legacy record
// whose Admins slice is nil (pre-migration on-disk shape) still
// treats the founder as an admin without needing the migration to
// have run first.
func (r Record) IsAdmin(pk cipher.PubKey) bool {
	if r.IsFounder(pk) {
		return true
	}
	for _, a := range r.Admins {
		if a == pk {
			return true
		}
	}
	return false
}

// EnsureFounderInAdmins normalizes the on-disk Admins slice so the
// founder is always explicitly present. Called by the store on read
// for legacy records (Admins == nil) and by manager ops that mutate
// Admins so the invariant holds post-mutation. Idempotent.
func (r *Record) EnsureFounderInAdmins() {
	if r.OwnerPK == (cipher.PubKey{}) {
		return
	}
	for _, a := range r.Admins {
		if a == r.OwnerPK {
			return
		}
	}
	r.Admins = append([]cipher.PubKey{r.OwnerPK}, r.Admins...)
}

// Message is the on-the-wire (well, on-the-feed) form of a group
// chat entry. For ModePrivate groups, the body lands in Ciphertext
// + Nonce instead of Text; the renderer plugs in AESKey to decrypt.
type Message struct {
	// SenderPK identifies which member sent this. Required.
	SenderPK cipher.PubKey `json:"sender_pk"`

	// TS is the sender's wall-clock. Used for ordering display and
	// for the per-feed last-50 history seed.
	TS time.Time `json:"ts"`

	// Text is the plaintext body for ModePublic groups. Empty for
	// ModePrivate (use Ciphertext+Nonce instead).
	Text string `json:"text,omitempty"`

	// Ciphertext + Nonce carry AES-GCM-encrypted bodies for
	// ModePrivate groups. Nonce is a fresh 12-byte value per message
	// — never reused for the same key, which is what AES-GCM
	// requires.
	Ciphertext []byte `json:"ciphertext,omitempty"`
	Nonce      []byte `json:"nonce,omitempty"`

	// Signature binds (SenderPK, TS, Text|Ciphertext|Nonce) to the
	// sender's identity via secp256k1 over the canonical-bytes
	// layout in signing.go. Verified on every inbound leaf so an
	// admin-aggregator republishing this Message verbatim cannot
	// tamper with the body or the sender claim — the worst they can
	// do is omit, which is detectable by cross-admin divergence.
	//
	// Zero value (cipher.Sig{}) means "unsigned legacy leaf" — a
	// publisher predating the admin-aggregator design. VerifyMessage
	// returns ErrLeafUnsignedLegacy for this case so the inbox path
	// can downgrade to accept-with-warning during the deprecation
	// window, then flip to strict-reject once those publishers
	// have aged out.
	//
	// JSON `omitempty` is honored when the field is exactly the
	// zero value, keeping pre-signing leaves on disk byte-identical
	// to what they were before this field was introduced.
	Signature cipher.Sig `json:"signature,omitempty"`
}
