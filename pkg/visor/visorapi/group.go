// Package visorapi pkg/visor/visorapi/group.go c3-vis-core
package visorapi

import (
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	skychatgroup "github.com/skycoin/skywire/pkg/skychat/group"
)

// GroupInfo is the public summary of a chat group, returned by
// GroupList and GroupGet.
type GroupInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Avatar is the group's picture as a data URI, empty when unset.
	// The URI form rather than bytes for the same reason Profile carries
	// one over RPC: JSON-friendly and directly usable as an <img> src,
	// with the MIME inside it always the validated one.
	Avatar  string            `json:"avatar,omitempty"`
	OwnerPK cipher.PubKey     `json:"owner_pk"`
	Port    uint16            `json:"port"`
	Mode    skychatgroup.Mode `json:"mode"`
	// Kind is the user-facing group type — "public" (open admission,
	// plaintext) or "private" (admin-approved admission, encrypted).
	// Mode is retained above for older clients; new code should read
	// Kind, JoinPolicy and PostPolicy.
	Kind skychatgroup.Kind `json:"kind,omitempty"`
	// JoinPolicy and PostPolicy are derived, not stored: they answer
	// "can someone walk in" and "who may speak right now" so a UI
	// doesn't have to re-derive the rules from Kind + ReadOnly.
	JoinPolicy skychatgroup.JoinPolicy `json:"join_policy,omitempty"`
	PostPolicy skychatgroup.PostPolicy `json:"post_policy,omitempty"`

	// Banned and Muted are the moderation lists. Banned PKs are out of
	// the group and refused at the join gate; muted PKs read but
	// cannot post.
	Banned []cipher.PubKey `json:"banned,omitempty"`
	Muted  []cipher.PubKey `json:"muted,omitempty"`

	// ReadOnly suspends posting for every non-admin.
	ReadOnly bool `json:"read_only,omitempty"`

	// JoinPoWBits is how much proof of work this group asks of a join
	// request, in leading zero bits — the price of one identity. Zero
	// means none. Surfaced so an operator under a flood can see the
	// current setting and raise it.
	JoinPoWBits uint8 `json:"join_pow_bits"`

	// PeerBackfill reports whether any online member may serve this
	// group's history and messages to a joiner (true) or only admins
	// (false). Positive sense here even though the record stores the
	// negative — an API consumer should not have to reason about a double
	// negative to render a checkbox.
	PeerBackfill bool `json:"peer_backfill"`

	// Listed reports whether THIS visor publishes the group in its
	// discovery catalog. Local, not gossiped — another admin's copy of the
	// same group carries its own answer. See skychatgroup.Record.Listed.
	Listed bool `json:"listed,omitempty"`

	// KeyEpoch is the generation of the key this group currently encrypts
	// with — 0 for a plaintext group or one still on its create-time key,
	// incremented by every rotation. Surfaced so an operator can see that
	// an eviction actually re-keyed the group, and that every member
	// converged onto the same epoch.
	KeyEpoch uint64 `json:"key_epoch,omitempty"`

	// KeyAge is how long the current key has been in force, in seconds.
	// The epoch number alone doesn't say whether it is a week old or a
	// year; this is what makes "bounded in time" visible.
	KeyAgeSeconds int64 `json:"key_age_seconds,omitempty"`

	// KeyRotatesInSeconds is how long until this group's key is due for
	// replacement on age. Negative means it is already due and the next
	// background tick will re-key it. Zero for a plaintext group.
	KeyRotatesInSeconds int64 `json:"key_rotates_in_seconds,omitempty"`

	// GovernanceSealed reports whether this group's membership and
	// moderation history is encrypted on the feed as well as its
	// messages — who was added, promoted, banned or muted, and when.
	//
	// True exactly when the group is encrypted; surfaced as its own
	// field rather than left for a client to infer from Kind, because
	// "the bans are private too" is a distinct promise from "the
	// messages are private" and it was NOT true before this build. A
	// client that shows it can tell the operator which one they have.
	GovernanceSealed bool `json:"governance_sealed,omitempty"`

	// CanPost / CannotPostReason answer "may THIS visor post into this
	// group right now", pre-computed so the UI can disable the composer
	// and explain why without duplicating the precedence rules
	// (banned > muted > read-only).
	CanPost          bool   `json:"can_post"`
	CannotPostReason string `json:"cannot_post_reason,omitempty"`

	// PendingJoins is the number of join requests awaiting an admin
	// decision. Zero for non-admins and for open groups, which admit
	// without queuing. Populated by GroupList and GroupGet.
	PendingJoins int `json:"pending_joins,omitempty"`
	// Admins is the per-record roster-authority set. Mirrors
	// Record.Admins exactly — founder is implicitly admin and is
	// surfaced explicitly here after EnsureFounderInAdmins runs on
	// the store read path, so RPC clients can render the admin list
	// without having to OR in OwnerPK themselves.
	Admins        []cipher.PubKey     `json:"admins,omitempty"`
	Members       []cipher.PubKey     `json:"members"`
	Role          skychatgroup.Role   `json:"role"`
	Status        skychatgroup.Status `json:"status"`
	CreatedAt     time.Time           `json:"created_at"`
	JoinedAt      time.Time           `json:"joined_at"`
	LastMessageAt time.Time           `json:"last_message_at,omitempty"`
	// SubscriberAlive is the live health of this visor's subscriber
	// side. Populated by GroupList; left zero by other accessors that
	// don't need it. See group.Manager.IsSubscriberAlive for semantics.
	SubscriberAlive bool `json:"subscriber_alive"`

	// PeerLastInbound is the per-peer last-inbound time, keyed by
	// peer-PK hex. Populated by GroupList and GroupGet alongside
	// SubscriberAlive. A zero time means the peerSub for that peer
	// is connecting, silent since startup, or (legacy s.sub path)
	// not individually tracked. Empty map for owner-role sessions
	// that don't follow peer feeds. Operator-facing: lets `cli
	// skychat group info` show which specific peer is stale in a
	// group whose session-level SubscriberAlive is otherwise true.
	PeerLastInbound map[string]time.Time `json:"peer_last_inbound,omitempty"`

	// SubDropCount is the running total of inbox-to-stream drops
	// across every live + torn-down gRPC StreamGroupMessages
	// subscriber on THIS VISOR. Bumped in groupInbox.deliver's
	// select+default branch when a subscriber's bounded channel is
	// full (per-subscriber 256-message buffer set by the gRPC
	// handler); persisted across unsubscribe so a single rolling
	// total survives stream restarts.
	//
	// Inbox-wide, not group-scoped — the inbox fans every message
	// to every live subscriber regardless of group, so a drop is
	// "this subscriber couldn't keep up" rather than "this group is
	// noisy". Repeated across every GroupInfo here for operator
	// convenience; cross-checking the value between two groups in
	// the same /status response always returns the same number.
	//
	// Distinct from the inbox ring's overflow drop semantics — the
	// ring keeps the most-recent groupInboxCap (1024) messages
	// regardless of subscriber draining. SubDropCount only counts
	// messages that reached deliver's fan-out but failed to enqueue
	// onto a particular subscriber's channel; the ring still has
	// them and the next StreamGroupMessages reconnect's backlog
	// replay picks them up via SinceTimestampNs (#2630/#2659).
	//
	// Surfaced live so the chat-app's /status can flag a busy group
	// whose subscriber side is silently dropping under burst — pre-
	// fix the count was only readable at unsubscribe time, leaving a
	// gap where an operator watching /status saw subscriber_alive=true
	// and last_message_at advancing but the CLI listener pipe
	// missing messages.
	SubDropCount uint64 `json:"sub_drop_count"`

	// DeliverCount is the running total of groupInbox.deliver()
	// invocations since visor startup — every message that landed
	// at the inbox layer, regardless of downstream consumption.
	//
	// Combined with PeerUpdateCount (per-peer) and SubDropCount,
	// localizes drops along the receive path:
	//   PeerUpdateCount[peer]  =  CXO peerSub → Session callback fired
	//   DeliverCount           =  Session → inbox.deliver() enqueued
	//   SubDropCount           =  inbox → gRPC subscriber channel dropped
	//
	// Deltas tell where messages are lost:
	//   peerUpdate_sum < deliverCount  → impossible (deliver is downstream)
	//   peerUpdate_sum > deliverCount  → drops in Session.makePeerOnUpdate's
	//                                    deliver call site (per-peer callback fired
	//                                    but message never reached inbox)
	//   deliverCount > rcvd_at_client  → drops between inbox and the CLI listener
	//                                    (subDropCount accounts for some; the rest
	//                                    are gRPC adapter or stream.Send)
	//
	// Visor-wide, not group-scoped (matches SubDropCount semantics).
	// Repeated across every GroupInfo for operator convenience.
	DeliverCount uint64 `json:"deliver_count"`

	// StreamSendCount is the running total of successful stream.Send
	// calls on rpcgrpc.StreamGroupMessages across both PingServer
	// instances (local CLI + dmsg-RPC). Counts data events only;
	// the Subscribed sentinel that the handler emits before entering
	// the dispatch loop is NOT included.
	//
	// Pairs with DeliverCount to localize drops between the inbox
	// layer and the CLI listener:
	//   DeliverCount    > StreamSendCount  → drops between inbox and
	//                                        stream (SubDropCount accounts
	//                                        for some; rest are adapter
	//                                        backpressure or stream
	//                                        flow-control on the gRPC
	//                                        send buffer).
	//   DeliverCount   ==  StreamSendCount → everything that landed
	//                                        in the inbox was sent to
	//                                        the wire; any operator-
	//                                        observed loss is downstream
	//                                        of stream.Send (gRPC client
	//                                        recv, CLI buffering, or
	//                                        Monitor task wrapper).
	//
	// Visor-wide, not group-scoped. Reset only on visor restart.
	StreamSendCount uint64 `json:"stream_send_count"`

	// PeerUpdateCount is the per-peer count of treestore subscriber
	// OnUpdate callbacks observed by Session.makePeerOnUpdate, keyed
	// by peer-PK hex. Populated alongside PeerLastInbound. Bumped on
	// every events>0 callback (the same seam that bumps the
	// peerLastInboundNs liveness signal).
	//
	// Empty map for owner-role sessions that don't follow peer feeds.
	// Operator-facing: an idle peer with PeerLastInbound zero AND
	// PeerUpdateCount zero has never been heard from; an idle peer
	// with PeerLastInbound stale but PeerUpdateCount > 0 went silent
	// after a known interaction. Comparing PeerUpdateCount sums to
	// DeliverCount localizes drops upstream vs downstream of the
	// inbox enqueue.
	PeerUpdateCount map[string]uint64 `json:"peer_update_count,omitempty"`
}

