// Package visor pkg/visor/rpc_group.go c3-vis-core
//
// RPC adapter for the chat-group feed manager. Thin wrappers over
// the Visor methods in group.go. Mirrors rpc_pairing.go shape.
package visor

import (
	"fmt"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
)

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

// GroupCreate constructs a new owner-side group on this visor and
// returns the persisted info + the invite link.
func (r *RPC) GroupCreate(req *GroupCreateArgs, out *GroupCreateResponse) (err error) {
	defer rpcutil.LogCall(r.log, "GroupCreate", req)(out, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	info, link, err := r.visor.GroupCreate(*req)
	if err != nil {
		return err
	}
	*out = GroupCreateResponse{Info: info, Invite: link}
	return nil
}

// GroupJoin accepts an invite link and registers a member-side
// record.
func (r *RPC) GroupJoin(req *GroupJoinArgs, out *GroupInfo) (err error) {
	defer rpcutil.LogCall(r.log, "GroupJoin", req)(out, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	info, err := r.visor.GroupJoin(*req)
	if err != nil {
		return err
	}
	*out = info
	return nil
}

// GroupResolve reports what a skychat address points at — a person, or a
// group/channel and what joining it involves.
func (r *RPC) GroupResolve(req *GroupResolveArgs, out *GroupResolveResult) (err error) {
	defer rpcutil.LogCall(r.log, "GroupResolve", req)(out, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	res, err := r.visor.GroupResolve(*req)
	if err != nil {
		return err
	}
	*out = res
	return nil
}

// GroupList returns every persisted group on this visor.
func (r *RPC) GroupList(_ *struct{}, out *[]GroupInfo) (err error) {
	defer rpcutil.LogCall(r.log, "GroupList", nil)(out, &err)
	all, err := r.visor.GroupList()
	if err != nil {
		return err
	}
	*out = all
	return nil
}

// GroupGet returns one group's info by ID.
func (r *RPC) GroupGet(id *string, out *GroupInfo) (err error) {
	defer rpcutil.LogCall(r.log, "GroupGet", id)(out, &err)
	if id == nil {
		return fmt.Errorf("nil request")
	}
	info, err := r.visor.GroupGet(*id)
	if err != nil {
		return err
	}
	*out = info
	return nil
}

// GroupInvite returns a freshly-encoded invite link for an
// owner-side group.
func (r *RPC) GroupInvite(id *string, out *string) (err error) {
	defer rpcutil.LogCall(r.log, "GroupInvite", id)(out, &err)
	if id == nil {
		return fmt.Errorf("nil request")
	}
	link, err := r.visor.GroupInvite(*id)
	if err != nil {
		return err
	}
	*out = link
	return nil
}

// GroupAddMember extends the allowlist + persisted member list.
func (r *RPC) GroupAddMember(req *GroupAddMemberRequest, out *GroupInfo) (err error) {
	defer rpcutil.LogCall(r.log, "GroupAddMember", req)(out, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	info, err := r.visor.GroupAddMember(req.ID, req.NewPK)
	if err != nil {
		return err
	}
	*out = info
	return nil
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

// GroupAskAgain re-submits a declined join request (the UI's "ask again").
func (r *RPC) GroupAskAgain(id *string, out *GroupInfo) (err error) {
	defer rpcutil.LogCall(r.log, "GroupAskAgain", id)(out, &err)
	info, err := r.visor.GroupAskAgain(*id)
	if err != nil {
		return err
	}
	*out = info
	return nil
}

// GroupJoinRequests returns the admission queue for a group.
func (r *RPC) GroupJoinRequests(id *string, out *[]GroupJoinRequest) (err error) {
	defer rpcutil.LogCall(r.log, "GroupJoinRequests", id)(out, &err)
	if id == nil {
		return fmt.Errorf("nil request")
	}
	reqs, err := r.visor.GroupJoinRequests(*id)
	if err != nil {
		return err
	}
	*out = reqs
	return nil
}

// GroupApproveJoin admits a queued requester.
func (r *RPC) GroupApproveJoin(req *GroupPeerRequest, out *GroupInfo) (err error) {
	defer rpcutil.LogCall(r.log, "GroupApproveJoin", req)(out, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	info, err := r.visor.GroupApproveJoin(req.ID, req.PK)
	if err != nil {
		return err
	}
	*out = info
	return nil
}

// GroupDenyJoin declines a queued request.
func (r *RPC) GroupDenyJoin(req *GroupPeerRequest, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "GroupDenyJoin", req)(nil, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	return r.visor.GroupDenyJoin(req.ID, req.PK)
}

// GroupRemoveMember evicts a peer from the roster.
func (r *RPC) GroupRemoveMember(req *GroupPeerRequest, out *GroupInfo) (err error) {
	defer rpcutil.LogCall(r.log, "GroupRemoveMember", req)(out, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	info, err := r.visor.GroupRemoveMember(req.ID, req.PK)
	if err != nil {
		return err
	}
	*out = info
	return nil
}

// GroupBanMember bars a peer from the group.
func (r *RPC) GroupBanMember(req *GroupPeerRequest, out *GroupInfo) (err error) {
	defer rpcutil.LogCall(r.log, "GroupBanMember", req)(out, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	info, err := r.visor.GroupBanMember(req.ID, req.PK)
	if err != nil {
		return err
	}
	*out = info
	return nil
}

// GroupUnbanMember lifts a ban.
func (r *RPC) GroupUnbanMember(req *GroupPeerRequest, out *GroupInfo) (err error) {
	defer rpcutil.LogCall(r.log, "GroupUnbanMember", req)(out, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	info, err := r.visor.GroupUnbanMember(req.ID, req.PK)
	if err != nil {
		return err
	}
	*out = info
	return nil
}

// GroupMuteMember restricts a peer from posting.
func (r *RPC) GroupMuteMember(req *GroupPeerRequest, out *GroupInfo) (err error) {
	defer rpcutil.LogCall(r.log, "GroupMuteMember", req)(out, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	info, err := r.visor.GroupMuteMember(req.ID, req.PK)
	if err != nil {
		return err
	}
	*out = info
	return nil
}

// GroupUnmuteMember lifts a posting restriction.
func (r *RPC) GroupUnmuteMember(req *GroupPeerRequest, out *GroupInfo) (err error) {
	defer rpcutil.LogCall(r.log, "GroupUnmuteMember", req)(out, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	info, err := r.visor.GroupUnmuteMember(req.ID, req.PK)
	if err != nil {
		return err
	}
	*out = info
	return nil
}

// GroupRotateKey mints a new key for an encrypted group and distributes
// it to every current member, sealed per member.
func (r *RPC) GroupRotateKey(id *string, out *GroupInfo) (err error) {
	defer rpcutil.LogCall(r.log, "GroupRotateKey", id)(out, &err)
	if id == nil {
		return fmt.Errorf("nil request")
	}
	info, err := r.visor.GroupRotateKey(*id)
	if err != nil {
		return err
	}
	*out = info
	return nil
}

// GroupPeerBackfillRequest toggles whether any online member may serve
// the group's history to a joiner.
type GroupPeerBackfillRequest struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

// GroupSetPeerBackfill sets the group's backfill-from-any-member policy.
func (r *RPC) GroupSetPeerBackfill(req *GroupPeerBackfillRequest, out *GroupInfo) (err error) {
	defer rpcutil.LogCall(r.log, "GroupSetPeerBackfill", req)(out, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	info, err := r.visor.GroupSetPeerBackfill(req.ID, req.Enabled)
	if err != nil {
		return err
	}
	*out = info
	return nil
}

// GroupJoinPoWRequest sets the join proof-of-work difficulty.
type GroupJoinPoWRequest struct {
	ID   string `json:"id"`
	Bits uint8  `json:"bits"`
}

// GroupSetJoinPoW sets how much proof of work a join request must carry.
func (r *RPC) GroupSetJoinPoW(req *GroupJoinPoWRequest, out *GroupInfo) (err error) {
	defer rpcutil.LogCall(r.log, "GroupSetJoinPoW", req)(out, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	info, err := r.visor.GroupSetJoinPoW(req.ID, req.Bits)
	if err != nil {
		return err
	}
	*out = info
	return nil
}

// GroupSetReadOnly suspends or resumes posting for non-admins.
func (r *RPC) GroupSetReadOnly(req *GroupReadOnlyRequest, out *GroupInfo) (err error) {
	defer rpcutil.LogCall(r.log, "GroupSetReadOnly", req)(out, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	info, err := r.visor.GroupSetReadOnly(req.ID, req.ReadOnly)
	if err != nil {
		return err
	}
	*out = info
	return nil
}

// GroupPromoteAdmin grants roster authority to PK on the named group.
// Callable by any existing admin on this visor.
func (r *RPC) GroupPromoteAdmin(req *GroupPromoteAdminRequest, out *GroupInfo) (err error) {
	defer rpcutil.LogCall(r.log, "GroupPromoteAdmin", req)(out, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	info, err := r.visor.GroupPromoteAdmin(req.ID, req.PK)
	if err != nil {
		return err
	}
	*out = info
	return nil
}

// GroupDemoteAdmin revokes roster authority from PK on the named
// group. Refuses to demote the founder (immutable recovery anchor).
func (r *RPC) GroupDemoteAdmin(req *GroupPromoteAdminRequest, out *GroupInfo) (err error) {
	defer rpcutil.LogCall(r.log, "GroupDemoteAdmin", req)(out, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	info, err := r.visor.GroupDemoteAdmin(req.ID, req.PK)
	if err != nil {
		return err
	}
	*out = info
	return nil
}

// GroupSend publishes one message into the named group's feed.
// Owner-side only in v1.
func (r *RPC) GroupSend(req *GroupSendArgs, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "GroupSend", req)(nil, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	return r.visor.GroupSend(*req)
}

// GroupUnsend deletes a message the local visor published, by UnixNano TS.
func (r *RPC) GroupUnsend(req *GroupUnsendArgs, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "GroupUnsend", req)(nil, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	return r.visor.GroupUnsend(*req)
}

// GroupPoll drains inbound group messages with TS strictly after Since.
func (r *RPC) GroupPoll(req *GroupPollRequest, out *[]GroupMessage) (err error) {
	defer rpcutil.LogCall(r.log, "GroupPoll", req)(out, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	msgs, err := r.visor.GroupPoll(req.Since)
	if err != nil {
		return err
	}
	*out = msgs
	return nil
}

// GroupDelete tears down an owner-side group.
func (r *RPC) GroupDelete(id *string, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "GroupDelete", id)(nil, &err)
	if id == nil {
		return fmt.Errorf("nil request")
	}
	return r.visor.GroupDelete(*id)
}

// GroupLeave is the member-side counterpart of GroupDelete.
func (r *RPC) GroupLeave(id *string, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "GroupLeave", id)(nil, &err)
	if id == nil {
		return fmt.Errorf("nil request")
	}
	return r.visor.GroupLeave(*id)
}

// GroupHistoryRequest is the input shape for GroupHistory. GroupID is
// required; Limit caps the result set (0 = all).
type GroupHistoryRequest struct {
	GroupID string `json:"group_id"`
	Limit   int    `json:"limit"`
}

// GroupHistory returns persisted group messages for a given group.
// Returns ErrGroupHistoryDisabled when persistence is off — operators
// enable it via Skychat.GroupHistoryDB in the visor config. Unlike
// GroupPoll (which drains the in-memory ring), this RPC reads from
// disk and survives visor restarts.
func (r *RPC) GroupHistory(req *GroupHistoryRequest, out *[]GroupMessage) (err error) {
	defer rpcutil.LogCall(r.log, "GroupHistory", req)(out, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	msgs, err := r.visor.GroupHistory(req.GroupID, req.Limit)
	if err != nil {
		return err
	}
	*out = msgs
	return nil
}

// GroupHistoryGroups returns every group ID that has stored messages.
// Returns ErrGroupHistoryDisabled when persistence is off.
func (r *RPC) GroupHistoryGroups(_ *struct{}, out *[]string) (err error) {
	defer rpcutil.LogCall(r.log, "GroupHistoryGroups", nil)(out, &err)
	groups, err := r.visor.GroupHistoryGroups()
	if err != nil {
		return err
	}
	*out = groups
	return nil
}

// GroupFileKey returns the per-file keys that seal and open one group
// attachment. The group key itself is not part of the response — see
// Visor.GroupFileKey.
//
// On the exposure: anyone who can reach this RPC can already call
// GroupPoll, which hands back decrypted message bodies, so a per-file
// attachment key grants strictly less than what the same caller has. What
// the scoping does buy is that the answer cannot be replayed against any
// OTHER file, or against the group's message history.
func (r *RPC) GroupFileKey(req *GroupFileKeyArgs, out *GroupFileKeyResult) (err error) {
	// Deliberately NOT logged through rpcutil.LogCall's result path: the
	// response carries key material, and an RPC log line is the one place
	// it would come to rest in plaintext.
	if req == nil {
		return fmt.Errorf("nil request")
	}
	res, err := r.visor.GroupFileKey(*req)
	if err != nil {
		return err
	}
	*out = res
	return nil
}
