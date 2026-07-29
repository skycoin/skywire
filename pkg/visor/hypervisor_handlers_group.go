// Package visor pkg/visor/hypervisor_handlers_group.go c3-vis-core
//
// Hypervisor HTTP routes for skychat GROUP chat — the native-visor
// counterpart to the wasm visor's skychatGroup* JS hooks. These thin
// handlers bridge the hvui's group panel to the local visor's group
// RPC surface (ctx.API.Group*), so the same Angular UI drives group
// chat on a native visor (over HTTP here) and on a wasm visor (over the
// in-process JS hooks) with no divergence.
//
// Scope: the LOCAL visor only (hv.visorCtx), matching the skychat
// password + proxy handlers. The group manager runs in the visor
// process (init_group.go); ctx.API resolves to it. Remote managed
// visors are out of scope here (a follow-up, same as skychatProxy).
package visor

import (
	"net/http"
	"strconv"
	"time"

	skychatgroup "github.com/skycoin/skywire/cmd/apps/skychat/group"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
)

// getGroups → GET /skychat/groups : list every group this visor knows.
func (hv *Hypervisor) getGroups() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		groups, err := ctx.API.GroupList()
		if err != nil {
			hv.writeGroupErr(w, r, err)
			return
		}
		if groups == nil {
			groups = []GroupInfo{}
		}
		httputil.WriteJSON(w, r, http.StatusOK, groups)
	})
}

// postGroupCreate → POST /skychat/groups {name, mode} : owner-create a room.
func (hv *Hypervisor) postGroupCreate() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var rb struct {
			Name string `json:"name"`
			Mode string `json:"mode"`
		}
		if err := httputil.ReadJSON(r, &rb); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "bad json: " + err.Error()})
			return
		}
		if rb.Name == "" {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "name required"})
			return
		}
		mode := skychatgroup.ModePublic
		if rb.Mode == "private" {
			mode = skychatgroup.ModePrivate
		}
		info, invite, err := ctx.API.GroupCreate(GroupCreateArgs{Name: rb.Name, Mode: mode})
		if err != nil {
			hv.writeGroupErr(w, r, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, struct {
			Info   GroupInfo `json:"info"`
			Invite string    `json:"invite"`
		}{Info: info, Invite: invite})
	})
}

// postGroupJoin → POST /skychat/groups/join {invite} : join via invite link.
func (hv *Hypervisor) postGroupJoin() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var rb struct {
			Invite string `json:"invite"`
		}
		if err := httputil.ReadJSON(r, &rb); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "bad json: " + err.Error()})
			return
		}
		if rb.Invite == "" {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "invite required"})
			return
		}
		info, err := ctx.API.GroupJoin(GroupJoinArgs{Invite: rb.Invite})
		if err != nil {
			hv.writeGroupErr(w, r, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, info)
	})
}

// postGroupSend → POST /skychat/groups/send {id, text} : publish a message.
func (hv *Hypervisor) postGroupSend() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var rb struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		}
		if err := httputil.ReadJSON(r, &rb); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "bad json: " + err.Error()})
			return
		}
		if rb.ID == "" || rb.Text == "" {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "id and text required"})
			return
		}
		if err := ctx.API.GroupSend(GroupSendArgs{ID: rb.ID, Text: rb.Text}); err != nil {
			hv.writeGroupErr(w, r, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, true)
	})
}

// getGroupMessages → GET /skychat/groups/messages?group_id=&since_ms= :
// snapshot the in-memory inbox for messages newer than since_ms (cursor
// poll — non-destructive), filtered to group_id when provided.
func (hv *Hypervisor) getGroupMessages() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var since time.Time
		if s := r.URL.Query().Get("since_ms"); s != "" {
			if ms, err := strconv.ParseInt(s, 10, 64); err == nil && ms > 0 {
				since = time.UnixMilli(ms)
			}
		}
		msgs, err := ctx.API.GroupPoll(since)
		if err != nil {
			hv.writeGroupErr(w, r, err)
			return
		}
		gid := r.URL.Query().Get("group_id")
		out := make([]GroupMessage, 0, len(msgs))
		for _, m := range msgs {
			if gid == "" || m.GroupID == gid {
				out = append(out, m)
			}
		}
		httputil.WriteJSON(w, r, http.StatusOK, out)
	})
}

// postGroupAddMember → POST /skychat/groups/add-member {id, pk} : owner/admin
// extends the allowlist.
func (hv *Hypervisor) postGroupAddMember() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var rb struct {
			ID string `json:"id"`
			PK string `json:"pk"`
		}
		if err := httputil.ReadJSON(r, &rb); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "bad json: " + err.Error()})
			return
		}
		var pk cipher.PubKey
		if err := pk.Set(rb.PK); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "bad pk: " + err.Error()})
			return
		}
		info, err := ctx.API.GroupAddMember(rb.ID, pk)
		if err != nil {
			hv.writeGroupErr(w, r, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, info)
	})
}

// getGroupJoinRequests → GET /skychat/groups/requests?group_id= : the
// admission queue for a group.
func (hv *Hypervisor) getGroupJoinRequests() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		gid := r.URL.Query().Get("group_id")
		if gid == "" {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "group_id required"})
			return
		}
		reqs, err := ctx.API.GroupJoinRequests(gid)
		if err != nil {
			hv.writeGroupErr(w, r, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, reqs)
	})
}

