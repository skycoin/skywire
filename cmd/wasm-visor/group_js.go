//go:build js && wasm

// Package main cmd/wasm-visor/group_js.go c3-vis-wasm
// cmd/wasm-visor/group_js.go: in-browser federated GROUP chat. The wasm visor
// runs the same group.Manager the native visor does (roster, signing, gossip,
// per-member CXO feeds), over its dmsg client, with an IN-MEMORY store + CXO tree
// (no filesystem in a tab). So a browser tab is a full federated group member: it
// publishes its own feed and subscribes to the other members' feeds over dmsg;
// the network retains whatever it published (local state resets on tab reload).
//
// JS surface on skywireVisor:
//
//	skychatGroupCreate(name[, "public"|"private"]) -> Promise<{id,name,invite}>
//	skychatGroupJoin(inviteLink)                   -> Promise<{id,name}>
//	skychatGroupSend(id, text)                     -> Promise<null>
//	skychatGroupAddMember(id, peerPkHex)           -> Promise<{id,members}>
//	skychatGroupList()                             -> JSON [{id,name,mode,role,members,status}]
//	skychatGroupMessages([id])                     -> JSON [{group_id,from,text,ts}] (newest last)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"syscall/js"
	"time"

	"github.com/skycoin/skywire/cmd/apps/skychat/group"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

const groupLogCap = 1000

var (
	groupMgr *group.Manager
	groupMu  sync.Mutex
	groupLog []groupMsg // in-memory ring of received group messages
)

// groupMsg is one group message surfaced to the page (skychatGroupMessages()).
type groupMsg struct {
	GroupID string `json:"group_id"`
	From    string `json:"from"` // sender PK hex
	Text    string `json:"text"`
	TS      int64  `json:"ts"` // unix milliseconds
}

func appendGroupMsg(m groupMsg) {
	groupMu.Lock()
	groupLog = append(groupLog, m)
	if len(groupLog) > groupLogCap {
		groupLog = groupLog[len(groupLog)-groupLogCap:]
	}
	groupMu.Unlock()
}

// startGroupChat brings up the in-browser group Manager over the wasm dmsg client
// with an in-memory store + CXO tree. Best-effort; called once from bootEdge after
// dmsg + the 1:1 skychat app are up. Any failure just leaves group chat disabled.
func startGroupChat(sk cipher.SecKey, log *logging.Logger) {
	if dmsgC == nil || selfPK.Null() {
		return
	}
	store, err := group.OpenStore("") // js build -> in-memory (path ignored)
	if err != nil {
		vlog("group: store open failed: " + err.Error())
		return
	}
	mgr, err := group.NewManager(group.ManagerConfig{
		Store:             store,
		DmsgC:             dmsgC,
		MyPK:              selfPK,
		MySK:              sk,
		InMemoryDB:        true, // no filesystem in a tab; CXO tree lives in memory
		Logger:            log,
		HeartbeatInterval: group.DefaultHeartbeatInterval,
	})
	if err != nil {
		vlog("group: NewManager failed: " + err.Error())
		return
	}
	mgr.SetMessageHandler(func(groupID string, sender cipher.PubKey, msg group.Message) {
		appendGroupMsg(groupMsg{GroupID: groupID, From: sender.Hex(), Text: msg.Text, TS: msg.TS.UnixMilli()})
		vlog(fmt.Sprintf("group: message in %s from %s: %q", shortID(groupID), shortPK(sender.Hex()), msg.Text))
	})
	groupMgr = mgr
	if err := mgr.Resume(); err != nil {
		vlog("group: Resume: " + err.Error())
	}
	vlog("group: chat manager up — in-memory federated groups over dmsg")
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// groupView is the compact record shape returned by skychatGroupList().
type groupView struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Mode    string `json:"mode"`
	Role    string `json:"role"`
	Members int    `json:"members"`
	Status  string `json:"status"`
}

