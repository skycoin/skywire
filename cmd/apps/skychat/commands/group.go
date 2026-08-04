// Package commands cmd/apps/skychat/commands/group.go c4-app-chat
//
// Browser-facing group chat: HTTP endpoints that proxy the visor's GroupX
// RPC (the same net/rpc surface the pair endpoints + /status use via
// pairRPCCall) plus an SSE bridge that drains visor.GroupPoll onto the chat
// stream. This is the group analog of pairing.go — the browser UI can only
// reach the app's HTTP surface, so every group operation is relayed to the
// visor here.
//
// Gated behind --pair-enable: groups need the visor RPC connection, which is
// only dialed when pairing is on. When it's off these are no-ops and the UI
// falls back to a group-less view (the /group endpoints aren't registered).
package commands

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skychat/address"
	"github.com/skycoin/skywire/pkg/skychat/group"
	"github.com/skycoin/skywire/pkg/visor"
)

// groupFileMeta is the file reference published on a group feed (as the message
// text) so every member — including future joiners — sees the file and can pull
// its bytes on demand via the file-backfill request. The bytes never ride the
// CXO feed; only this small reference does.
type groupFileMeta struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type groupFileEnvelope struct {
	File *groupFileMeta `json:"skychat_file"`
}

// encodeGroupFileText renders a file reference as the group message body.
func encodeGroupFileText(m groupFileMeta) (string, error) {
	b, err := json.Marshal(groupFileEnvelope{File: &m})
	return string(b), err
}

// parseGroupFileText detects a file-reference envelope in a group message body.
// A cheap prefix + substring gate avoids running the JSON parser on ordinary
// chat text.
func parseGroupFileText(text string) (groupFileMeta, bool) {
	t := strings.TrimSpace(text)
	if len(t) == 0 || t[0] != '{' || !strings.Contains(t, "skychat_file") {
		return groupFileMeta{}, false
	}
	var env groupFileEnvelope
	if err := json.Unmarshal([]byte(t), &env); err != nil || env.File == nil || env.File.ID == "" {
		return groupFileMeta{}, false
	}
	return *env.File, true
}

// enrichGroupFileRow adds the file_* fields (+ a /files/ URL when the bytes are
// already held locally) to a group-message map for a file-reference message.
// Shared by the SSE poller and the /group/<id>/history response.
func enrichGroupFileRow(row map[string]any, meta groupFileMeta) {
	row["file_id"] = meta.ID
	row["file_name"] = meta.Name
	row["file_size"] = meta.Size
	if p, ok := findFileByID(meta.ID, meta.Name); ok {
		row["file_url"] = "/files/" + filepath.Base(p)
	}
}

// groupDeleteMeta is a delete tombstone published on the group feed to hide a
// message for everyone. It references the target by its exact UnixNano
// timestamp — the same value GroupUnsend prunes by, and the identity carried in
// ts_nano on each delivered message. The tombstone's own feed leaf is signed by
// the deleter, so its authenticated sender IS the only account whose messages it
// can hide (readers accept a tombstone only against a message by the same
// author) — you can delete only your own messages for everyone.
type groupDeleteMeta struct {
	ToTSNano int64 `json:"to_ts_nano"`
}

type groupDeleteEnvelope struct {
	Delete *groupDeleteMeta `json:"skychat_delete"`
}

func encodeGroupDeleteText(m groupDeleteMeta) (string, error) {
	b, err := json.Marshal(groupDeleteEnvelope{Delete: &m})
	return string(b), err
}

// parseGroupDeleteText detects a delete tombstone in a group message body.
func parseGroupDeleteText(text string) (groupDeleteMeta, bool) {
	t := strings.TrimSpace(text)
	if len(t) == 0 || t[0] != '{' || !strings.Contains(t, "skychat_delete") {
		return groupDeleteMeta{}, false
	}
	var env groupDeleteEnvelope
	if err := json.Unmarshal([]byte(t), &env); err != nil || env.Delete == nil || env.Delete.ToTSNano == 0 {
		return groupDeleteMeta{}, false
	}
	return *env.Delete, true
}

