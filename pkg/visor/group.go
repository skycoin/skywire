// Package visor pkg/visor/group.go c3-vis-core
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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	skychataddr "github.com/skycoin/skywire/pkg/skychat/address"
	skychatgroup "github.com/skycoin/skywire/pkg/skychat/group"
	skychatprofile "github.com/skycoin/skywire/pkg/skychat/profile"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

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
func (v *Visor) GroupCreate(args visorapi.GroupCreateArgs) (visorapi.GroupInfo, string, error) {
	mgr := v.groupManager()
	if mgr == nil {
		return visorapi.GroupInfo{}, "", ErrGroupingDisabled
	}
	r, err := mgr.Create(args.Name, args.ResolvedKind(), args.InitialMembers,
		skychatgroup.WithPeerBackfill(!args.DisablePeerBackfill),
		skychatgroup.WithListed(args.Listed))
	if err != nil {
		return visorapi.GroupInfo{}, "", err
	}
	link, err := mgr.BuildInvite(r.ID)
	if err != nil {
		return visorapi.GroupInfo{}, "", err
	}
	return toInfo(r), link, nil
}

// GroupJoin accepts an invite link OR a short skychat:// group address,
// registers a member-side record, and opens the subscriber. Returns the
// info on the joined group.
//
// An address route costs one extra round trip (the describe) before the
// join proper; a link goes straight to the join. Both converge on
// Manager.RequestJoin, so admission policy, proof of work and rate
// limiting are identical either way — the address is a shorter way to
// name the group, not a shortcut past its door.
func (v *Visor) GroupJoin(args visorapi.GroupJoinArgs) (visorapi.GroupInfo, error) {
	mgr := v.groupManager()
	if mgr == nil {
		return visorapi.GroupInfo{}, ErrGroupingDisabled
	}
	// An invite link is preferred when both are given: it needs no
	// network to interpret and carries strictly more than an address.
	raw := strings.TrimSpace(args.Invite)
	if raw == "" {
		raw = strings.TrimSpace(args.Address)
	}
	if raw == "" {
		return visorapi.GroupInfo{}, errors.New("grouping: join: invite or address required")
	}
	if skychataddr.IsInvite(raw) {
		inv, err := skychatgroup.DecodeInvite(raw)
		if err != nil {
			return visorapi.GroupInfo{}, err
		}
		r, err := mgr.Join(inv)
		if err != nil {
			return visorapi.GroupInfo{}, err
		}
		return toInfo(r), nil
	}
	addr, err := skychataddr.Parse(raw)
	if err != nil {
		return visorapi.GroupInfo{}, err
	}
	if !addr.IsGroup() {
		return visorapi.GroupInfo{}, errors.New("grouping: join: that address names a person, not a group — open a direct message instead")
	}
	ctx, cancel := context.WithTimeout(context.Background(), groupJoinByAddressBudget)
	defer cancel()
	r, err := mgr.JoinByAddress(ctx, addr.PK, addr.GroupID)
	if err != nil {
		return visorapi.GroupInfo{}, err
	}
	return toInfo(r), nil
}

// groupJoinByAddressBudget caps a join driven from a short address: one
// describe round trip plus the join fan-out behind it. Generous, because
// it is a deliberate user action where a spurious timeout is worse than a
// slow success — the same reasoning as joinResponseReadTimeout.
const groupJoinByAddressBudget = 45 * time.Second