// jsGroupCreate(name[, mode]) → Promise<{id,name,invite}>. mode is "public"
// (default) or "private".
func jsGroupCreate(_ js.Value, args []js.Value) interface{} {
	if groupMgr == nil {
		return errPromise("group chat not ready")
	}
	if len(args) < 1 || args[0].String() == "" {
		return errPromise("skychatGroupCreate(name[, mode])")
	}
	name := args[0].String()
	mode := group.ModePublic
	if len(args) >= 2 && args[1].String() == "private" {
		mode = group.ModePrivate
	}
	return promise(func() (interface{}, error) {
		rec, err := groupMgr.Create(name, mode, nil)
		if err != nil {
			return nil, err
		}
		invite, err := groupMgr.BuildInvite(rec.ID)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"id": rec.ID, "name": rec.Name, "invite": invite}, nil
	})
}

// jsGroupJoin(inviteLink) → Promise<{id,name}>.
func jsGroupJoin(_ js.Value, args []js.Value) interface{} {
	if groupMgr == nil {
		return errPromise("group chat not ready")
	}
	if len(args) < 1 || args[0].String() == "" {
		return errPromise("skychatGroupJoin(inviteLink)")
	}
	link := args[0].String()
	return promise(func() (interface{}, error) {
		inv, err := group.DecodeInvite(link)
		if err != nil {
			return nil, fmt.Errorf("bad invite: %w", err)
		}
		rec, err := groupMgr.Join(inv)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"id": rec.ID, "name": rec.Name}, nil
	})
}

// jsGroupSend(id, text) → Promise<null>.
func jsGroupSend(_ js.Value, args []js.Value) interface{} {
	if groupMgr == nil {
		return errPromise("group chat not ready")
	}
	if len(args) < 2 {
		return errPromise("skychatGroupSend(id, text)")
	}
	id, text := args[0].String(), args[1].String()
	return promise(func() (interface{}, error) {
		sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		return nil, groupMgr.SendToGroup(sctx, id, text)
	})
}

// jsGroupAddMember(id, peerPkHex) → Promise<{id,members}> (owner extends the allowlist).
func jsGroupAddMember(_ js.Value, args []js.Value) interface{} {
	if groupMgr == nil {
		return errPromise("group chat not ready")
	}
	if len(args) < 2 {
		return errPromise("skychatGroupAddMember(id, peerPkHex)")
	}
	id, pkHex := args[0].String(), args[1].String()
	return promise(func() (interface{}, error) {
		var pk cipher.PubKey
		if err := pk.Set(pkHex); err != nil {
			return nil, fmt.Errorf("bad member pk: %w", err)
		}
		rec, err := groupMgr.AddMember(id, pk)
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"id": rec.ID, "members": len(rec.Members)}, nil
	})
}

// jsGroupList() → JSON [{id,name,mode,role,members,status}].
func jsGroupList(js.Value, []js.Value) interface{} {
	if groupMgr == nil {
		return "[]"
	}
	recs, err := groupMgr.List()
	if err != nil {
		return "[]"
	}
	out := make([]groupView, 0, len(recs))
	for _, r := range recs {
		out = append(out, groupView{
			ID: r.ID, Name: r.Name, Mode: string(r.Mode),
			Role: string(r.Role), Members: len(r.Members), Status: string(r.Status),
		})
	}
	b, _ := json.Marshal(out) //nolint:errcheck
	return string(b)
}

// jsGroupMessages([id]) → JSON of buffered group messages (newest last),
// optionally filtered to one group id.
func jsGroupMessages(_ js.Value, args []js.Value) interface{} {
	filter := ""
	if len(args) >= 1 && args[0].Type() == js.TypeString {
		filter = args[0].String()
	}
	groupMu.Lock()
	var view []groupMsg
	if filter == "" {
		view = append(view, groupLog...)
	} else {
		for _, m := range groupLog {
			if m.GroupID == filter {
				view = append(view, m)
			}
		}
	}
	groupMu.Unlock()
	b, _ := json.Marshal(view) //nolint:errcheck
	return string(b)
}

// errPromise returns an already-rejected Promise with the given message, so the
// group hooks fail gracefully (reject) rather than throwing synchronously.
func errPromise(msg string) interface{} {
	return promise(func() (interface{}, error) { return nil, fmt.Errorf("%s", msg) })
}
