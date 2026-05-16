// Package visor pkg/visor/group.go
//
// Visor-level wrapper around the cmd/apps/skychat/group package.
// Mirrors pkg/visor/pairing.go in shape: owns one group.Manager
// (initialized in init_group.go) plus an in-memory ring of recently
// received messages. RPC clients call GroupCreate / GroupJoin /
// GroupList / GroupSend / GroupPoll / GroupDelete / GroupLeave; the
// poll model is the same as PairPoll, so apps that already drain a
// pair-poll loop can layer on a group-poll loop with the same shape.
//
// Scope (v1, D1 owner-centric):
//
//   - Owners can create groups, invite via link, broadcast messages.
//   - Members can join via invite, read messages, leave.
//   - Member-side "send" is NOT in v1. Members read only; the owner
//     drives the conversation. Phase-2 (project memo group-chat
//     plan) adds member-side relay via the existing pair-message
//     wire with a group_id tag.
package visor

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	skychatgroup "github.com/skycoin/skywire/cmd/apps/skychat/group"
	"github.com/skycoin/skywire/pkg/cipher"
)

// GroupInfo is the public summary of a chat group, returned by
// GroupList and GroupGet.
type GroupInfo struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	OwnerPK cipher.PubKey     `json:"owner_pk"`
	Port    uint16            `json:"port"`
	Mode    skychatgroup.Mode `json:"mode"`
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
	Name           string            `json:"name"`
	Mode           skychatgroup.Mode `json:"mode"`
	InitialMembers []cipher.PubKey   `json:"initial_members,omitempty"`
}

// GroupJoinArgs is the RPC input for GroupJoin.
type GroupJoinArgs struct {
	Invite string `json:"invite"`
}

// GroupSendArgs is the RPC input for GroupSend.
type GroupSendArgs struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// ErrGroupingDisabled is returned by Visor group methods when the
// manager isn't initialized (dmsg unavailable at startup, or the
// bolt store failed to open).
var ErrGroupingDisabled = errors.New("grouping: manager not initialized")

// ErrGroupHistoryDisabled is returned by Visor.GroupHistory /
// GroupHistoryGroups when persistence is off (the default). Enable by
// setting Skychat.GroupHistoryDB or the equivalent config knob.
var ErrGroupHistoryDisabled = errors.New("grouping: history persistence not enabled")

// ErrGroupNotFound is returned for an ID the local store doesn't
// know about. Distinct from a generic error so the CLI can render
// "no such group" vs a transport-layer failure.
var ErrGroupNotFound = errors.New("grouping: group not found")

// GroupCreate constructs a new owner-side group on this visor.
// Returns the persisted info and the invite link so the operator
// can hand the link out.
func (v *Visor) GroupCreate(args GroupCreateArgs) (GroupInfo, string, error) {
	mgr := v.groupManager()
	if mgr == nil {
		return GroupInfo{}, "", ErrGroupingDisabled
	}
	r, err := mgr.Create(args.Name, args.Mode, args.InitialMembers)
	if err != nil {
		return GroupInfo{}, "", err
	}
	link, err := mgr.BuildInvite(r.ID)
	if err != nil {
		return GroupInfo{}, "", err
	}
	return toInfo(r), link, nil
}

// GroupJoin accepts an invite link, registers a member-side record,
// and opens the subscriber. Returns the info on the joined group.
func (v *Visor) GroupJoin(args GroupJoinArgs) (GroupInfo, error) {
	mgr := v.groupManager()
	if mgr == nil {
		return GroupInfo{}, ErrGroupingDisabled
	}
	inv, err := skychatgroup.DecodeInvite(args.Invite)
	if err != nil {
		return GroupInfo{}, err
	}
	r, err := mgr.Join(inv)
	if err != nil {
		return GroupInfo{}, err
	}
	return toInfo(r), nil
}

// GroupList returns every persisted group on this visor. The
// SubscriberAlive field is populated from the live session map —
// the only API surface where that's needed (the chat-app's /status
// renders per-group health from this).
func (v *Visor) GroupList() ([]GroupInfo, error) {
	mgr := v.groupManager()
	if mgr == nil {
		return nil, ErrGroupingDisabled
	}
	all, err := mgr.List()
	if err != nil {
		return nil, err
	}
	out := make([]GroupInfo, 0, len(all))
	for _, r := range all {
		info := toInfo(r)
		info.SubscriberAlive = mgr.IsSubscriberAlive(r.ID)
		info.PeerLastInbound = peerLivenessHex(mgr.PeerLiveness(r.ID))
		out = append(out, info)
	}
	return out, nil
}