// sendFileToVisorGroup publishes a file to a visor-managed group: it keeps a
// served copy (id-named, so re-requests can find it) and publishes a file
// reference on the group feed. The bytes are NOT fanned out — every member
// pulls them on demand via the file-backfill request, which reaches even
// members who were offline at send time and future joiners. Returns the file
// id + the sender-side /files/ URL for an optimistic UI render.
func sendFileToVisorGroup(_ context.Context, groupID, path, name string) (string, string, error) {
	fileID := newEventID()
	if name == "" {
		name = filepath.Base(path)
	}
	fi, err := os.Stat(path) //nolint
	if err != nil {
		return "", "", err
	}
	// The served copy IS the copy every member will eventually hold: the
	// backfill path re-sends these bytes verbatim. So it is sealed here,
	// once, under a key derived for this file — and the plaintext never
	// reaches the downloads dir on any member's disk. A failure is fatal to
	// the send rather than a fallback: publishing the reference and then
	// serving the file in the clear is exactly the gap being closed.
	if err := storeGroupAttachment(path, groupID, fileID, name); err != nil {
		return "", "", err
	}
	text, err := encodeGroupFileText(groupFileMeta{ID: fileID, Name: name, Size: fi.Size()})
	if err != nil {
		return "", "", err
	}
	if err := pairRPCCall("GroupSend", func(c visor.API) error {
		return c.GroupSend(visor.GroupSendArgs{ID: groupID, Text: text})
	}); err != nil {
		return "", "", err
	}
	return fileID, "/files/" + fileID + strings.ToLower(filepath.Ext(name)), nil
}

// groupPollerCancel stops the SSE-bridge goroutine on shutdown.
var groupPollerCancel context.CancelFunc

// startGroupPoller bridges visor.GroupPoll into the SSE pipeline, mirroring
// startPairPoller. Each inbound group message is broadcast as a legacy /sse
// envelope tagged channel="group" + group_id, and recorded on the structured
// /events "group" channel. No-op when pairing (and thus the visor RPC) is off.
func startGroupPoller(parent context.Context) {
	if !pairEnable {
		return
	}
	ctx, cancel := context.WithCancel(parent) //nolint:gosec
	groupPollerCancel = cancel

	go func() {
		var since time.Time
		// pendingSeen tracks the last observed pending-request count per
		// group so a rising edge — someone new asked to join — can be
		// surfaced once, rather than re-notifying on every poll for a
		// request that is simply still waiting.
		pendingSeen := map[string]int{}
		var sinceJoinScan int
		ticker := time.NewTicker(pairPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			// The approval queue changes on human timescales, so scan it
			// far less often than messages. GroupList is a store walk on
			// the visor side; running it at message cadence would be
			// pure overhead for a signal that can afford to be seconds late.
			if sinceJoinScan++; sinceJoinScan >= joinScanEveryNPolls {
				sinceJoinScan = 0
				scanPendingJoins(pendingSeen)
			}
			var msgs []visor.GroupMessage
			err := pairRPCCall("GroupPoll", func(c visor.API) error {
				out, e := c.GroupPoll(since)
				msgs = out
				return e
			})
			if err != nil {
				if !errors.Is(err, errPairRPCUnavailable) {
					appLog("Group: GroupPoll error: %v", err)
				}
				continue
			}
			for _, m := range msgs {
				if m.TS.After(since) {
					since = m.TS
				}
				// A delete tombstone hides a message for everyone. It's authored
				// (signed) by the deleter, so deleted_by is authentic — a reader
				// applies it only to a message by that same author. Surfaced as a
				// control event, never rendered as a chat line.
				if dmeta, ok := parseGroupDeleteText(m.Text); ok {
					if body, e := json.Marshal(map[string]any{
						"channel":         "group-delete",
						"group_id":        m.GroupID,
						"deleted_by":      m.SenderPK.Hex(),
						"deleted_ts_nano": dmeta.ToTSNano,
					}); e == nil {
						hub.broadcast(string(body))
					}
					continue
				}
				envelope := map[string]any{
					"sender":   m.SenderPK.Hex(),
					"message":  m.Text,
					"channel":  "group",
					"group_id": m.GroupID,
					"ts":       m.TS.Format(time.RFC3339Nano),
					"ts_nano":  m.TS.UnixNano(),
				}
				if meta, ok := parseGroupFileText(m.Text); ok {
					envelope["message"] = "📎 " + meta.Name
					enrichGroupFileRow(envelope, meta)
				} else if rmeta, ok := parseReplyText(m.Text); ok {
					envelope["message"] = rmeta.Text
					enrichReplyRow(envelope, rmeta)
				} else if fmeta, ok := parseForwardText(m.Text); ok {
					envelope["message"] = fmeta.Text
					enrichForwardRow(envelope, fmeta)
				}
				body, mErr := json.Marshal(envelope)
				if mErr != nil {
					appLog("Group: marshal SSE message: %v", mErr)
					continue
				}
				hub.broadcast(string(body))
				hub.recordEvent(chatEvent{
					ID:        newEventID(),
					Channel:   channelGroup,
					Transport: "cxo",
					Dir:       "in",
					From:      m.SenderPK.Hex(),
					GroupID:   m.GroupID,
					Text:      m.Text,
				})
				// Host-OS notification when no capable browser UI is showing it.
				// Body = "<sender>: <text>" using the display text (file/reply
				// overrides applied above).
				msgText, _ := envelope["message"].(string)
				notifyOSInbound("Group message", shortHexPK(m.SenderPK.Hex())+": "+notifPreview(msgText))
			}
		}
	}()
}