// groupPeerAction wraps the shared shape of every {id, pk} admin
// command: decode, parse the PK, call, render the updated info. One
// helper rather than seven near-identical handlers.
func (hv *Hypervisor) groupPeerAction(op func(*httpCtx, string, cipher.PubKey) (GroupInfo, error)) http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var rb struct {
			ID string `json:"id"`
			PK string `json:"pk"`
		}
		if err := httputil.ReadJSON(r, &rb); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "bad json: " + err.Error()})
			return
		}
		var pk cipher.PubKey
		if err := pk.Set(rb.PK); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "bad pk: " + err.Error()})
			return
		}
		info, err := op(ctx, rb.ID, pk)
		if err != nil {
			hv.writeGroupErr(w, r, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, info)
	})
}

// postGroupApproveJoin → POST /skychat/groups/requests/approve {id, pk}.
func (hv *Hypervisor) postGroupApproveJoin() http.HandlerFunc {
	return hv.groupPeerAction(func(ctx *httpCtx, id string, pk cipher.PubKey) (GroupInfo, error) {
		return ctx.API.GroupApproveJoin(id, pk)
	})
}

// postGroupDenyJoin → POST /skychat/groups/requests/deny {id, pk}.
func (hv *Hypervisor) postGroupDenyJoin() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var rb struct {
			ID string `json:"id"`
			PK string `json:"pk"`
		}
		if err := httputil.ReadJSON(r, &rb); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "bad json: " + err.Error()})
			return
		}
		var pk cipher.PubKey
		if err := pk.Set(rb.PK); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "bad pk: " + err.Error()})
			return
		}
		if err := ctx.API.GroupDenyJoin(rb.ID, pk); err != nil {
			hv.writeGroupErr(w, r, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, map[string]bool{"ok": true})
	})
}

// postGroupRemoveMember → POST /skychat/groups/remove-member {id, pk}.
func (hv *Hypervisor) postGroupRemoveMember() http.HandlerFunc {
	return hv.groupPeerAction(func(ctx *httpCtx, id string, pk cipher.PubKey) (GroupInfo, error) {
		return ctx.API.GroupRemoveMember(id, pk)
	})
}

// postGroupBanMember → POST /skychat/groups/ban {id, pk}.
func (hv *Hypervisor) postGroupBanMember() http.HandlerFunc {
	return hv.groupPeerAction(func(ctx *httpCtx, id string, pk cipher.PubKey) (GroupInfo, error) {
		return ctx.API.GroupBanMember(id, pk)
	})
}

// postGroupUnbanMember → POST /skychat/groups/unban {id, pk}.
func (hv *Hypervisor) postGroupUnbanMember() http.HandlerFunc {
	return hv.groupPeerAction(func(ctx *httpCtx, id string, pk cipher.PubKey) (GroupInfo, error) {
		return ctx.API.GroupUnbanMember(id, pk)
	})
}

// postGroupMuteMember → POST /skychat/groups/mute {id, pk}.
func (hv *Hypervisor) postGroupMuteMember() http.HandlerFunc {
	return hv.groupPeerAction(func(ctx *httpCtx, id string, pk cipher.PubKey) (GroupInfo, error) {
		return ctx.API.GroupMuteMember(id, pk)
	})
}

// postGroupUnmuteMember → POST /skychat/groups/unmute {id, pk}.
func (hv *Hypervisor) postGroupUnmuteMember() http.HandlerFunc {
	return hv.groupPeerAction(func(ctx *httpCtx, id string, pk cipher.PubKey) (GroupInfo, error) {
		return ctx.API.GroupUnmuteMember(id, pk)
	})
}

// postGroupReadOnly → POST /skychat/groups/read-only {id, read_only}.
func (hv *Hypervisor) postGroupReadOnly() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var rb struct {
			ID       string `json:"id"`
			ReadOnly bool   `json:"read_only"`
		}
		if err := httputil.ReadJSON(r, &rb); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "bad json: " + err.Error()})
			return
		}
		info, err := ctx.API.GroupSetReadOnly(rb.ID, rb.ReadOnly)
		if err != nil {
			hv.writeGroupErr(w, r, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, info)
	})
}

// getGroupInvite → GET /skychat/groups/invite?group_id= : mint a fresh invite.
func (hv *Hypervisor) getGroupInvite() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		gid := r.URL.Query().Get("group_id")
		if gid == "" {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "group_id required"})
			return
		}
		invite, err := ctx.API.GroupInvite(gid)
		if err != nil {
			hv.writeGroupErr(w, r, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, map[string]string{"invite": invite})
	})
}

// postGroupLeave → POST /skychat/groups/leave {id} : member leaves.
func (hv *Hypervisor) postGroupLeave() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var rb struct {
			ID string `json:"id"`
		}
		if err := httputil.ReadJSON(r, &rb); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "bad json: " + err.Error()})
			return
		}
		if err := ctx.API.GroupLeave(rb.ID); err != nil {
			hv.writeGroupErr(w, r, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, true)
	})
}

// postGroupDelete → POST /skychat/groups/delete {id} : owner tears down.
func (hv *Hypervisor) postGroupDelete() http.HandlerFunc {
	return hv.withCtx(hv.visorCtx, func(w http.ResponseWriter, r *http.Request, ctx *httpCtx) {
		var rb struct {
			ID string `json:"id"`
		}
		if err := httputil.ReadJSON(r, &rb); err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, map[string]string{"error": "bad json: " + err.Error()})
			return
		}
		if err := ctx.API.GroupDelete(rb.ID); err != nil {
			hv.writeGroupErr(w, r, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, true)
	})
}

// writeGroupErr maps the "grouping disabled" sentinel to 503 (so the UI can
// show "group chat unavailable on this visor") and everything else to 400.
func (hv *Hypervisor) writeGroupErr(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusBadRequest
	if err == ErrGroupingDisabled {
		status = http.StatusServiceUnavailable
	}
	httputil.WriteJSON(w, r, status, map[string]string{"error": err.Error()})
}