// GroupGet returns the info for a specific group, or ErrGroupNotFound.
//
// Populates SubscriberAlive the same way GroupList does — without
// this, single-group queries (`cli skychat group info <id>`) always
// reported subscriber_alive=false regardless of the live session
// state, because toInfo doesn't reach into the session map. The
// list path was already correct; the single-group path silently
// dropped the field. Surfaced as a misleading symptom during the
// agent-coordination work today: persisted last_message_at would
// advance while subscriber_alive stayed false, and we kept chasing
// that as a session-state divergence when really it was the RPC
// shape leaving the field unpopulated.
func (v *Visor) GroupGet(id string) (GroupInfo, error) {
	mgr := v.groupManager()
	if mgr == nil {
		return GroupInfo{}, ErrGroupingDisabled
	}
	r, ok, err := mgr.Get(id)
	if err != nil {
		return GroupInfo{}, err
	}
	if !ok {
		return GroupInfo{}, ErrGroupNotFound
	}
	info := toInfo(r)
	info.SubscriberAlive = mgr.IsSubscriberAlive(r.ID)
	info.PeerLastInbound = peerLivenessHex(mgr.PeerLiveness(r.ID))
	return info, nil
}

// peerLivenessHex converts the cipher-keyed Manager.PeerLiveness map
// into a hex-keyed JSON-friendly map. Returns nil when the input map
// is empty so the JSON omitempty tag drops the field entirely for
// records with no peer-level telemetry (e.g. owner-role sessions, or
// when no live session exists).
func peerLivenessHex(in map[cipher.PubKey]time.Time) map[string]time.Time {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]time.Time, len(in))
	for pk, ts := range in {
		out[pk.Hex()] = ts
	}
	return out
}

// GroupInvite returns a freshly-encoded invite link for an
// owner-side group. Members get an error (only owners can issue
// invites in D1).
func (v *Visor) GroupInvite(id string) (string, error) {
	mgr := v.groupManager()
	if mgr == nil {
		return "", ErrGroupingDisabled
	}
	return mgr.BuildInvite(id)
}

// GroupAddMember extends the allowlist + persisted member list.
// Owner-side only.
func (v *Visor) GroupAddMember(id string, pk cipher.PubKey) (GroupInfo, error) {
	mgr := v.groupManager()
	if mgr == nil {
		return GroupInfo{}, ErrGroupingDisabled
	}
	r, err := mgr.AddMember(id, pk)
	if err != nil {
		return GroupInfo{}, err
	}
	return toInfo(r), nil
}

// GroupPromoteAdmin adds pk to the group's Admins set. Callable by
// any existing admin (founder implicitly, or any explicit Admins
// entry). Returns the updated info — surfacing Admins so the caller
// can confirm the grant landed.
func (v *Visor) GroupPromoteAdmin(id string, pk cipher.PubKey) (GroupInfo, error) {
	mgr := v.groupManager()
	if mgr == nil {
		return GroupInfo{}, ErrGroupingDisabled
	}
	r, err := mgr.PromoteAdmin(id, pk)
	if err != nil {
		return GroupInfo{}, err
	}
	return toInfo(r), nil
}

// GroupDemoteAdmin removes pk from the group's explicit Admins set.
// Refuses to demote the founder (immutable recovery anchor). Other
// admins can demote each other freely — the assumption is that admins
// trust each other by virtue of having been promoted.
func (v *Visor) GroupDemoteAdmin(id string, pk cipher.PubKey) (GroupInfo, error) {
	mgr := v.groupManager()
	if mgr == nil {
		return GroupInfo{}, ErrGroupingDisabled
	}
	r, err := mgr.DemoteAdmin(id, pk)
	if err != nil {
		return GroupInfo{}, err
	}
	return toInfo(r), nil
}