// joinScanEveryNPolls is how many message-poll ticks pass between
// approval-queue scans. At the default pair-poll cadence this lands
// around a few seconds, which is far below the latency a human
// approving a request would notice.
const joinScanEveryNPolls = 10

// scanPendingJoins looks for newly-arrived join requests and surfaces
// them as an SSE control event plus a host-OS notification.
//
// Rising-edge only: seen holds the previous count per group, and a
// notification fires only when the count goes up. A request that stays
// queued because nobody has acted on it must not re-notify every scan —
// that would train admins to ignore the signal.
//
// A group that disappears from the list is dropped from seen so a
// rejoin later starts clean rather than comparing against a stale count.
func scanPendingJoins(seen map[string]int) {
	var groups []visor.GroupInfo
	if err := pairRPCCall("GroupList", func(c visor.API) error {
		out, e := c.GroupList()
		groups = out
		return e
	}); err != nil {
		return
	}
	live := make(map[string]bool, len(groups))
	for _, g := range groups {
		live[g.ID] = true
		prev, had := seen[g.ID]
		seen[g.ID] = g.PendingJoins
		if g.PendingJoins == 0 || g.PendingJoins <= prev {
			continue
		}
		// First observation of an existing queue (app just started) is
		// still worth surfacing to the UI, but not worth a desktop
		// notification — nothing actually happened just now.
		if body, err := json.Marshal(map[string]any{
			"channel":       "group-join-request",
			"group_id":      g.ID,
			"group_name":    g.Name,
			"pending_joins": g.PendingJoins,
		}); err == nil {
			hub.broadcast(string(body))
		}
		if had {
			notifyOSInbound("Group join request",
				strconv.Itoa(g.PendingJoins)+" waiting to join "+g.Name)
		}
	}
	for id := range seen {
		if !live[id] {
			delete(seen, id)
		}
	}
}

// stopGroupPoller cancels the poller goroutine. Idempotent.
func stopGroupPoller() {
	if groupPollerCancel != nil {
		groupPollerCancel()
		groupPollerCancel = nil
	}
}

// registerGroupHTTPHandlers wires the /group endpoints onto mux. No-op when
// --pair-enable is off. Pattern precedence: net/http picks the longest match,
// so "/group/join" shadows "/group/" and "/group" (exact) is the list/create
// root.
func registerGroupHTTPHandlers(mux *http.ServeMux) {
	if !pairEnable {
		return
	}
	mux.HandleFunc("/group", requireAuthFunc(groupRootHandler()))
	mux.HandleFunc("/group/join", requireAuthFunc(groupJoinHandler()))
	// Registered before the catch-all /group/ so it isn't swallowed by
	// groupItemHandler, which reads the next path segment as a group ID.
	mux.HandleFunc("/group/resolve", requireAuthFunc(groupResolveHandler()))
	mux.HandleFunc("/group/catalog", requireAuthFunc(groupCatalogHandler()))
	mux.HandleFunc("/group/", requireAuthFunc(groupItemHandler()))
}