// GroupMessage is one inbound message delivered through the visor's
// group inbox. Outbound (owner-only Sends) are NOT echoed here; the
// caller already knows what it sent.
type GroupMessage struct {
	GroupID  string        `json:"group_id"`
	SenderPK cipher.PubKey `json:"sender_pk"`
	Text     string        `json:"text"`
	TS       time.Time     `json:"ts"`
}

// GroupCreateArgs is the RPC input for GroupCreate.
type GroupCreateArgs struct {
	Name string `json:"name"`

	// Kind is the group type: "public" (open admission, plaintext),
	// "private" (admin-approved admission, encrypted) or "channel"
	// (open admission, plaintext, only admins may post). Preferred
	// over Mode.
	Kind skychatgroup.Kind `json:"kind,omitempty"`

	// Mode is the legacy field, kept so an older RPC client keeps
	// working: it carried the same two values and mapped onto the same
	// two group types. Used only when Kind is empty, and it cannot name
	// a channel — public and channel share ModePublic.
	Mode skychatgroup.Mode `json:"mode,omitempty"`

	InitialMembers []cipher.PubKey `json:"initial_members,omitempty"`

	// DisablePeerBackfill is the creator's choice on whether any online
	// member may serve this group's history to a joiner, or only admins.
	// The zero value keeps backfill ENABLED, which is the default an
	// operator gets by not saying anything and matches the group record's
	// own inverted field. An admin can change it later with
	// GroupSetPeerBackfill.
	DisablePeerBackfill bool `json:"disable_peer_backfill,omitempty"`

	// Listed opts the new group into this visor's discovery catalog. The
	// zero value keeps it unlisted, which is the private direction — see
	// skychatgroup.Record.Listed for why that default matters.
	Listed bool `json:"listed,omitempty"`
}