// GroupSend publishes a message into the named group's feed.
// Owners write directly; members open a dmsg stream to the owner's
// relay listener and submit, the owner re-publishes with sender
// attribution preserved. Either way the sender's own subscriber
// renders the message back into its inbox so the UX is consistent
// across roles.
//
// Bounded with a 30s context: a dead owner shouldn't hang an RPC
// caller forever. Members get a clean dial-timeout error they can
// surface to the operator.
func (v *Visor) GroupSend(args GroupSendArgs) error {
	mgr := v.groupManager()
	if mgr == nil {
		return ErrGroupingDisabled
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return mgr.SendToGroup(ctx, args.ID, args.Text)
}

// GroupPoll drains messages with TS > since. Mirrors PairPoll.
func (v *Visor) GroupPoll(since time.Time) ([]GroupMessage, error) {
	v.initLock.RLock()
	inbox := v.grouping.inbox
	v.initLock.RUnlock()
	if inbox == nil {
		return nil, ErrGroupingDisabled
	}
	return inbox.snapshotAfter(since), nil
}

// GroupHistoryFetcher is the read side of the group history store.
// Wired in init_group.go as an adapter around skychat/history.BoltStore
// when persistence is enabled. nil when disabled — callers must
// nil-check and return ErrGroupHistoryDisabled.
type GroupHistoryFetcher interface {
	// ListByGroup returns up to limit most recent messages for the
	// named group, newest last. Limit <= 0 returns all stored.
	ListByGroup(groupID string, limit int) ([]GroupMessage, error)
	// Groups returns the set of group IDs that have any stored
	// messages — useful for operator inspection ('cli skychat group
	// history --list-groups' style introspection).
	Groups() ([]string, error)
	// ListGroupSince returns every stored message for the named
	// group with TS strictly after `since`, oldest first. Powers the
	// gRPC StreamGroupMessages history-fallback path that backfills
	// a reconnecting subscriber whose disconnect gap is longer than
	// the in-memory inbox ring can cover.
	ListGroupSince(groupID string, since time.Time) ([]GroupMessage, error)
}

// GroupHistory returns up to limit most recent persisted messages for
// the named group. Persistence is opt-in (see init_group.go's history
// store wiring); when disabled, returns ErrGroupHistoryDisabled.
// Mirrors GroupPoll's RPC shape but reads from disk instead of the
// in-memory ring buffer — operators can recover messages across visor
// restarts.
func (v *Visor) GroupHistory(groupID string, limit int) ([]GroupMessage, error) {
	v.initLock.RLock()
	hist := v.grouping.history
	v.initLock.RUnlock()
	if hist == nil {
		return nil, ErrGroupHistoryDisabled
	}
	return hist.ListByGroup(groupID, limit)
}

// GroupHistoryGroups lists every group ID that has stored messages.
// Returns ErrGroupHistoryDisabled when persistence is off.
func (v *Visor) GroupHistoryGroups() ([]string, error) {
	v.initLock.RLock()
	hist := v.grouping.history
	v.initLock.RUnlock()
	if hist == nil {
		return nil, ErrGroupHistoryDisabled
	}
	return hist.Groups()
}

// GroupDelete tears down an owner-side group and marks it revoked.
func (v *Visor) GroupDelete(id string) error {
	mgr := v.groupManager()
	if mgr == nil {
		return ErrGroupingDisabled
	}
	return mgr.Delete(id)
}

// GroupLeave is the member-side counterpart of Delete.
func (v *Visor) GroupLeave(id string) error {
	mgr := v.groupManager()
	if mgr == nil {
		return ErrGroupingDisabled
	}
	return mgr.Leave(id)
}

func (v *Visor) groupManager() *skychatgroup.Manager {
	v.initLock.RLock()
	defer v.initLock.RUnlock()
	return v.grouping.manager
}

func toInfo(r skychatgroup.Record) GroupInfo {
	return GroupInfo{
		ID:            r.ID,
		Name:          r.Name,
		OwnerPK:       r.OwnerPK,
		Port:          r.Port,
		Mode:          r.Mode,
		Admins:        append([]cipher.PubKey(nil), r.Admins...),
		Members:       append([]cipher.PubKey(nil), r.Members...),
		Role:          r.Role,
		Status:        r.Status,
		CreatedAt:     r.CreatedAt,
		JoinedAt:      r.JoinedAt,
		LastMessageAt: r.LastMessageAt,
	}
}

// groupInbox is a bounded ring buffer of inbound group messages,
// drained by GroupPoll. Mirrors pairInbox exactly.
//
// mgr is set after construction via setManager; deliver() uses it to
// tick last_message_at on the persisted record so the indicator stays
// fresh independent of the wrapped-handler chain in
// group.Manager.openLocked. Nil-tolerant: if setManager hasn't been
// called the bookkeeping side-effect is skipped.
//
// subs is the live-subscriber set used by the gRPC StreamGroupMessages
// path. Each subscriber gets its own bounded channel; deliver() fans
// every inbound message out to all channels with select+default so a
// slow consumer can't block the inbox. Slow consumers see the dropCount
// for their subscription advance — they observe the drop, the rest of
// the system keeps moving. nil-safe; the legacy GroupPoll-only path
// works fine with subs unused.
type groupInbox struct {
	mu  sync.Mutex
	cap int
	buf []GroupMessage
	mgr *skychatgroup.Manager

	subsMu sync.RWMutex
	subs   map[*groupSub]struct{}

	// hist, when non-nil, gets a copy of every delivered message for
	// disk persistence. Best-effort: write errors are logged but never
	// block the in-memory ring or the live subscriber fan-out. nil
	// when persistence is disabled (the default).
	hist groupHistorySink
}

// groupHistorySink is the interface the inbox uses to persist group
// messages. Defined here (not imported from skychat/history) to keep
// pkg/visor independent of cmd/apps/skychat at the type level — the
// init_group.go wires an adapter that bridges to the concrete history
// store.
type groupHistorySink interface {
	// AppendGroup stores one message. Returns nil on success, or an
	// error that the caller should log but not propagate.
	AppendGroup(groupID, senderPK, text string, ts time.Time, outgoing bool) error
}

// groupSub is a single live-subscription registered against the inbox.
// Closed by the inbox on unsubscribe; the subscriber drains ch until it
// returns ok==false, then exits.
type groupSub struct {
	ch        chan GroupMessage
	dropCount atomic.Uint64
}

func newGroupInbox(capacity int) *groupInbox {
	if capacity <= 0 {
		capacity = groupInboxCap
	}
	return &groupInbox{cap: capacity, buf: make([]GroupMessage, 0, capacity)}
}

// setManager wires the group Manager so deliver() can refresh
// last_message_at on the persisted record after a successful push.
// Set once during init_group.go after both the Manager and inbox
// exist; subsequent calls overwrite atomically under the inbox mutex.
func (g *groupInbox) setManager(mgr *skychatgroup.Manager) {
	g.mu.Lock()
	g.mgr = mgr
	g.mu.Unlock()
}

func (g *groupInbox) deliver(groupID string, senderPK cipher.PubKey, msg skychatgroup.Message) {
	gm := GroupMessage{
		GroupID:  groupID,
		SenderPK: senderPK,
		Text:     msg.Text,
		TS:       msg.TS,
	}
	g.mu.Lock()
	g.buf = append(g.buf, gm)
	if len(g.buf) > g.cap {
		drop := len(g.buf) - g.cap
		g.buf = append(g.buf[:0], g.buf[drop:]...)
	}
	mgr := g.mgr
	g.mu.Unlock()

	// Fan-out to live subscribers (gRPC StreamGroupMessages path).
	// select+default so a backed-up subscriber never blocks delivery
	// of this message to anyone else or to the ring buffer above.
	// A subscriber that misses messages sees its dropCount tick; the
	// rest of the system is unaffected.
	g.subsMu.RLock()
	for sub := range g.subs {
		select {
		case sub.ch <- gm:
		default:
			sub.dropCount.Add(1)
		}
	}
	g.subsMu.RUnlock()

	// Best-effort persist. Errors logged at the sink layer; this path
	// never blocks delivery to the in-memory ring or the live
	// subscriber fan-out.
	if g.hist != nil {
		// outgoing is determined upstream — this deliver is the
		// inbound path, so messages here are by definition not from
		// us. The hist sink's adapter can override if it ever wires
		// the outbound side.
		_ = g.hist.AppendGroup(groupID, senderPK.Hex(), msg.Text, msg.TS, false) //nolint:errcheck
	}
	// Belt-and-suspenders: also tick the persisted last_message_at on
	// the manager. The wrapped MessageHandler installed by openLocked
	// is supposed to do this too, but during the 3-agent coordination
	// session some peers observed last_message_at not advancing even
	// while messages were arriving in their group-listen output —
	// suggesting at least one delivery path bypasses the wrapper.
	// Updating from inbox.deliver guarantees the indicator tracks
	// whatever actually lands in the inbox, regardless of which
	// upstream code path put it there.
	if mgr != nil {
		mgr.MarkMessageDelivered(groupID, msg.TS)
	}
}

func (g *groupInbox) snapshotAfter(since time.Time) []GroupMessage {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]GroupMessage, 0, len(g.buf))
	for _, m := range g.buf {
		if m.TS.After(since) {
			out = append(out, m)
		}
	}
	return out
}

