// Package group cmd/apps/skychat/group/group.go c4-app-chat
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
// Group type: the Kind field is the single user-facing switch. It
// selects BOTH admission (who may join) and payload encryption,
// because the two answers are not independent — see below.
//
//   - KindPublic: anyone who asks is admitted. Bodies are plaintext
//     JSON on the feed. Encrypting a public group would mean handing
//     the key to every stranger who asks, which protects nothing
//     while adding rotation and stale-key failure modes; plaintext
//     is the honest representation of what "public" means. Note this
//     is about the leaf at rest — the wire is always Noise-encrypted
//     by the transport (dmsg KK / CXO-TCP XX / skynet), so "plaintext"
//     never means readable in flight.
//
//   - KindPrivate: every join is a request an admin approves. Bodies
//     are AES-256-GCM. The key is handed to the joiner inside the
//     approval response over the (already encrypted) relay stream —
//     NOT in the invite link. That is what makes a forwarded link
//     worthless on its own, and it is also the precondition for ever
//     rotating the key: the owner is a live distributor rather than a
//     one-shot link author. Links that still carry a key are accepted
//     for backward compatibility.
//
// Mode is retained as the persisted encryption switch (and the
// invite-link wire field) so existing records and links keep working
// unchanged; Kind is derived from it for legacy records by EnsureKind.
// Read encryption through Record.Encrypted(), not by comparing Mode.
//
// Moderation state (Banned / Muted / ReadOnly) rides alongside the
// roster and converges through the same signed-gossip reconciler. See
// moderation.go for the envelope and the enforcement-point table.
package group

import (
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// Mode selects whether messages are encrypted on the feed. It is the
// persisted encryption switch and the invite-link wire field; the
// user-facing group type is Kind, which Mode is derived from at
// create time (and which is derived FROM Mode for legacy records).
// Prefer Record.Encrypted() over comparing this directly.
type Mode string

const (
	// ModePublic — plaintext JSON message bodies on the feed.
	// Anyone the owner allowlists can read. Suited for semi-public
	// rooms (e.g. "operator support").
	ModePublic Mode = "public"

	// ModePrivate — AES-256-GCM-encrypted message bodies. Without
	// the key, even an allowlisted subscriber sees only ciphertext.
	// The key reaches a joiner in the approval response (see
	// JoinResponseMsg); legacy invite links carrying it still work.
	ModePrivate Mode = "private"
)

// IsValid returns true for the two recognized modes.
func (m Mode) IsValid() bool {
	return m == ModePublic || m == ModePrivate
}

// Kind is the user-facing group type — the one switch an operator
// picks at create time. It determines admission policy and, through
// modeForKind, payload encryption.
//
// A third value (KindChannel — a public group only admins may post
// to) is the planned broadcast-channel type; PostPolicy is already
// shaped to carry it so adding it is a table entry rather than a
// refactor.
type Kind string

const (
	// KindPublic — open admission, plaintext bodies.
	KindPublic Kind = "public"

	// KindPrivate — admin-approved admission, encrypted bodies.
	KindPrivate Kind = "private"
)

// IsValid returns true for the recognized group kinds.
func (k Kind) IsValid() bool {
	return k == KindPublic || k == KindPrivate
}

// modeForKind maps a group kind onto the persisted encryption mode.
// Single place to change when a new Kind is introduced.
func modeForKind(k Kind) Mode {
	if k == KindPrivate {
		return ModePrivate
	}
	return ModePublic
}

// kindForMode is the legacy-record inverse: a Record persisted before
// Kind existed carries only Mode, and its admission policy is whatever
// an operator would have assumed that Mode meant.
func kindForMode(m Mode) Kind {
	if m == ModePrivate {
		return KindPrivate
	}
	return KindPublic
}

// JoinPolicy is the admission rule for a group.
type JoinPolicy string

const (
	// JoinOpen — any PK that asks is admitted immediately, provided
	// it isn't banned. The request round-trip still happens: it's
	// what puts the joiner in the roster so other members subscribe
	// to their feed.
	JoinOpen JoinPolicy = "open"

	// JoinApproval — the request is queued for an admin, who
	// approves or denies it. The requester polls for the outcome.
	JoinApproval JoinPolicy = "approval"
)

// PostPolicy is who may publish chat messages into a group.
type PostPolicy string

const (
	// PostAll — every member may post (minus individually muted PKs).
	PostAll PostPolicy = "all"

	// PostAdminsOnly — only admins may post. Set either by the
	// group-wide ReadOnly flag (a reversible "everyone quiet now")
	// or, in future, permanently by a broadcast-channel Kind.
	PostAdminsOnly PostPolicy = "admins"
)

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

	// StatusAwaitingApproval — requester side: we sent a join request
	// to a KindPrivate group and an admin hasn't ruled on it yet. No
	// session is open (we have no allowlist seat, so a subscribe would
	// be refused); the manager's pending-join loop re-asks on a timer
	// until the answer flips this to active or denied.
	StatusAwaitingApproval Status = "awaiting_approval"

	// StatusDenied — requester side: an admin denied the request. A
	// terminal state the operator can retry from explicitly; we do
	// NOT keep re-asking, because a denial that auto-retried every
	// 30s would be indistinguishable from harassment on the admin's
	// pending list.
	StatusDenied Status = "denied"

	// StatusBanned — requester side: the group told us this PK is
	// banned. Terminal and not retryable; kept (rather than deleted)
	// so the UI can explain why the group vanished instead of
	// silently dropping it.
	StatusBanned Status = "banned"
)