// ResolvedKind resolves the group type from either field, defaulting to public.
func (a GroupCreateArgs) ResolvedKind() skychatgroup.Kind {
	if a.Kind != "" {
		return a.Kind
	}
	if a.Mode == skychatgroup.ModePrivate {
		return skychatgroup.KindPrivate
	}
	return skychatgroup.KindPublic
}

// GroupJoinArgs is the RPC input for GroupJoin. Exactly one of the two
// fields is used, Invite first.
type GroupJoinArgs struct {
	// Invite is a skychat:invite:<base64url> link. Self-contained: it
	// works even while the group's host is offline, because it carries
	// the port and mode inline.
	Invite string `json:"invite"`

	// Address is the short form — skychat://<host-pk>/<group-id>. Not
	// self-contained: the host is asked what the group is (see
	// group.Manager.JoinByAddress) before the ordinary join runs, so
	// this needs the host reachable and an invite does not.
	//
	// Both are accepted because they trade against each other rather
	// than one superseding the other: an address is short enough to scan
	// or read aloud and stays correct as the group changes, a link keeps
	// working when nobody is home.
	Address string `json:"address,omitempty"`
}

// GroupResolveArgs is the RPC input for GroupResolve.
type GroupResolveArgs struct {
	// Address is any spelling the address parser accepts — a bare public
	// key, skychat://<pk>, skychat://<pk>/<group-id> — or a
	// skychat:invite: link, which is answered from the link itself
	// without a round trip.
	Address string `json:"address"`
}