// GroupResolve says what a skychat address points at: a person, or a
// group/channel and what joining it involves.
//
// This is what lets one input field accept anything a user can hold. A
// bare public key is answered locally — a key naming a person and a key
// hosting a channel are the same string, so only the address's shape can
// distinguish them, and a DM needs no permission to open. A group address
// is answered from this visor's own store when it already knows the
// group, and otherwise by asking the host.
func (v *Visor) GroupResolve(args visorapi.GroupResolveArgs) (visorapi.GroupResolveResult, error) {
	raw := strings.TrimSpace(args.Address)
	if raw == "" {
		return visorapi.GroupResolveResult{}, errors.New("grouping: resolve: address required")
	}

	// A pasted invite link is answered from the link itself. No round
	// trip, and it works while the host is offline — which is the reason
	// links still exist alongside addresses.
	if skychataddr.IsInvite(raw) {
		inv, err := skychatgroup.DecodeInvite(raw)
		if err != nil {
			return visorapi.GroupResolveResult{}, err
		}
		res := visorapi.GroupResolveResult{
			Target:  string(skychataddr.KindGroup),
			PK:      inv.OwnerPK,
			Address: skychataddr.Group(inv.OwnerPK, inv.ID).String(),
			Invite:  raw,
			Group: &visorapi.GroupDescriptor{
				ID: inv.ID, HostPK: inv.OwnerPK, Name: inv.Name,
				Kind: inv.InviteKind(), Mode: inv.Mode,
				Policy:  inv.InviteKind().Policy(),
				Port:    inv.Port,
				PoWBits: inv.PoWBits,
			},
		}
		v.fillGroupMembership(&res, inv.ID)
		return res, nil
	}

	addr, err := skychataddr.Parse(raw)
	if err != nil {
		return visorapi.GroupResolveResult{}, err
	}
	if !addr.IsGroup() {
		return visorapi.GroupResolveResult{
			Target:  string(skychataddr.KindDM),
			PK:      addr.PK,
			Address: addr.String(),
		}, nil
	}

	mgr := v.groupManager()
	if mgr == nil {
		return visorapi.GroupResolveResult{}, ErrGroupingDisabled
	}
	ctx, cancel := context.WithTimeout(context.Background(), groupResolveBudget)
	defer cancel()
	d, err := mgr.ProbeGroup(ctx, addr.PK, addr.GroupID)
	if err != nil {
		return visorapi.GroupResolveResult{}, err
	}
	res := visorapi.GroupResolveResult{
		Target:  string(skychataddr.KindGroup),
		PK:      addr.PK,
		Address: addr.String(),
		Group: &visorapi.GroupDescriptor{
			ID: d.ID, HostPK: d.HostPK, Name: d.Name,
			Kind: d.Kind, Mode: d.Mode, Policy: d.Policy,
			Port: d.Port, PoWBits: d.PoWBits, ReadOnly: d.ReadOnly,
			PriceHint: d.PriceHint, Banned: d.Banned,
		},
	}
	v.fillGroupMembership(&res, d.ID)
	return res, nil
}

// groupResolveBudget caps one describe. Sits directly under a user
// watching a dialog, so it is deliberately tighter than the join budget.
const groupResolveBudget = 15 * time.Second

// fillGroupMembership annotates a resolve result with what this visor
// already knows about the group. Without it the UI would offer "Join" for
// a group the operator is already in, and would offer it again to someone
// whose request is still queued.
func (v *Visor) fillGroupMembership(res *visorapi.GroupResolveResult, id string) {
	mgr := v.groupManager()
	if mgr == nil {
		return
	}
	r, ok, err := mgr.Get(id)
	if err != nil || !ok {
		return
	}
	res.Status = r.Status
	res.Joined = r.Status == skychatgroup.StatusActive || r.Status == skychatgroup.StatusPending
}

// GroupAskAgain re-submits a join request an admin has DECLINED — the UI's
// "ask again" action. Everything is derived from the stored record, so the
// caller passes only the group id; the request pays the same PoW and
// rate-limit gates as a first ask (see group.Manager.AskAgain).
func (v *Visor) GroupAskAgain(id string) (visorapi.GroupInfo, error) {
	mgr := v.groupManager()
	if mgr == nil {
		return visorapi.GroupInfo{}, ErrGroupingDisabled
	}
	r, err := mgr.AskAgain(id, "")
	if err != nil {
		return visorapi.GroupInfo{}, err
	}
	return toInfo(r), nil
}

// GroupList returns every persisted group on this visor. The
// SubscriberAlive field is populated from the live session map —
// the only API surface where that's needed (the chat-app's /status
// renders per-group health from this).
func (v *Visor) GroupList() ([]visorapi.GroupInfo, error) {
	mgr := v.groupManager()
	if mgr == nil {
		return nil, ErrGroupingDisabled
	}
	all, err := mgr.List()
	if err != nil {
		return nil, err
	}
	subDrop := v.groupSubDropCount()
	deliverCount := v.groupDeliverCount()
	streamSendCount := v.groupStreamSendCount()
	out := make([]visorapi.GroupInfo, 0, len(all))
	for _, r := range all {
		info := v.toInfoFor(r, mgr)
		info.SubscriberAlive = mgr.IsSubscriberAlive(r.ID)
		info.PeerLastInbound = peerLivenessHex(mgr.PeerLiveness(r.ID))
		info.SubDropCount = subDrop
		info.DeliverCount = deliverCount
		info.StreamSendCount = streamSendCount
		info.PeerUpdateCount = peerUpdateCountHex(mgr.PeerUpdateCount(r.ID))
		out = append(out, info)
	}
	return out, nil
}