// IsTerminal reports whether a status means "this group is over for
// us" — no session should be opened and no reconnect attempted. Used
// by Resume and by the browser-facing list filter.
func (s Status) IsTerminal() bool {
	return s == StatusLeft || s == StatusRevoked || s == StatusDenied || s == StatusBanned
}

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
	// + whether the body is encrypted on the feed. Derived from Kind
	// at create time; kept as the persisted + invite-link field so
	// older binaries and links keep working.
	Mode Mode `json:"mode"`

	// Kind is the user-facing group type — the switch that decides
	// admission policy as well as encryption. Empty on records
	// persisted before this field existed; EnsureKind fills it from
	// Mode on read, so callers never see the zero value.
	Kind Kind `json:"kind,omitempty"`

	// Banned enumerates PKs barred from the group. A ban is strictly
	// stronger than a removal: the PK loses its allowlist seat (so it
	// can no longer read), leaves Members (so nobody subscribes to
	// its feed), is refused at the join-request gate (so it can't
	// walk back in through an open group), and its leaves are dropped
	// reader-side if any are still in flight.
	//
	// Admin-authored and gossiped, like the roster.
	Banned []cipher.PubKey `json:"banned,omitempty"`

	// Muted enumerates PKs that may read but not post ("restricted
	// users"). Unlike a ban this cannot be enforced at the transport:
	// in the federated topology a muted member still owns their own
	// feed and can physically publish to it. Enforcement is therefore
	// reader-side — every honest member drops msgs/ leaves authored
	// by a muted PK — plus a local composer lock so the muted
	// operator sees why rather than shouting into a void.
	//
	// That is the best achievable without reintroducing a central
	// relay, and it is sound against the threat that matters: the
	// mute state is admin-signed and converges everywhere, so a
	// patched client can emit bytes but cannot make anyone render
	// them.
	Muted []cipher.PubKey `json:"muted,omitempty"`

	// MutedSince records when each mute took effect, keyed by the
	// PK's hex. Mutes are FORWARD-ONLY: a restriction stops someone
	// from speaking, it does not erase what they already said. The
	// reader-side gate drops a leaf only when its timestamp is after
	// the entry here, so muting a member leaves their history intact
	// and a later unmute doesn't have to resurrect anything.
	//
	// Always written alongside a Muted entry; a missing entry means
	// zero time, i.e. effective from the beginning. Deliberately NOT
	// applied to bans — a ban means "gone", and hiding the banned
	// PK's history is the intent.
	MutedSince map[string]time.Time `json:"muted_since,omitempty"`

	// ReadOnly, when set, suspends posting for every non-admin —
	// the reversible "quiet the room" control. Same reader-side
	// enforcement model as Muted.
	ReadOnly bool `json:"read_only,omitempty"`

	// ReadOnlySince is when the current read-only period began, and
	// exists for the same forward-only reason as MutedSince. Without
	// it, flipping a busy group to read-only would make every prior
	// message vanish from every member's view the moment the state
	// converged — the reader gate cannot otherwise tell "sent while
	// quieted" from "sent last week".
	ReadOnlySince time.Time `json:"read_only_since,omitempty"`

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

