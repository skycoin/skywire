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
//	skychatGroupLeave(id)                          -> Promise<null>   (unsubscribe, keep history)
//	skychatGroupDelete(id)                         -> Promise<null>   (leave + drop the record)
//	skychatGroupInvite(id)                         -> Promise<string> (re-share invite link)
//	skychatGroupList()                             -> JSON [{id,name,mode,role,members,status}]
//	skychatGroupMessages([id])                     -> JSON [{group_id,from,text,ts}] (newest last)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"syscall/js"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skychat/group"
)

const groupLogCap = 1000

// groupSeenCap bounds the dedup set. Larger than groupLogCap so the dedup
// window outlives the display ring; on overflow we clear it wholesale (a
// browser tab can tolerate the rare re-duplication that implies).
const groupSeenCap = 8192

var (
	groupMgr  *group.Manager
	groupMu   sync.Mutex
	groupLog  []groupMsg          // in-memory ring of received group messages
	groupSeen = map[string]bool{} // dedup: GroupID|senderHex|ts-nanos (replay vs. live)
)

// groupMsg is one group message surfaced to the page (skychatGroupMessages()).
type groupMsg struct {
	GroupID string `json:"group_id"`
	From    string `json:"from"` // sender PK hex
	Text    string `json:"text"`
	TS      int64  `json:"ts"` // unix milliseconds (display)
	// TSNano is the message's exact UnixNano as a STRING — JS numbers are
	// float64 and lose precision past 2^53, but a nanosecond ts is ~1.7e18.
	// skychatGroupUnsend(id, tsNano) round-trips this to delete-for-everyone.
	TSNano string `json:"ts_nano"`
}

// appendGroupMsg records a group message, deduped by (group, sender, ns-ts) so
// a ReplayHistory pump can't double-list messages already delivered live (or by
// an earlier replay). Returns true if the message was new. key is derived from
// nanosecond ts so it survives the millisecond truncation in groupMsg.TS.
func appendGroupMsg(m groupMsg, key string) bool {
	groupMu.Lock()
	defer groupMu.Unlock()
	if key != "" {
		if groupSeen[key] {
			return false
		}
		if len(groupSeen) >= groupSeenCap {
			groupSeen = make(map[string]bool, groupSeenCap)
		}
		groupSeen[key] = true
	}
	groupLog = append(groupLog, m)
	if len(groupLog) > groupLogCap {
		groupLog = groupLog[len(groupLog)-groupLogCap:]
	}
	return true
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
		key := groupDedupKey(groupID, sender.Hex(), msg.TS.UnixNano())
		if appendGroupMsg(groupMsg{GroupID: groupID, From: sender.Hex(), Text: msg.Text, TS: msg.TS.UnixMilli(), TSNano: strconv.FormatInt(msg.TS.UnixNano(), 10)}, key) {
			vlog(fmt.Sprintf("group: message in %s from %s: %q", shortID(groupID), shortPK(sender.Hex()), msg.Text))
		}
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

// groupDedupKey identifies a group message by (group, sender, nanosecond ts):
// a sender does not publish two leaves at the same nanosecond, so this is a
// stable identity across the replay and live delivery paths.
func groupDedupKey(groupID, senderHex string, tsNano int64) string {
	return groupID + "|" + senderHex + "|" + strconv.FormatInt(tsNano, 10)
}

// backfillGroupHistory re-pumps a group's history through the message handler
// after a Join, best-effort. A Join subscribes to peers' CXO feeds but their
// historical leaves sync asynchronously, so we replay a few times on a short
// backoff — dedup makes the repeats idempotent — to catch history that lands
// after the initial subscribe returns. The page can also call skychatGroupReplay
// explicitly (e.g. a "load history" action).
func backfillGroupHistory(id string) {
	go func() {
		for _, d := range []time.Duration{2 * time.Second, 6 * time.Second, 15 * time.Second} {
			t := time.NewTimer(d)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
			if groupMgr != nil {
				groupMgr.ReplayHistory(id)
			}
		}
	}()
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
		// Peer CXO feeds sync asynchronously after subscribe; pull their
		// history into the buffer over the next few seconds.
		backfillGroupHistory(rec.ID)
		return map[string]interface{}{"id": rec.ID, "name": rec.Name}, nil
	})
}

// jsGroupReplay(id) → Promise<null>. Explicitly re-pumps a group's history
// through the handler (deduped), for a UI "load history" action or to backfill
// after a slow feed sync. Parity with the native app, where Resume replays at
// startup.
func jsGroupReplay(_ js.Value, args []js.Value) interface{} {
	if groupMgr == nil {
		return errPromise("group chat not ready")
	}
	if len(args) < 1 || args[0].String() == "" {
		return errPromise("skychatGroupReplay(id)")
	}
	id := args[0].String()
	return promise(func() (interface{}, error) {
		groupMgr.ReplayHistory(id)
		return nil, nil
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

// jsGroupLeave(id) → Promise<null>. Unsubscribes from the group's CXO feed and
// marks it StatusLeft, but keeps the record (history stays visible). Mirrors the
// native app's Leave. The shared Manager owns the CXO teardown + roster update.
func jsGroupLeave(_ js.Value, args []js.Value) interface{} {
	if groupMgr == nil {
		return errPromise("group chat not ready")
	}
	if len(args) < 1 {
		return errPromise("skychatGroupLeave(id)")
	}
	id := args[0].String()
	return promise(func() (interface{}, error) { return nil, groupMgr.Leave(id) })
}

// jsGroupDelete(id) → Promise<null>. Leaves the group AND removes it from the
// store (record + local history gone). Mirrors the native app's Delete.
func jsGroupDelete(_ js.Value, args []js.Value) interface{} {
	if groupMgr == nil {
		return errPromise("group chat not ready")
	}
	if len(args) < 1 {
		return errPromise("skychatGroupDelete(id)")
	}
	id := args[0].String()
	return promise(func() (interface{}, error) { return nil, groupMgr.Delete(id) })
}

// jsGroupInvite(id) → Promise<string>. Regenerates the shareable invite link for
// an EXISTING group (Create already returns one on first creation; this lets the
// UI re-fetch/re-share it later). Owner/admin only, enforced by the Manager.
func jsGroupInvite(_ js.Value, args []js.Value) interface{} {
	if groupMgr == nil {
		return errPromise("group chat not ready")
	}
	if len(args) < 1 {
		return errPromise("skychatGroupInvite(id)")
	}
	id := args[0].String()
	return promise(func() (interface{}, error) { return groupMgr.BuildInvite(id) })
}

// jsGroupUnsend(id, tsNano) → Promise<null>. Delete-for-everyone: the shared
// Manager publishes a signed tombstone for the message at that exact UnixNano.
// tsNano is a STRING (the ts_nano field from skychatGroupMessages) to avoid the
// JS float64 precision loss. Owner/sender authorization is enforced by Manager.
func jsGroupUnsend(_ js.Value, args []js.Value) interface{} {
	if groupMgr == nil {
		return errPromise("group chat not ready")
	}
	if len(args) < 2 {
		return errPromise("skychatGroupUnsend(id, tsNano)")
	}
	id := args[0].String()
	ts, err := strconv.ParseInt(args[1].String(), 10, 64)
	if err != nil {
		return errPromise("skychatGroupUnsend: tsNano must be a numeric string")
	}
	return promise(func() (interface{}, error) { return nil, groupMgr.Unsend(id, ts) })
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