// groupSubDropCount returns the inbox-wide running total of drops on
// the gRPC StreamGroupMessages fan-out path. Returns 0 when the
// grouping subsystem is uninitialized or has no inbox attached
// (matches the existing pattern for nil-state graceful degradation).
//
// Repeated across every GroupInfo in GroupList / GroupGet rather
// than living in its own RPC because operators viewing per-group
// /status entries shouldn't have to make a separate call to find
// the drop count. The value is the same across all groups on this
// visor — see GroupInfo.SubDropCount docstring.
func (v *Visor) groupSubDropCount() uint64 {
	v.initLock.RLock()
	inbox := v.grouping.inbox
	v.initLock.RUnlock()
	if inbox == nil {
		return 0
	}
	return inbox.SubDropCount()
}

// groupStreamSendCount returns the running total of successful
// stream.Send calls on rpcgrpc.StreamGroupMessages across both
// PingServer instances (local CLI + dmsg-RPC). Surfaced via
// GroupInfo.StreamSendCount; visor-wide, not group-scoped (matches
// the other layer counters' semantics).
//
// Subscribed sentinels are NOT counted. The counter is monotonic
// across the visor's lifetime; reset only on visor restart.
func (v *Visor) groupStreamSendCount() uint64 {
	return v.groupStreamSendCounter.Load()
}

// groupDeliverCount returns the running total of groupInbox.deliver()
// invocations since the visor started — i.e. every message that
// landed at the inbox layer, regardless of downstream consumption.
// Operator-facing: the (deliverCount) − (per-peer update count
// sum) delta tells you what fraction of messages reached the inbox
// vs. were lost between the CXO peer-subscriber's OnUpdate and the
// inbox enqueue. Returns 0 on uninitialized grouping. Visor-wide,
// not per-group (matches SubDropCount semantics).
func (v *Visor) groupDeliverCount() uint64 {
	v.initLock.RLock()
	inbox := v.grouping.inbox
	v.initLock.RUnlock()
	if inbox == nil {
		return 0
	}
	return inbox.deliverCount.Load()
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
func (v *Visor) GroupGet(id string) (visorapi.GroupInfo, error) {
	mgr := v.groupManager()
	if mgr == nil {
		return visorapi.GroupInfo{}, ErrGroupingDisabled
	}
	r, ok, err := mgr.Get(id)
	if err != nil {
		return visorapi.GroupInfo{}, err
	}
	if !ok {
		return visorapi.GroupInfo{}, ErrGroupNotFound
	}
	info := v.toInfoFor(r, mgr)
	info.SubscriberAlive = mgr.IsSubscriberAlive(r.ID)
	info.PeerLastInbound = peerLivenessHex(mgr.PeerLiveness(r.ID))
	info.SubDropCount = v.groupSubDropCount()
	info.DeliverCount = v.groupDeliverCount()
	info.StreamSendCount = v.groupStreamSendCount()
	info.PeerUpdateCount = peerUpdateCountHex(mgr.PeerUpdateCount(r.ID))
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

// peerUpdateCountHex is the parallel converter for the per-peer
// OnUpdate-callback counts from Manager.PeerUpdateCount. Same
// empty-input → nil → JSON-omitted convention.
func peerUpdateCountHex(in map[cipher.PubKey]uint64) map[string]uint64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]uint64, len(in))
	for pk, c := range in {
		out[pk.Hex()] = c
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
func (v *Visor) GroupAddMember(id string, pk cipher.PubKey) (visorapi.GroupInfo, error) {
	mgr := v.groupManager()
	if mgr == nil {
		return visorapi.GroupInfo{}, ErrGroupingDisabled
	}
	r, err := mgr.AddMember(id, pk)
	if err != nil {
		return visorapi.GroupInfo{}, err
	}
	return toInfo(r), nil
}

func toJoinRequest(r skychatgroup.JoinRequest) visorapi.GroupJoinRequest {
	return visorapi.GroupJoinRequest{
		GroupID:   r.GroupID,
		PK:        r.PK,
		Note:      r.Note,
		AskedAt:   r.AskedAt,
		Status:    r.Status,
		DecidedAt: r.DecidedAt,
		DecidedBy: r.DecidedBy,
	}
}

// GroupJoinRequests returns the admission queue for a group, newest
// first. Includes decided entries so callers can show history; filter
// on Status == "pending" for the actionable set.
func (v *Visor) GroupJoinRequests(id string) ([]visorapi.GroupJoinRequest, error) {
	mgr := v.groupManager()
	if mgr == nil {
		return nil, ErrGroupingDisabled
	}
	reqs, err := mgr.PendingJoins(id)
	if err != nil {
		return nil, err
	}
	out := make([]visorapi.GroupJoinRequest, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, toJoinRequest(r))
	}
	return out, nil
}