// GroupResolveResult describes what an address points at, so a UI can
// offer the one right action instead of making the user pick.
//
// The zero-ish DM case is deliberately cheap: a bare public key needs no
// network at all to classify, because a key that names a person and a key
// that hosts a channel are the same 66 characters — only the presence of
// a group ID distinguishes them, and that is in the address.
type GroupResolveResult struct {
	// Target is "dm" or "group".
	Target string `json:"target"`

	// PK is the peer for a DM, the host for a group address.
	PK cipher.PubKey `json:"pk"`

	// Address is the canonical skychat:// form of what was resolved,
	// suitable for display, copying, and QR encoding.
	Address string `json:"address"`

	// Group is populated for a group address: what the group is and what
	// joining it will involve. Nil for a DM.
	Group *GroupDescriptor `json:"group,omitempty"`

	// Joined reports that this visor is already an active member, so the
	// caller should open the group rather than offer to join it.
	Joined bool `json:"joined,omitempty"`

	// Status is this visor's own record status for the group when one
	// exists — "awaiting_approval" and "denied" in particular, which are
	// the difference between offering "Send request" and explaining that
	// one was already refused.
	Status skychatgroup.Status `json:"status,omitempty"`

	// Invite carries the link back when the input WAS a link, so a
	// caller that resolved one can hand it straight to GroupJoin without
	// re-parsing.
	Invite string `json:"invite,omitempty"`
}

// GroupDescriptor is the RPC-facing shape of group.GroupDescriptor.
// Re-declared rather than aliased so the RPC surface can carry
// display-oriented additions (a rendered kind label, later a price) that
// the protocol type has no business knowing about.
type GroupDescriptor struct {
	ID        string                  `json:"id"`
	HostPK    cipher.PubKey           `json:"host_pk"`
	Name      string                  `json:"name"`
	Kind      skychatgroup.Kind       `json:"kind"`
	Mode      skychatgroup.Mode       `json:"mode"`
	Policy    skychatgroup.JoinPolicy `json:"policy"`
	Port      uint16                  `json:"port"`
	PoWBits   uint8                   `json:"pow_bits,omitempty"`
	ReadOnly  bool                    `json:"read_only,omitempty"`
	PriceHint string                  `json:"price_hint,omitempty"`
	Banned    bool                    `json:"banned,omitempty"`
}

// GroupSendArgs is the RPC input for GroupSend.
type GroupSendArgs struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// GroupFileKeyArgs is the RPC input for GroupFileKey: which group, and
// which attachment within it.
type GroupFileKeyArgs struct {
	ID     string `json:"id"`
	FileID string `json:"file_id"`
}

// GroupFileKeyResult carries the keys for one group attachment.
//
// Two fields rather than one because the two uses differ: sealing needs
// exactly the current key, while opening has to try the retired ones too
// (an attachment shared before a rotation must keep opening afterwards,
// the same way message history does).
//
// Both are keys DERIVED for this one file id — the group key itself never
// crosses the RPC boundary. See the group package's filekey.go.
type GroupFileKeyResult struct {
	// Seal is the key to encrypt a new attachment with. Nil for a
	// plaintext (public) group, where sealing would protect nothing: the
	// caller sends such files as-is.
	Seal []byte `json:"seal,omitempty"`

	// Open is every key that may open an existing attachment, current
	// epoch first then the ring — the order that makes the common case a
	// single trial decryption.
	Open [][]byte `json:"open,omitempty"`

	// Epoch is the group's current key generation, for diagnostics.
	Epoch uint64 `json:"epoch,omitempty"`

	// Encrypted reports whether this group seals attachments at all,
	// distinguishing "public group, nothing to do" from "encrypted group
	// whose key we somehow lack" — which is an error, not a no-op.
	Encrypted bool `json:"encrypted"`
}

// GroupJoinRequest is one entry in a group's admission queue.
type GroupJoinRequest struct {
	GroupID   string                  `json:"group_id"`
	PK        cipher.PubKey           `json:"pk"`
	Note      string                  `json:"note,omitempty"`
	AskedAt   time.Time               `json:"asked_at"`
	Status    skychatgroup.JoinStatus `json:"status"`
	DecidedAt time.Time               `json:"decided_at,omitempty"`
	DecidedBy cipher.PubKey           `json:"decided_by,omitempty"`
}

