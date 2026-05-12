// Package visor pkg/visor/rpc_group.go
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

// GroupSend publishes one message into the named group's feed.
// Owner-side only in v1.
func (r *RPC) GroupSend(req *GroupSendArgs, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "GroupSend", req)(nil, &err)
	if req == nil {
		return fmt.Errorf("nil request")
	}
	return r.visor.GroupSend(*req)
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
