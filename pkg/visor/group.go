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
	"time"

	skychatgroup "github.com/skycoin/skywire/cmd/apps/skychat/group"
	"github.com/skycoin/skywire/pkg/cipher"
)

// GroupInfo is the public summary of a chat group, returned by
// GroupList and GroupGet.
type GroupInfo struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	OwnerPK       cipher.PubKey       `json:"owner_pk"`
	Port          uint16              `json:"port"`
	Mode          skychatgroup.Mode   `json:"mode"`
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
	return info, nil
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
type groupInbox struct {
	mu  sync.Mutex
	cap int
	buf []GroupMessage
	mgr *skychatgroup.Manager
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
	g.mu.Lock()
	g.buf = append(g.buf, GroupMessage{
		GroupID:  groupID,
		SenderPK: senderPK,
		Text:     msg.Text,
		TS:       msg.TS,
	})
	if len(g.buf) > g.cap {
		drop := len(g.buf) - g.cap
		g.buf = append(g.buf[:0], g.buf[drop:]...)
	}
	mgr := g.mgr
	g.mu.Unlock()
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