// subscribe registers a live subscriber that receives every message
// passed to deliver() from this point forward. The returned channel is
// owned by the inbox: callers drain it until ok==false, which happens
// when unsubscribe is called. bufSize bounds the per-subscriber queue;
// bursts past it are dropped (counted via the returned sub's
// dropCount).
//
// Used by the gRPC StreamGroupMessages handler to push messages to the
// CLI without the net/rpc poll loop. The handler is responsible for
// calling unsubscribe on its way out (defer or ctx-done).
func (g *groupInbox) subscribe(bufSize int) *groupSub {
	if bufSize <= 0 {
		bufSize = 64
	}
	sub := &groupSub{ch: make(chan GroupMessage, bufSize)}
	g.subsMu.Lock()
	if g.subs == nil {
		g.subs = make(map[*groupSub]struct{})
	}
	g.subs[sub] = struct{}{}
	g.subsMu.Unlock()
	return sub
}

// unsubscribe removes sub from the live-subscriber set and closes its
// channel so the consumer's range/select exits cleanly. Safe to call
// once; double-call is a no-op.
func (g *groupInbox) unsubscribe(sub *groupSub) {
	if sub == nil {
		return
	}
	g.subsMu.Lock()
	if _, ok := g.subs[sub]; ok {
		delete(g.subs, sub)
		close(sub.ch)
	}
	g.subsMu.Unlock()
}