// GroupApproveJoin admits a queued requester. Admin-only.
func (v *Visor) GroupApproveJoin(id string, pk cipher.PubKey) (visorapi.GroupInfo, error) {
	return v.groupRosterOp(id, pk, func(mgr *skychatgroup.Manager) (skychatgroup.Record, error) {
		return mgr.ApproveJoin(id, pk)
	})
}

// GroupDenyJoin declines a queued request. Admin-only.
func (v *Visor) GroupDenyJoin(id string, pk cipher.PubKey) error {
	mgr := v.groupManager()
	if mgr == nil {
		return ErrGroupingDisabled
	}
	return mgr.DenyJoin(id, pk)
}

// GroupRemoveMember evicts a peer from the roster. Admin-only. A kick,
// not a ban — the peer may ask to rejoin.
func (v *Visor) GroupRemoveMember(id string, pk cipher.PubKey) (visorapi.GroupInfo, error) {
	return v.groupRosterOp(id, pk, func(mgr *skychatgroup.Manager) (skychatgroup.Record, error) {
		return mgr.RemoveMember(id, pk)
	})
}

// GroupBanMember bars a peer from the group. Admin-only.
func (v *Visor) GroupBanMember(id string, pk cipher.PubKey) (visorapi.GroupInfo, error) {
	return v.groupRosterOp(id, pk, func(mgr *skychatgroup.Manager) (skychatgroup.Record, error) {
		return mgr.BanMember(id, pk)
	})
}

// GroupUnbanMember lifts a ban. The peer must ask to join again.
func (v *Visor) GroupUnbanMember(id string, pk cipher.PubKey) (visorapi.GroupInfo, error) {
	return v.groupRosterOp(id, pk, func(mgr *skychatgroup.Manager) (skychatgroup.Record, error) {
		return mgr.UnbanMember(id, pk)
	})
}

// GroupMuteMember restricts a peer from posting. Admin-only.
func (v *Visor) GroupMuteMember(id string, pk cipher.PubKey) (visorapi.GroupInfo, error) {
	return v.groupRosterOp(id, pk, func(mgr *skychatgroup.Manager) (skychatgroup.Record, error) {
		return mgr.MuteMember(id, pk)
	})
}

// GroupUnmuteMember lifts a posting restriction. Admin-only.
func (v *Visor) GroupUnmuteMember(id string, pk cipher.PubKey) (visorapi.GroupInfo, error) {
	return v.groupRosterOp(id, pk, func(mgr *skychatgroup.Manager) (skychatgroup.Record, error) {
		return mgr.UnmuteMember(id, pk)
	})
}

// GroupSetReadOnly suspends (or resumes) posting for every non-admin.
// Admin-only.
func (v *Visor) GroupSetReadOnly(id string, readOnly bool) (visorapi.GroupInfo, error) {
	return v.groupRosterOp(id, cipher.PubKey{}, func(mgr *skychatgroup.Manager) (skychatgroup.Record, error) {
		return mgr.SetReadOnly(id, readOnly)
	})
}

// GroupSetJoinPoW sets how much proof of work a join request must carry,
// in leading zero bits. Zero turns the price off. Admin-only.
//
// The lever for a group being flooded: public keys are free, so this is
// what makes each identity cost measurable CPU. Local to this visor —
// see skychatgroup.Record.JoinPoWBits.
func (v *Visor) GroupSetJoinPoW(id string, bits uint8) (visorapi.GroupInfo, error) {
	return v.groupRosterOp(id, cipher.PubKey{}, func(mgr *skychatgroup.Manager) (skychatgroup.Record, error) {
		return mgr.SetJoinPoW(id, bits)
	})
}

// GroupSetPeerBackfill decides whether any online member may serve this
// group's history and messages to a joiner, or only admins. Admin-only.
//
// Enabled (the default) is what keeps a group readable while its admins
// are offline; disabled restores the admins-only topology and the group
// goes dark whenever no admin is up.
func (v *Visor) GroupSetPeerBackfill(id string, enabled bool) (visorapi.GroupInfo, error) {
	return v.groupRosterOp(id, cipher.PubKey{}, func(mgr *skychatgroup.Manager) (skychatgroup.Record, error) {
		return mgr.SetPeerBackfill(id, enabled)
	})
}