// groupRootHandler serves GET /group (list) and POST /group (create).
func groupRootHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !pairRPCAlive() {
			http.Error(w, "groups disabled (visor RPC unavailable)", http.StatusServiceUnavailable)
			return
		}
		switch r.Method {
		case http.MethodGet:
			var groups []visor.GroupInfo
			if err := pairRPCCall("GroupList", func(c visor.API) error {
				out, e := c.GroupList()
				groups = out
				return e
			}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			// Hide groups the user has left or the owner revoked. GroupList
			// deliberately keeps terminal records (Leave/Delete only flip the
			// status, they don't purge — the CLI can still audit them), but for
			// the browser a deleted/left group must disappear, not reappear on
			// the next sync.
			active := make([]visor.GroupInfo, 0, len(groups))
			for _, g := range groups {
				if g.Status == group.StatusLeft || g.Status == group.StatusRevoked {
					continue
				}
				active = append(active, g)
			}
			writeJSON(w, active)

		case http.MethodPost:
			var body struct {
				Name string `json:"name"`
				// Kind is the group type. "mode" is still read as a
				// fallback so an older cached UI build keeps working —
				// it carried the same two values.
				Kind    string   `json:"kind"`
				Mode    string   `json:"mode"`
				Members []string `json:"members"`
				// PeerBackfill is the creator's choice on whether any online
				// member may serve this group's history to a joiner. Sent as
				// a pointer so an older cached UI build — which sends no such
				// field — still gets the default (enabled) rather than
				// silently creating admins-only groups.
				PeerBackfill *bool `json:"peer_backfill"`
				// Listed opts the group into this visor's discovery
				// catalog. Absent means unlisted — see Record.Listed for
				// why the default has to be the private one.
				Listed bool `json:"listed"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
				return
			}
			if strings.TrimSpace(body.Name) == "" {
				http.Error(w, "name required", http.StatusBadRequest)
				return
			}
			raw := body.Kind
			if raw == "" {
				raw = body.Mode
			}
			kind := group.Kind(raw)
			if raw == "" {
				kind = group.KindPublic
			}
			if !kind.IsValid() {
				http.Error(w, "invalid kind (use public, private or channel)", http.StatusBadRequest)
				return
			}
			members, err := parsePKList(body.Members)
			if err != nil {
				http.Error(w, "invalid member pk: "+err.Error(), http.StatusBadRequest)
				return
			}
			var info visor.GroupInfo
			var link string
			if err := pairRPCCall("GroupCreate", func(c visor.API) error {
				i, l, e := c.GroupCreate(visor.GroupCreateArgs{
					Name: body.Name, Kind: kind, InitialMembers: members,
					DisablePeerBackfill: body.PeerBackfill != nil && !*body.PeerBackfill,
					Listed:              body.Listed,
				})
				info, link = i, l
				return e
			}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, map[string]any{"info": info, "invite": link})

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// groupJoinHandler serves POST /group/join {invite} or {address}.
//
// Both spellings are accepted because they are not interchangeable: an
// invite link is self-contained and works while the group's host is
// offline, and a skychat://<pk>/<group-id> address is short enough to
// scan from a QR code but has to ask the host what the group is first.
func groupJoinHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !pairRPCAlive() {
			http.Error(w, "groups disabled (visor RPC unavailable)", http.StatusServiceUnavailable)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Invite  string `json:"invite"`
			Address string `json:"address"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
			return
		}
		invite := strings.TrimSpace(body.Invite)
		addr := strings.TrimSpace(body.Address)
		if invite == "" && addr == "" {
			http.Error(w, "invite or address required", http.StatusBadRequest)
			return
		}
		// A single UI field takes either, so route by shape rather than
		// trusting which key it arrived under.
		if invite != "" && !address.IsInvite(invite) {
			invite, addr = "", invite
		}
		var info visor.GroupInfo
		if err := pairRPCCall("GroupJoin", func(c visor.API) error {
			i, e := c.GroupJoin(visor.GroupJoinArgs{Invite: invite, Address: addr})
			info = i
			return e
		}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, info)
	}
}