// GroupSetMetaArgs is the input for GroupSetMeta. Zero-valued halves stay
// untouched, so one call can rename, re-picture, or both.
type GroupSetMetaArgs struct {
	ID string `json:"id"`

	// Name is the new display name; empty keeps the current one.
	Name string `json:"name,omitempty"`

	// Avatar is the new picture as a data URI (or bare base64), consulted
	// only when SetAvatar is true. SetAvatar with an empty Avatar clears
	// the picture — the flag is what tells "clear it" apart from "leave
	// it alone".
	Avatar    string `json:"avatar,omitempty"`
	SetAvatar bool   `json:"set_avatar,omitempty"`
}

// GroupCatalogEntry is the RPC-facing shape of one discovered group.
//
// Port and PoWBits are deliberately absent: they are join mechanics the
// visor uses internally, and a caller acts on Address, which carries
// everything needed to join and nothing a UI has to understand.
type GroupCatalogEntry struct {
	ID        string                  `json:"id"`
	HostPK    cipher.PubKey           `json:"host_pk"`
	Name      string                  `json:"name"`
	Kind      skychatgroup.Kind       `json:"kind"`
	Mode      skychatgroup.Mode       `json:"mode"`
	Policy    skychatgroup.JoinPolicy `json:"policy"`
	PriceHint string                  `json:"price_hint,omitempty"`
	Address   string                  `json:"address"`
}

// GroupUnsendArgs is the RPC input for GroupUnsend. TS is the message's
// UnixNano timestamp (the value carried in the delivered message).
type GroupUnsendArgs struct {
	ID string `json:"id"`
	TS int64  `json:"ts"`
}

// GroupHistoryPageArgs is the RPC input for GroupHistoryPage.
type GroupHistoryPageArgs struct {
	GroupID string `json:"group_id"`
	// Before is the exclusive upper bound — the oldest message the caller
	// already has. Zero means "the newest page".
	Before time.Time `json:"before,omitempty"`
	Limit  int       `json:"limit,omitempty"`
}

// GroupCreateResponse pairs the persisted GroupInfo with the
// freshly-encoded invite link the operator should distribute.
type GroupCreateResponse struct {
	Info   GroupInfo `json:"info"`
	Invite string    `json:"invite"`
}

// GroupAddMemberRequest is the input to RPC.GroupAddMember.
type GroupAddMemberRequest struct {
	ID    string        `json:"id"`
	NewPK cipher.PubKey `json:"new_pk"`
}

// GroupPollRequest is the input to RPC.GroupPoll.
type GroupPollRequest struct {
	Since time.Time `json:"since"`
}

// GroupSetListedRequest is the input to RPC.GroupSetListed.
type GroupSetListedRequest struct {
	ID     string `json:"id"`
	Listed bool   `json:"listed"`
}

// GroupCatalogResponse pairs the discovered entries with whether the host
// had more than it sent.
type GroupCatalogResponse struct {
	Entries   []GroupCatalogEntry `json:"entries"`
	Truncated bool                `json:"truncated,omitempty"`
}

// GroupPromoteAdminRequest is the input to RPC.GroupPromoteAdmin /
// GroupDemoteAdmin. Shape mirrors GroupAddMemberRequest.
type GroupPromoteAdminRequest struct {
	ID string        `json:"id"`
	PK cipher.PubKey `json:"pk"`
}

// GroupPeerRequest is the (group, peer) input shared by every
// admission + moderation command: approve, deny, remove, ban, unban,
// mute, unmute. One request type rather than seven identical ones —
// the method name already carries the verb.
type GroupPeerRequest struct {
	ID string        `json:"id"`
	PK cipher.PubKey `json:"pk"`
}

// GroupReadOnlyRequest toggles group-wide read-only.
type GroupReadOnlyRequest struct {
	ID       string `json:"id"`
	ReadOnly bool   `json:"read_only"`
}

// GroupPeerBackfillRequest toggles whether any online member may serve
// the group's history to a joiner.
type GroupPeerBackfillRequest struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

// GroupJoinPoWRequest sets the join proof-of-work difficulty.
type GroupJoinPoWRequest struct {
	ID   string `json:"id"`
	Bits uint8  `json:"bits"`
}

// GroupHistoryRequest is the input shape for GroupHistory. GroupID is
// required; Limit caps the result set (0 = all).
type GroupHistoryRequest struct {
	GroupID string `json:"group_id"`
	Limit   int    `json:"limit"`
}