// GroupSetListed publishes (or un-publishes) a group in this visor's
// discovery catalog. Admin-only, and local rather than gossiped — it says
// what this visor answers questions about, not what the group is.
func (v *Visor) GroupSetListed(id string, listed bool) (visorapi.GroupInfo, error) {
	return v.groupRosterOp(id, cipher.PubKey{}, func(mgr *skychatgroup.Manager) (skychatgroup.Record, error) {
		return mgr.SetListed(id, listed)
	})
}

// GroupSetMeta updates a group's display metadata — name, picture, or
// both. Founder-only (the manager enforces it): members mirror the
// founding visor's record and re-learn these fields from it, so an edit
// anywhere else would only un-happen.
func (v *Visor) GroupSetMeta(args visorapi.GroupSetMetaArgs) (visorapi.GroupInfo, error) {
	mgr := v.groupManager()
	if mgr == nil {
		return visorapi.GroupInfo{}, ErrGroupingDisabled
	}
	var (
		r       skychatgroup.Record
		err     error
		touched bool
	)
	if name := strings.TrimSpace(args.Name); name != "" {
		if r, err = mgr.Rename(args.ID, name); err != nil {
			return visorapi.GroupInfo{}, err
		}
		touched = true
	}
	if args.SetAvatar {
		var raw []byte
		if args.Avatar != "" {
			if raw, err = skychatprofile.DecodeAvatar(args.Avatar); err != nil {
				return visorapi.GroupInfo{}, err
			}
		}
		if r, err = mgr.SetAvatar(args.ID, raw); err != nil {
			return visorapi.GroupInfo{}, err
		}
		touched = true
	}
	if !touched {
		return visorapi.GroupInfo{}, errors.New("grouping: set meta: nothing to change")
	}
	return v.toInfoFor(r, mgr), nil
}

// GroupRefreshMeta re-reads a group's display metadata from its founding
// visor and returns the (possibly updated) info. Best-effort: an
// unreachable founder returns the local record unchanged rather than an
// error, because the caller is a UI opening a chat, and the founder being
// asleep is not a problem the person opening it can act on.
func (v *Visor) GroupRefreshMeta(id string) (visorapi.GroupInfo, error) {
	mgr := v.groupManager()
	if mgr == nil {
		return visorapi.GroupInfo{}, ErrGroupingDisabled
	}
	ctx, cancel := context.WithTimeout(context.Background(), groupResolveBudget)
	defer cancel()
	r, _, err := mgr.RefreshMeta(ctx, id)
	if err != nil {
		return visorapi.GroupInfo{}, err
	}
	return v.toInfoFor(r, mgr), nil
}

// GroupCatalog asks a visor what groups and channels it publishes.
//
// Answered locally when host is this visor, so an operator can see their
// own listing exactly as others would without a network round trip.
func (v *Visor) GroupCatalog(host cipher.PubKey) ([]visorapi.GroupCatalogEntry, bool, error) {
	mgr := v.groupManager()
	if mgr == nil {
		return nil, false, ErrGroupingDisabled
	}
	var (
		entries   []skychatgroup.CatalogEntry
		truncated bool
		err       error
	)
	if host == (cipher.PubKey{}) || host == v.conf.PK {
		entries, truncated, err = mgr.Catalog()
		// The local path returns records, which carry no Address — the
		// remote path builds one from the host it dialed. Fill it in so
		// both answers have the same shape.
		for i := range entries {
			entries[i].Address = skychataddr.Group(entries[i].HostPK, entries[i].ID).String()
		}
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), groupCatalogBudget)
		defer cancel()
		entries, truncated, err = mgr.FetchCatalog(ctx, host)
	}
	if err != nil {
		return nil, false, err
	}
	out := make([]visorapi.GroupCatalogEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, visorapi.GroupCatalogEntry{
			ID: e.ID, HostPK: e.HostPK, Name: e.Name, Kind: e.Kind,
			Mode: e.Mode, Policy: e.Policy, PriceHint: e.PriceHint,
			Address: e.Address,
		})
	}
	return out, truncated, nil
}

// groupCatalogBudget caps one catalog fetch. Sits under a user watching a
// list appear, so it is tight for the same reason groupResolveBudget is.
const groupCatalogBudget = 15 * time.Second

