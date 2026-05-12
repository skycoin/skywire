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

// IsValid returns true for the two recognised modes.
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

	// OwnerPK is the visor that publishes the message feed.
	OwnerPK cipher.PubKey `json:"owner_pk"`

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
}