// groupCatalogHandler serves GET /group/catalog?pk=… — "what does this
// visor publish?". An absent or empty pk asks this visor about itself, so
// an operator can see their own listing exactly as others would.
//
// Read-only and answers only what the host chose to publish; a visor that
// has listed nothing returns an empty list rather than an error, because
// "nothing here" is a legitimate answer and not a failure.
func groupCatalogHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !pairRPCAlive() {
			http.Error(w, "groups disabled (visor RPC unavailable)", http.StatusServiceUnavailable)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var host cipher.PubKey
		if raw := strings.TrimSpace(r.URL.Query().Get("pk")); raw != "" {
			// Accept a full skychat:// address as well as a bare key: the UI
			// hands over whatever the user typed.
			if addr, err := address.Parse(raw); err == nil {
				host = addr.PK
			} else if err := host.Set(raw); err != nil {
				http.Error(w, "invalid pk: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		var (
			entries   []visor.GroupCatalogEntry
			truncated bool
		)
		if err := pairRPCCall("GroupCatalog", func(c visor.API) error {
			e, t, cerr := c.GroupCatalog(host)
			entries, truncated = e, t
			return cerr
		}); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if entries == nil {
			entries = []visor.GroupCatalogEntry{}
		}
		writeJSON(w, map[string]any{"entries": entries, "truncated": truncated})
	}
}

// groupResolveHandler serves GET|POST /group/resolve?address=… — "what is
// this thing I just pasted or scanned".
//
// GET as well as POST because the UI calls this as the user types, and a
// GET keeps that a plainly cacheless read with the address in the query
// rather than a body. Nothing is mutated either way.
//
// A resolve failure is reported as 404 rather than 500: the overwhelmingly
// common causes are a mistyped key and a group the host does not hold,
// which are "not found", not "the visor broke". A UI showing a red error
// bar for a half-typed key would be wrong every time.
func groupResolveHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !pairRPCAlive() {
			http.Error(w, "groups disabled (visor RPC unavailable)", http.StatusServiceUnavailable)
			return
		}
		var raw string
		switch r.Method {
		case http.MethodGet:
			raw = r.URL.Query().Get("address")
		case http.MethodPost:
			var body struct {
				Address string `json:"address"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
				return
			}
			raw = body.Address
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if strings.TrimSpace(raw) == "" {
			http.Error(w, "address required", http.StatusBadRequest)
			return
		}
		var res visor.GroupResolveResult
		if err := pairRPCCall("GroupResolve", func(c visor.API) error {
			out, e := c.GroupResolve(visor.GroupResolveArgs{Address: raw})
			res = out
			return e
		}); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, res)
	}
}

// groupItemHandler serves the per-group subtree:
//
//	GET    /group/<id>          → info
//	DELETE /group/<id>          → delete (owner)
//	GET    /group/<id>/invite   → invite link
//	POST   /group/<id>/send     → publish a message
//	POST   /group/<id>/listed   → publish in the discovery catalog
//	POST   /group/<id>/leave    → leave (member)
//	GET    /group/<id>/history  → persisted history (?limit=N)
func groupItemHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !pairRPCAlive() {
			http.Error(w, "groups disabled (visor RPC unavailable)", http.StatusServiceUnavailable)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, "/group/")
		segs := strings.SplitN(rest, "/", 2)
		id := segs[0]
		if id == "" {
			http.Error(w, "missing group id", http.StatusBadRequest)
			return
		}
		action := ""
		if len(segs) == 2 {
			action = segs[1]
		}

		switch {
		case action == "" && r.Method == http.MethodGet:
			var info visor.GroupInfo
			if err := pairRPCCall("GroupGet", func(c visor.API) error {
				i, e := c.GroupGet(id)
				info = i
				return e
			}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, info)

		case action == "" && r.Method == http.MethodDelete:
			if err := pairRPCCall("GroupDelete", func(c visor.API) error { return c.GroupDelete(id) }); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)

		case action == "ask-again" && r.Method == http.MethodPost:
			// Deliberate retry after an admin declined our join (the UI's
			// "ask again" button). Everything is derived from the stored
			// record, so no invite re-paste; it pays the same PoW +
			// rate-limit gates as a first ask.
			var info visor.GroupInfo
			if err := pairRPCCall("GroupAskAgain", func(c visor.API) error {
				i, e := c.GroupAskAgain(id)
				info = i
				return e
			}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, info)

		case action == "invite" && r.Method == http.MethodGet:
			var link string
			if err := pairRPCCall("GroupInvite", func(c visor.API) error {
				l, e := c.GroupInvite(id)
				link = l
				return e
			}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, map[string]string{"invite": link})

		case action == "send" && r.Method == http.MethodPost:
			var body struct {
				Text string `json:"text"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
				return
			}
			if strings.TrimSpace(body.Text) == "" {
				http.Error(w, "text required", http.StatusBadRequest)
				return
			}
			if err := pairRPCCall("GroupSend", func(c visor.API) error {
				return c.GroupSend(visor.GroupSendArgs{ID: id, Text: body.Text})
			}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)

		case action == "message" && r.Method == http.MethodDelete:
			// "Delete for everyone": publish a durable tombstone (so live
			// members, offline-then-back members, and future joiners all hide
			// it) AND prune our own leaf so the bytes are erased. Both GroupSend
			// and GroupUnsend are sender-scoped, so this only affects messages we
			// authored. The tombstone is the backstop if the prune can't run.
			tsNano, perr := strconv.ParseInt(r.URL.Query().Get("ts"), 10, 64)
			if perr != nil || tsNano == 0 {
				http.Error(w, "ts (unixnano) required", http.StatusBadRequest)
				return
			}
			tomb, terr := encodeGroupDeleteText(groupDeleteMeta{ToTSNano: tsNano})
			if terr != nil {
				http.Error(w, terr.Error(), http.StatusInternalServerError)
				return
			}
			if err := pairRPCCall("GroupSend", func(c visor.API) error {
				return c.GroupSend(visor.GroupSendArgs{ID: id, Text: tomb})
			}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			// Best-effort byte erasure — a failure here is non-fatal; the
			// tombstone already hides the message everywhere.
			if err := pairRPCCall("GroupUnsend", func(c visor.API) error {
				return c.GroupUnsend(visor.GroupUnsendArgs{ID: id, TS: tsNano})
			}); err != nil {
				appLog("Group: unsend prune failed (tombstone still applies): %v", err)
			}
			w.WriteHeader(http.StatusNoContent)

		case action == "requests" && r.Method == http.MethodGet:
			var reqs []visor.GroupJoinRequest
			if err := pairRPCCall("GroupJoinRequests", func(c visor.API) error {
				out, e := c.GroupJoinRequests(id)
				reqs = out
				return e
			}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			// Only the actionable set by default: an admin's queue should
			// open on "what needs me", not on an audit log. ?all=1 returns
			// decided entries too.
			if r.URL.Query().Get("all") != "1" {
				pending := make([]visor.GroupJoinRequest, 0, len(reqs))
				for _, q := range reqs {
					if q.Status == group.JoinStatusPending {
						pending = append(pending, q)
					}
				}
				reqs = pending
			}
			writeJSON(w, reqs)

		case r.Method == http.MethodPost && groupPeerActions[action] != nil:
			var body struct {
				PK string `json:"pk"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
				return
			}
			var pk cipher.PubKey
			if err := pk.Set(strings.TrimSpace(body.PK)); err != nil {
				http.Error(w, "invalid pk: "+err.Error(), http.StatusBadRequest)
				return
			}
			info, err := groupPeerActions[action](id, pk)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, info)

		case action == "join-pow" && r.Method == http.MethodPost:
			var body struct {
				Bits uint8 `json:"bits"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
				return
			}
			var info visor.GroupInfo
			if err := pairRPCCall("GroupSetJoinPoW", func(c visor.API) error {
				i, e := c.GroupSetJoinPoW(id, body.Bits)
				info = i
				return e
			}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, info)

		case action == "peer-backfill" && r.Method == http.MethodPost:
			var body struct {
				Enabled bool `json:"enabled"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
				return
			}
			var info visor.GroupInfo
			if err := pairRPCCall("GroupSetPeerBackfill", func(c visor.API) error {
				i, e := c.GroupSetPeerBackfill(id, body.Enabled)
				info = i
				return e
			}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, info)

		case action == "rotate-key" && r.Method == http.MethodPost:
			// No body: rotation targets the group, not a peer.
			var info visor.GroupInfo
			if err := pairRPCCall("GroupRotateKey", func(c visor.API) error {
				i, e := c.GroupRotateKey(id)
				info = i
				return e
			}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, info)

		case action == "readonly" && r.Method == http.MethodPost:
			var body struct {
				ReadOnly bool `json:"read_only"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
				return
			}
			var info visor.GroupInfo
			if err := pairRPCCall("GroupSetReadOnly", func(c visor.API) error {
				i, e := c.GroupSetReadOnly(id, body.ReadOnly)
				info = i
				return e
			}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, info)

		case action == "listed" && r.Method == http.MethodPost:
			var body struct {
				Listed bool `json:"listed"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
				return
			}
			var info visor.GroupInfo
			if err := pairRPCCall("GroupSetListed", func(c visor.API) error {
				i, e := c.GroupSetListed(id, body.Listed)
				info = i
				return e
			}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, info)

		case action == "leave" && r.Method == http.MethodPost:
			if err := pairRPCCall("GroupLeave", func(c visor.API) error { return c.GroupLeave(id) }); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)

		case action == "history" && r.Method == http.MethodGet:
			limit := 100
			if s := r.URL.Query().Get("limit"); s != "" {
				if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= 1000 {
					limit = n
				}
			}
			// before is the backward page cursor: the ts_nano of the oldest
			// message the caller already holds. Absent means "the newest
			// page", so a first load and a scroll-back are the same request.
			// Unparseable is treated as absent rather than an error — a
			// client that lost its cursor should get the newest page, not a
			// failure.
			var before time.Time
			if s := r.URL.Query().Get("before"); s != "" {
				if ns, err := strconv.ParseInt(s, 10, 64); err == nil && ns > 0 {
					before = time.Unix(0, ns).UTC()
				}
			}
			var msgs []visor.GroupMessage
			if err := pairRPCCall("GroupHistoryPage", func(c visor.API) error {
				out, e := c.GroupHistoryPage(visor.GroupHistoryPageArgs{
					GroupID: id, Before: before, Limit: limit,
				})
				msgs = out
				return e
			}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			// First pass: collect delete tombstones (keyed by author + target
			// ts_nano) so a reloading client / new joiner sees deletes applied
			// even if the pruned original still lingers on the feed.
			type delKey struct {
				sender string
				ts     int64
			}
			deleted := make(map[delKey]bool)
			for _, m := range msgs {
				if dmeta, ok := parseGroupDeleteText(m.Text); ok {
					deleted[delKey{m.SenderPK.Hex(), dmeta.ToTSNano}] = true
				}
			}
			// Second pass: emit chat rows, skipping tombstones and any message
			// its own author deleted for everyone. Enrich file/reply bodies +
			// carry ts_nano (the exact delete/reply identity).
			out := make([]map[string]any, 0, len(msgs))
			for _, m := range msgs {
				if _, ok := parseGroupDeleteText(m.Text); ok {
					continue
				}
				if deleted[delKey{m.SenderPK.Hex(), m.TS.UnixNano()}] {
					continue
				}
				row := map[string]any{
					"group_id":  m.GroupID,
					"sender_pk": m.SenderPK.Hex(),
					"text":      m.Text,
					"ts":        m.TS,
					"ts_nano":   m.TS.UnixNano(),
				}
				if meta, ok := parseGroupFileText(m.Text); ok {
					row["text"] = "📎 " + meta.Name
					enrichGroupFileRow(row, meta)
				} else if rmeta, ok := parseReplyText(m.Text); ok {
					row["text"] = rmeta.Text
					enrichReplyRow(row, rmeta)
				} else if fmeta, ok := parseForwardText(m.Text); ok {
					row["text"] = fmeta.Text
					enrichForwardRow(row, fmeta)
				}
				out = append(out, row)
			}
			writeJSON(w, out)

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// groupPeerActions maps a URL action onto the visor RPC it proxies.
// Every one of these takes {pk} and returns the updated GroupInfo, so a
// table beats seven near-identical switch arms — and adding a command
// later means adding a row, not another arm to keep in sync.
//
// Deny is the one that doesn't return info; it re-reads the group so
// the response shape stays uniform for the UI.
var groupPeerActions = map[string]func(string, cipher.PubKey) (visor.GroupInfo, error){
	"requests/approve": func(id string, pk cipher.PubKey) (visor.GroupInfo, error) {
		return groupPeerRPC("GroupApproveJoin", func(c visor.API) (visor.GroupInfo, error) {
			return c.GroupApproveJoin(id, pk)
		})
	},
	"requests/deny": func(id string, pk cipher.PubKey) (visor.GroupInfo, error) {
		if err := pairRPCCall("GroupDenyJoin", func(c visor.API) error {
			return c.GroupDenyJoin(id, pk)
		}); err != nil {
			return visor.GroupInfo{}, err
		}
		return groupPeerRPC("GroupGet", func(c visor.API) (visor.GroupInfo, error) {
			return c.GroupGet(id)
		})
	},
	"members/remove": func(id string, pk cipher.PubKey) (visor.GroupInfo, error) {
		return groupPeerRPC("GroupRemoveMember", func(c visor.API) (visor.GroupInfo, error) {
			return c.GroupRemoveMember(id, pk)
		})
	},
	"members/ban": func(id string, pk cipher.PubKey) (visor.GroupInfo, error) {
		return groupPeerRPC("GroupBanMember", func(c visor.API) (visor.GroupInfo, error) {
			return c.GroupBanMember(id, pk)
		})
	},
	"members/unban": func(id string, pk cipher.PubKey) (visor.GroupInfo, error) {
		return groupPeerRPC("GroupUnbanMember", func(c visor.API) (visor.GroupInfo, error) {
			return c.GroupUnbanMember(id, pk)
		})
	},
	"members/mute": func(id string, pk cipher.PubKey) (visor.GroupInfo, error) {
		return groupPeerRPC("GroupMuteMember", func(c visor.API) (visor.GroupInfo, error) {
			return c.GroupMuteMember(id, pk)
		})
	},
	"members/unmute": func(id string, pk cipher.PubKey) (visor.GroupInfo, error) {
		return groupPeerRPC("GroupUnmuteMember", func(c visor.API) (visor.GroupInfo, error) {
			return c.GroupUnmuteMember(id, pk)
		})
	},
	// Promote / demote were reachable over the CLI and the visor RPC but
	// had no route here, so a browser-only operator could never create a
	// second admin. That made the founder a permanent single point of
	// failure for admission — invites name the group's admins, and there
	// was never more than one to name.
	"members/promote": func(id string, pk cipher.PubKey) (visor.GroupInfo, error) {
		return groupPeerRPC("GroupPromoteAdmin", func(c visor.API) (visor.GroupInfo, error) {
			return c.GroupPromoteAdmin(id, pk)
		})
	},
	"members/demote": func(id string, pk cipher.PubKey) (visor.GroupInfo, error) {
		return groupPeerRPC("GroupDemoteAdmin", func(c visor.API) (visor.GroupInfo, error) {
			return c.GroupDemoteAdmin(id, pk)
		})
	},
}

// groupPeerRPC adapts an info-returning visor call to pairRPCCall's
// error-only closure shape.
func groupPeerRPC(name string, call func(visor.API) (visor.GroupInfo, error)) (visor.GroupInfo, error) {
	var info visor.GroupInfo
	err := pairRPCCall(name, func(c visor.API) error {
		i, e := call(c)
		info = i
		return e
	})
	return info, err
}

// parsePKList parses a list of hex public keys, skipping blank entries.
func parsePKList(raw []string) ([]cipher.PubKey, error) {
	out := make([]cipher.PubKey, 0, len(raw))
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		var pk cipher.PubKey
		if err := pk.Set(s); err != nil {
			return nil, err
		}
		out = append(out, pk)
	}
	return out, nil
}