// GroupRotateKey mints a new key for an encrypted group and hands it to
// every current member, each copy sealed to that member's own PK.
// Admin-only.
//
// Eviction rotates on its own; this is the operator-driven case — a key
// believed leaked, or tidying up after someone left out of band. Errors
// for a plaintext group, which has no key to rotate.
func (v *Visor) GroupRotateKey(id string) (visorapi.GroupInfo, error) {
	return v.groupRosterOp(id, cipher.PubKey{}, func(mgr *skychatgroup.Manager) (skychatgroup.Record, error) {
		return mgr.RotateKey(id)
	})
}

// groupRosterOp is the shared body of every admin command that mutates
// a group and returns its updated info.
func (v *Visor) groupRosterOp(_ string, _ cipher.PubKey, op func(*skychatgroup.Manager) (skychatgroup.Record, error)) (visorapi.GroupInfo, error) {
	mgr := v.groupManager()
	if mgr == nil {
		return visorapi.GroupInfo{}, ErrGroupingDisabled
	}
	r, err := op(mgr)
	if err != nil {
		return visorapi.GroupInfo{}, err
	}
	return v.toInfoFor(r, mgr), nil
}

// GroupPromoteAdmin adds pk to the group's Admins set. Callable by
// any existing admin (founder implicitly, or any explicit Admins
// entry). Returns the updated info — surfacing Admins so the caller
// can confirm the grant landed.
func (v *Visor) GroupPromoteAdmin(id string, pk cipher.PubKey) (visorapi.GroupInfo, error) {
	mgr := v.groupManager()
	if mgr == nil {
		return visorapi.GroupInfo{}, ErrGroupingDisabled
	}
	r, err := mgr.PromoteAdmin(id, pk)
	if err != nil {
		return visorapi.GroupInfo{}, err
	}
	return toInfo(r), nil
}

