// Package group pkg/skychat/group/group.go c4-app-chat
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
//     worthless on its own, and it is what made rotation possible: the
//     owner is a live distributor rather than a one-shot link author.
//     Links that still carry a key are accepted for backward
//     compatibility.
//
// Mode is retained as the persisted encryption switch (and the
// invite-link wire field) so existing records and links keep working
// unchanged; Kind is derived from it for legacy records by EnsureKind.
// Read encryption through Record.Encrypted(), not by comparing Mode.
//
// Moderation state (Banned / Muted / ReadOnly) rides alongside the
// roster and converges through the same signed-gossip reconciler. See
// moderation.go for the envelope and the enforcement-point table.
//
// The key is not fixed for the life of the group. Evicting a member
// rotates it, sealed per remaining member over secp256k1 ECDH, so losing
// the seat also means losing the ability to read what comes next — see
// keyrotate.go for the distribution and keyring.go for why every visor
// keeps the keys it has already held.
package group

import (
	"errors"
	"fmt"
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
// picks at create time. It determines admission policy, who may
// publish, and — through modeForKind — payload encryption.
type Kind string

const (
	// KindPublic — open admission, plaintext bodies, everyone posts.
	KindPublic Kind = "public"

	// KindPrivate — admin-approved admission, encrypted bodies.
	KindPrivate Kind = "private"

	// KindChannel — broadcast: open admission like a public group, but
	// only admins may publish. Plaintext for the same reason a public
	// group is: admission is open, so the key would go to anyone who
	// asked and would protect nothing.
	//
	// Unlike the ReadOnly flag — which is a reversible "everyone quiet
	// now" an admin toggles — a channel is admins-only permanently, as
	// a property of the group rather than of its current moderation
	// state. That distinction is why PostPolicy consults Kind before
	// ReadOnly, and why the reader-side gate applies it to leaves of
	// every age rather than forward-only.
	KindChannel Kind = "channel"
)

// IsValid returns true for the recognized group kinds.
func (k Kind) IsValid() bool {
	return k == KindPublic || k == KindPrivate || k == KindChannel
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
	// group-wide ReadOnly flag (a reversible "everyone quiet now") or
	// permanently by KindChannel.
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

	// AdmissionPKs are the admin PKs an invite named as able to admit us.
	// Kept ONLY so a requester whose join is queued can keep asking
	// someone other than the founder — retryPendingJoins reads it, and
	// nothing else does.
	//
	// Deliberately not merged into Admins. These PKs are an unverified
	// claim by whoever wrote the invite, and Admins is roster authority:
	// a PK in there has its signed roster/mod gossip accepted. Keeping
	// the two apart means an invite can route a join request without also
	// granting the PKs it names any power over our copy of the group. The
	// real admin set arrives in the admission response and converges
	// through gossip from then on.
	AdmissionPKs []cipher.PubKey `json:"admission_pks,omitempty"`

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

	// JoinPoWBits is how much proof of work this group demands with a join
	// request, in leading zero bits. Zero means none.
	//
	// Local policy, not gossiped, and that is a deliberate limitation
	// worth stating: each admin enforces its own price, and an open group
	// is only as expensive as its cheapest admin. Converging it would not
	// fix that — an admin running a patched binary can always answer for
	// free — so the cost of the extra machinery buys nothing against the
	// threat that matters, which is an outsider minting identities rather
	// than an insider undercutting the group.
	//
	// Zero on records written before this existed. Read it through
	// JoinPoWRequired(), which applies the default so an operator who has
	// never heard of this field still gets the protection.
	JoinPoWBits uint8 `json:"join_pow_bits,omitempty"`

	// JoinPoWConfigured distinguishes "an admin chose zero" from "this
	// record predates the setting". Without it, turning the price OFF
	// would be indistinguishable from never having set it, and the
	// default would silently turn itself back on.
	JoinPoWConfigured bool `json:"join_pow_configured,omitempty"`

	// PeerBackfillDisabled turns OFF serving the group's history and
	// messages from any online member, restricting it to admins only.
	//
	// An admin decision, gossiped like the other group-wide toggles. With
	// backfill enabled (the default) every member mirrors the verified
	// leaves it sees onto its own feed and non-admins follow a couple of
	// other non-admins, so a joiner can be caught up by whoever happens to
	// be online. Disabled, only admins mirror and only admins are
	// followed — the original topology, where no admin online means no
	// history for anyone.
	//
	// Stored as the NEGATIVE so the zero value means enabled: existing
	// records and newly created groups get the availability behavior
	// without a migration step. Read it through PeerBackfillEnabled()
	// rather than testing this field.
	//
	// What an admin is trading. Enabled costs storage and subscriptions on
	// every member (each holds the room's history, not just admins) and
	// widens who can be asked for it. It does NOT widen who may READ:
	// every copy is the author's own signed leaf, still encrypted for
	// private groups, and only members hold an allowlist seat. Disable it
	// for a group where members should not carry each other's history at
	// rest, and accept that the group then goes dark whenever the admins
	// are offline.
	PeerBackfillDisabled bool `json:"peer_backfill_disabled,omitempty"`

	// Listed opts this group into the visor's discovery catalog: with it
	// set, anyone who knows the HOST's public key can ask for the group
	// and be told it exists. Without it, the group is reachable only by
	// its address or an invite link, both of which require the group ID —
	// 122 bits nobody guesses.
	//
	// Opt-in, and the zero value is the private one. That direction is the
	// whole point: a catalog is the only mechanism here that turns one
	// public key into a LIST, so it must never acquire an entry because
	// somebody didn't read a checkbox. Existing records stay unlisted
	// through the upgrade for the same reason.
	//
	// Local, NOT gossiped. It says what THIS visor is willing to answer
	// questions about, which is a hosting decision rather than a property
	// of the group — two admins can legitimately differ on whether they
	// advertise the same channel, and neither should be able to publish
	// the other's copy. That also means an admin cannot un-list a channel
	// somebody else is advertising, which is honest: they never could.
	Listed bool `json:"listed,omitempty"`

	// ReadOnlySince is when the current read-only period began, and
	// exists for the same forward-only reason as MutedSince. Without
	// it, flipping a busy group to read-only would make every prior
	// message vanish from every member's view the moment the state
	// converged — the reader gate cannot otherwise tell "sent while
	// quieted" from "sent last week".
	ReadOnlySince time.Time `json:"read_only_since,omitempty"`

	// AESKey is the 32-byte AES-256-GCM key currently used to encrypt
	// outgoing messages. Set iff Mode == ModePrivate. Reaches a joiner in
	// the admission response (it no longer travels in the invite link)
	// and is replaced on every rotation.
	//
	// In memory only. The store swaps it for AESKeySealed on the way to
	// disk, so a persisted record never carries it — see store_seal.go.
	// It is still tagged (rather than json:"-") because records written
	// before sealing existed carry it, and those have to keep opening.
	AESKey []byte `json:"aes_key,omitempty"`

	// AESKeySealed is AESKey encrypted at rest under a key derived from
	// this visor's secret key. Present on disk, absent in memory: the
	// store fills one and clears the other in each direction. Never
	// populate it by hand.
	AESKeySealed []byte `json:"aes_key_sealed,omitempty"`

	// KeyEpoch numbers the generation of AESKey. Zero is the key the
	// group was created with — and the value every record written before
	// rotation existed carries, so no migration is needed.
	KeyEpoch uint64 `json:"key_epoch,omitempty"`

	// KeyIssuedAt is the IssuedAt of the KeyMutation that delivered
	// AESKey. Used to settle a same-epoch race between two admins
	// rotating at once; zero for the create-time key.
	KeyIssuedAt time.Time `json:"key_issued_at,omitempty"`

	// KeyRing holds superseded keys, newest first, so messages published
	// before a rotation still open. Rotation is forward-only: it takes
	// away what the evicted PK can read NEXT, it does not erase the
	// group's history for everyone else. See keyring.go.
	KeyRing []GroupKey `json:"key_ring,omitempty"`

	// Members enumerates every PK the owner has allowlisted. On
	// the owner side this drives Publisher.SetAllowlist; on the
	// member side it's display-only (the operator wants to know
	// who else is in the room).
	Members []cipher.PubKey `json:"members"`

	// Role tells lifecycle code whether to manage a Publisher
	// (owner) or a Subscriber (member).
	Role Role `json:"role"`

	// MutationSeen is the replay guard's state: the IssuedAt of the
	// newest gossip mutation applied per (family, target), keyed
	// "<r|a|m>:<pkHex|group>". A mutation older than its entry is
	// refused, so a replayed leaf cannot resurrect state an admin
	// already undid. See replay_guard.go for why this exists and why
	// the map is never pruned.
	MutationSeen map[string]time.Time `json:"mutation_seen,omitempty"`

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

// AdmissionTargets returns the ordered PKs to send a join request to for
// this group: the founder, then whoever the invite named, then any admin
// we have since learned about. self is excluded. Used by the pending-join
// retry loop; the first attempt goes through Invite.AdmissionTargets
// because there is no Record yet.
func (r Record) AdmissionTargets(self cipher.PubKey) []cipher.PubKey {
	return admissionOrder(r.OwnerPK, self, r.AdmissionPKs, r.Admins)
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

// Policy returns the admission rule this kind implies. Exported for
// callers holding a Kind without a Record — the invite/describe paths,
// which know what a group is before they have one.
func (k Kind) Policy() JoinPolicy { return policyForKind(k) }

// policyForKind maps a kind onto its admission rule. Only KindPrivate
// queues; public groups and channels both admit on request — a channel
// restricts publishing, not reading.
func policyForKind(k Kind) JoinPolicy {
	if k == KindPrivate {
		return JoinApproval
	}
	return JoinOpen
}

// PostPolicy returns who may currently publish into this group.
// ReadOnly narrows an otherwise-open group to admins only; it is a
// live toggle, so this is deliberately computed rather than stored.
// A channel is admins-only by construction, whatever ReadOnly says.
func (r Record) PostPolicy() PostPolicy {
	if r.IsChannel() || r.ReadOnly {
		return PostAdminsOnly
	}
	return PostAll
}

// ErrKindImmutable is returned by the store when a write would change an
// existing group's Kind.
var ErrKindImmutable = errors.New("group: a group's kind cannot be changed after it is created")

// checkKindStable rejects a write that would redefine what a persisted
// group IS.
//
// The rule exists for channels specifically. Every other property of a
// group is negotiable — a name, an admin set, a key, even whether it is
// read-only — but "only admins may post here" is the promise a channel's
// subscribers joined under, and flipping it to a public group would hand
// the floor to thousands of people who were never admitted on those
// terms. There is no legitimate migration: the honest way to change a
// channel into a group is to create a group.
//
// An empty incoming Kind counts as a change, not as "no opinion". That is
// the dangerous direction: EnsureKind re-derives an empty Kind from Mode
// on the next read, and a channel and a public group share ModePublic — so
// a write that merely FORGOT the kind would silently reopen the channel to
// everyone. Rejecting it turns that class of bug into an error at the
// boundary instead of a policy change nobody asked for.
//
// prev == "" is allowed through: that is a record written before Kind
// existed, being normalized for the first time.
func checkKindStable(id string, prev, next Kind) error {
	if prev == "" || prev == next {
		return nil
	}
	if next == "" {
		return fmt.Errorf("%w: %s is %q and the write carried no kind", ErrKindImmutable, id, prev)
	}
	return fmt.Errorf("%w: %s is %q and cannot become %q", ErrKindImmutable, id, prev, next)
}

// IsChannel reports whether this group is a broadcast channel — open to
// join, admins-only to post.
//
// Needs no Mode fallback, unlike JoinPolicy: a record written before Kind
// existed cannot have been a channel, because channels did not, so an
// empty Kind is correctly false. That is also the safe direction — an
// unnormalized record reads as an ordinary group rather than silently
// silencing every member of one.
func (r Record) IsChannel() bool { return r.Kind == KindChannel }

// JoinPoWRequired returns the difficulty a join request must meet for
// this group: the admin's setting when one exists, the package default
// otherwise. The single predicate the admission gate consults.
func (r Record) JoinPoWRequired() uint8 {
	if !r.JoinPoWConfigured {
		return DefaultJoinPoWBits
	}
	return clampJoinPoWBits(r.JoinPoWBits)
}

// PeerBackfillEnabled reports whether any online member may serve this
// group's history and messages, as opposed to admins only. The single
// predicate every call site should consult — see PeerBackfillDisabled for
// why the stored field is inverted.
//
// Always false for a channel, whatever the stored flag says. In a channel
// a non-admin has nothing of its own to serve — it cannot post — so
// mirroring the room onto its feed would buy availability for nobody while
// charging every subscriber the disk. It is also what the whole shape of a
// channel implies: subscribers read, admins publish and serve. A channel
// with 10,000 members would otherwise have 10,000 copies of itself.
func (r Record) PeerBackfillEnabled() bool {
	if r.IsChannel() {
		return false
	}
	return !r.PeerBackfillDisabled
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
	case r.IsChannel() && !r.IsAdmin(pk):
		return false, "this is a channel — only admins can post"
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