// EnsureKind normalizes the on-disk Kind so callers never observe the
// zero value. Records written before Kind existed carry only Mode;
// their admission policy becomes whatever that Mode implied. Called by
// the store on read, mirroring EnsureFounderInAdmins. Idempotent.
func (r *Record) EnsureKind() {
	if r.Kind == "" {
		r.Kind = kindForMode(r.Mode)
	}
}

// Encrypted reports whether message bodies on this group's feed are
// AES-GCM-sealed. The single predicate every encrypt/decrypt site
// should consult, so introducing a Kind whose encryption doesn't match
// the historical Mode mapping is a one-line change here.
func (r Record) Encrypted() bool { return r.Mode == ModePrivate }

// JoinPolicy returns the admission rule implied by this group's Kind.
func (r Record) JoinPolicy() JoinPolicy {
	if r.Kind == "" {
		// Defensive: an unnormalized record (constructed in a test, or
		// read through a path that skipped EnsureKind) still answers
		// correctly rather than falling through to the open default.
		return policyForKind(kindForMode(r.Mode))
	}
	return policyForKind(r.Kind)
}

func policyForKind(k Kind) JoinPolicy {
	if k == KindPrivate {
		return JoinApproval
	}
	return JoinOpen
}

// PostPolicy returns who may currently publish into this group.
// ReadOnly narrows an otherwise-open group to admins only; it is a
// live toggle, so this is deliberately computed rather than stored.
func (r Record) PostPolicy() PostPolicy {
	if r.ReadOnly {
		return PostAdminsOnly
	}
	return PostAll
}

// IsBanned reports whether pk is barred from this group.
func (r Record) IsBanned(pk cipher.PubKey) bool { return containsPK(r.Banned, pk) }

// IsMuted reports whether pk is individually restricted from posting.
// Does NOT account for the group-wide ReadOnly flag — use CanPost for
// the composite answer.
func (r Record) IsMuted(pk cipher.PubKey) bool { return containsPK(r.Muted, pk) }

// MuteEffectiveFrom returns when pk's mute began. Zero time for a PK
// that isn't muted, or for a mute recorded without a timestamp.
func (r Record) MuteEffectiveFrom(pk cipher.PubKey) time.Time {
	if r.MutedSince == nil {
		return time.Time{}
	}
	return r.MutedSince[pk.Hex()]
}

// CanPost reports whether pk may publish a chat message into this
// group right now, and if not, a short operator-facing reason. The
// reason is surfaced verbatim in the UI's disabled-composer state and
// in the error returned to a send attempt, so it's phrased for a human.
//
// Admins are exempt from ReadOnly (they're the ones who set it) but
// NOT from an explicit mute: muting an admin is unusual, but if an
// admin did it deliberately, silently ignoring it would be worse than
// honoring it. Banned outranks everything.
func (r Record) CanPost(pk cipher.PubKey) (bool, string) {
	switch {
	case r.IsBanned(pk):
		return false, "you are banned from this group"
	case r.IsMuted(pk):
		return false, "you are restricted from sending messages in this group"
	case r.ReadOnly && !r.IsAdmin(pk):
		return false, "this group is read-only — only admins can send messages"
	default:
		return true, ""
	}
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
