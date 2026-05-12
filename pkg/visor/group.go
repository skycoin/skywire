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

// GroupList returns every persisted group on this visor.
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
		out = append(out, toInfo(r))
	}
	return out, nil
}

// GroupGet returns the info for a specific group, or ErrGroupNotFound.
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
	return toInfo(r), nil
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
// Owner-side only in v1. Members get "only owner can send" — the
// member-side relay path lands in a follow-up commit.
func (v *Visor) GroupSend(args GroupSendArgs) error {
	mgr := v.groupManager()
	if mgr == nil {
		return ErrGroupingDisabled
	}
	return mgr.SendToGroup(args.ID, args.Text)
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
type groupInbox struct {
	mu  sync.Mutex
	cap int
	buf []GroupMessage
}

func newGroupInbox(capacity int) *groupInbox {
	if capacity <= 0 {
		capacity = groupInboxCap
	}
	return &groupInbox{cap: capacity, buf: make([]GroupMessage, 0, capacity)}
}

func (g *groupInbox) deliver(groupID string, senderPK cipher.PubKey, msg skychatgroup.Message) {
	g.mu.Lock()
	defer g.mu.Unlock()
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