// GroupDemoteAdmin removes pk from the group's explicit Admins set.
// Refuses to demote the founder (immutable recovery anchor). Other
// admins can demote each other freely — the assumption is that admins
// trust each other by virtue of having been promoted.
func (v *Visor) GroupDemoteAdmin(id string, pk cipher.PubKey) (visorapi.GroupInfo, error) {
	mgr := v.groupManager()
	if mgr == nil {
		return visorapi.GroupInfo{}, ErrGroupingDisabled
	}
	r, err := mgr.DemoteAdmin(id, pk)
	if err != nil {
		return visorapi.GroupInfo{}, err
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
func (v *Visor) GroupSend(args visorapi.GroupSendArgs) error {
	mgr := v.groupManager()
	if mgr == nil {
		return ErrGroupingDisabled
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return mgr.SendToGroup(ctx, args.ID, args.Text)
}

// GroupFileKey returns the keys that seal and open one attachment of a
// group — derived per file, so the group key itself stays in the visor.
//
// The chat app calls this on both sides of an attachment's life: once to
// seal a file it is about to publish, and once per file it needs to render
// or serve. See the group package's filekey.go for the derivation and
// cmd/apps/skychat/commands/filecrypt.go for the container.
func (v *Visor) GroupFileKey(args visorapi.GroupFileKeyArgs) (visorapi.GroupFileKeyResult, error) {
	mgr := v.groupManager()
	if mgr == nil {
		return visorapi.GroupFileKeyResult{}, ErrGroupingDisabled
	}
	seal, open, epoch, err := mgr.FileKeys(args.ID, args.FileID)
	if err != nil {
		return visorapi.GroupFileKeyResult{}, err
	}
	return visorapi.GroupFileKeyResult{
		Seal:      seal,
		Open:      open,
		Epoch:     epoch,
		Encrypted: len(seal) > 0,
	}, nil
}

// GroupUnsend deletes a message the local visor published to the group,
// by its UnixNano timestamp. Only the sender can remove their own
// message; the deletion propagates to subscribers via CXO snapshot-diff.
func (v *Visor) GroupUnsend(args visorapi.GroupUnsendArgs) error {
	mgr := v.groupManager()
	if mgr == nil {
		return ErrGroupingDisabled
	}
	return mgr.Unsend(args.ID, args.TS)
}

// GroupPoll drains messages with TS > since. Mirrors PairPoll.
func (v *Visor) GroupPoll(since time.Time) ([]visorapi.GroupMessage, error) {
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
	ListByGroup(groupID string, limit int) ([]visorapi.GroupMessage, error)
	// ListGroupBefore returns up to limit messages strictly older than
	// `before`, newest last — the backward page cursor that lets a
	// client walk a large channel's backlog a chunk at a time instead
	// of pulling all of it to show anything. A zero `before` means
	// "from the newest", so it doubles as the first page.
	ListGroupBefore(groupID string, before time.Time, limit int) ([]visorapi.GroupMessage, error)
	// Groups returns the set of group IDs that have any stored
	// messages — useful for operator inspection ('cli skychat group
	// history --list-groups' style introspection).
	Groups() ([]string, error)
	// ListGroupSince returns every stored message for the named
	// group with TS strictly after `since`, oldest first. Powers the
	// gRPC StreamGroupMessages history-fallback path that backfills
	// a reconnecting subscriber whose disconnect gap is longer than
	// the in-memory inbox ring can cover.
	ListGroupSince(groupID string, since time.Time) ([]visorapi.GroupMessage, error)
}

// GroupHistory returns up to limit most recent persisted messages for
// the named group. Persistence is opt-in (see init_group.go's history
// store wiring); when disabled, returns ErrGroupHistoryDisabled.
// Mirrors GroupPoll's RPC shape but reads from disk instead of the
// in-memory ring buffer — operators can recover messages across visor
// restarts.
func (v *Visor) GroupHistory(groupID string, limit int) ([]visorapi.GroupMessage, error) {
	return v.GroupHistoryPage(visorapi.GroupHistoryPageArgs{GroupID: groupID, Limit: limit})
}

// GroupHistoryPage is GroupHistory with a backward cursor: up to Limit
// messages strictly older than Before, newest last.
//
// A zero Before asks for the newest page, so a client's first call and its
// subsequent "older, please" calls are the same request with the oldest
// timestamp it holds filled in. That is the whole mechanism behind reading
// a channel's backlog in chunks — see history.Store.ListGroupBefore.
func (v *Visor) GroupHistoryPage(args visorapi.GroupHistoryPageArgs) ([]visorapi.GroupMessage, error) {
	v.initLock.RLock()
	hist := v.grouping.history
	v.initLock.RUnlock()
	if hist == nil {
		return nil, ErrGroupHistoryDisabled
	}
	msgs, err := hist.ListGroupBefore(args.GroupID, args.Before, args.Limit)
	if err != nil {
		return nil, err
	}
	// Stores written by builds that predate the inbox's heartbeat filter
	// carry the owner's liveness probes as ordinary rows; a page may come
	// back short of Limit, which readers already tolerate.
	kept := msgs[:0]
	for _, m := range msgs {
		if m.Text != skychatgroup.HeartbeatMarker {
			kept = append(kept, m)
		}
	}
	return kept, nil
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

func toInfo(r skychatgroup.Record) visorapi.GroupInfo {
	info := visorapi.GroupInfo{
		ID:   r.ID,
		Name: r.Name,
		Avatar: skychatprofile.Profile{
			Avatar: r.Avatar, AvatarMime: r.AvatarMime,
		}.AvatarDataURI(),
		OwnerPK:       r.OwnerPK,
		Port:          r.Port,
		Mode:          r.Mode,
		Kind:          r.Kind,
		JoinPolicy:    r.JoinPolicy(),
		PostPolicy:    r.PostPolicy(),
		Admins:        append([]cipher.PubKey(nil), r.Admins...),
		Members:       append([]cipher.PubKey(nil), r.Members...),
		Banned:        append([]cipher.PubKey(nil), r.Banned...),
		Muted:         append([]cipher.PubKey(nil), r.Muted...),
		ReadOnly:      r.ReadOnly,
		PeerBackfill:  r.PeerBackfillEnabled(),
		Listed:        r.Listed,
		JoinPoWBits:   r.JoinPoWRequired(),
		KeyEpoch:      r.KeyEpoch,
		Role:          r.Role,
		Status:        r.Status,
		CreatedAt:     r.CreatedAt,
		JoinedAt:      r.JoinedAt,
		LastMessageAt: r.LastMessageAt,
		// Same condition as the message bodies: a public group has no key
		// to seal governance with, so it publishes roster/admin/mod
		// mutations in the clear and this stays false.
		GovernanceSealed: r.Encrypted(),
	}
	// Key age / time-to-rotation, for encrypted groups whose key has a
	// known issue time. A record from before KeyIssuedAt was stamped
	// leaves both at zero rather than reporting a made-up age.
	if r.Encrypted() && !r.KeyIssuedAt.IsZero() {
		age := time.Since(r.KeyIssuedAt.UTC())
		info.KeyAgeSeconds = int64(age / time.Second)
		info.KeyRotatesInSeconds = int64((skychatgroup.DefaultKeyMaxAge - age) / time.Second)
	}
	return info
}

// toInfoFor is toInfo plus the fields that depend on who is asking:
// whether this visor may post, and how many requests await its
// decision. Split out because toInfo is called from paths that have no
// manager handy (and don't need either answer).
func (v *Visor) toInfoFor(r skychatgroup.Record, mgr *skychatgroup.Manager) visorapi.GroupInfo {
	info := toInfo(r)
	canPost, reason := r.CanPost(v.conf.PK)
	info.CanPost, info.CannotPostReason = canPost, reason
	if mgr != nil && r.IsAdmin(v.conf.PK) {
		if reqs, err := mgr.PendingJoins(r.ID); err == nil {
			for _, q := range reqs {
				if q.IsPending() {
					info.PendingJoins++
				}
			}
		}
	}
	return info
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
	buf []visorapi.GroupMessage
	mgr *skychatgroup.Manager

	subsMu sync.RWMutex
	subs   map[*groupSub]struct{}

	// subDropTotal is the running accumulator of drop counts harvested
	// from torn-down subscribers at unsubscribe time. The live-counter
	// reads in SubDropCount sum this with each live sub's atomic.Uint64,
	// keeping the total monotonic across stream restarts. Atomic so
	// the read path doesn't need the subsMu write lock.
	subDropTotal atomic.Uint64

	// deliverCount ticks once per groupInbox.deliver() call — i.e. every
	// time a message lands in this visor's group inbox, regardless of
	// whether any live subscriber consumed it or any history sink
	// captured it. Operators use the (deliver_count) − (per-sub
	// receive_count) delta to localize whether drops are at the
	// inbox→sub fan-out (subDropCount visible) or further upstream
	// (peer-subscriber OnUpdate count vs deliver_count). Monotonic
	// across the inbox's lifetime; reset only on visor restart.
	deliverCount atomic.Uint64

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
	ch        chan visorapi.GroupMessage
	dropCount atomic.Uint64
}

func newGroupInbox(capacity int) *groupInbox {
	if capacity <= 0 {
		capacity = groupInboxCap
	}
	return &groupInbox{cap: capacity, buf: make([]visorapi.GroupMessage, 0, capacity)}
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
	// The filter IsHeartbeat was exposed for: the inbox is the user-facing
	// surface, and a heartbeat that slips past the session layer must not
	// reach subscribers, the ring, history, or last_message_at.
	if skychatgroup.IsHeartbeat(msg) {
		return
	}
	gm := visorapi.GroupMessage{
		GroupID:  groupID,
		SenderPK: senderPK,
		Text:     msg.Text,
		TS:       msg.TS,
	}
	g.deliverCount.Add(1)
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

func (g *groupInbox) snapshotAfter(since time.Time) []visorapi.GroupMessage {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]visorapi.GroupMessage, 0, len(g.buf))
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
	sub := &groupSub{ch: make(chan visorapi.GroupMessage, bufSize)}
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
		// Accumulate the departing subscriber's drop count into the
		// inbox-wide total so SubDropCount stays monotonic across
		// stream restarts. Doing it under subsMu keeps the live-sum
		// + accumulator from racing: a fresh subscribe is also
		// behind the mutex, so the read-then-accumulate sequence on
		// the same sub-instance is serialized.
		g.subDropTotal.Add(sub.dropCount.Load())
		close(sub.ch)
	}
	g.subsMu.Unlock()
}

// SubDropCount returns the rolling total of inbox-to-stream drops
// across every live + torn-down subscriber on this inbox. Drops
// happen in deliver's select+default when a subscriber's bounded
// channel is full; this method sums the live subscribers' running
// counters with the accumulator that carries the count forward
// across unsubscribe events. Inbox-wide (not per-group).
//
// Surfaced via GroupInfo.SubDropCount on every GroupList /
// GroupGet result; the chat-app's /status renders it alongside
// subscriber_alive + last_message_at so an operator can spot a
// silently-dropping stream under burst without reading visor logs.
func (g *groupInbox) SubDropCount() uint64 {
	total := g.subDropTotal.Load()
	g.subsMu.RLock()
	for sub := range g.subs {
		total += sub.dropCount.Load()
	}
	g.subsMu.RUnlock()
	return total
}
