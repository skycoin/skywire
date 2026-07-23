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
	"strconv"
	"strings"
	"time"

	"github.com/skycoin/skywire/cmd/apps/skychat/group"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor"
)

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
		ticker := time.NewTicker(pairPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
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
				envelope := map[string]string{
					"sender":   m.SenderPK.Hex(),
					"message":  m.Text,
					"channel":  "group",
					"group_id": m.GroupID,
					"ts":       m.TS.Format(time.RFC3339Nano),
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
			}
		}
	}()
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
			writeJSON(w, groups)

		case http.MethodPost:
			var body struct {
				Name    string   `json:"name"`
				Mode    string   `json:"mode"`
				Members []string `json:"members"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
				return
			}
			if strings.TrimSpace(body.Name) == "" {
				http.Error(w, "name required", http.StatusBadRequest)
				return
			}
			mode := group.Mode(body.Mode)
			if body.Mode == "" {
				mode = group.ModePublic
			}
			if !mode.IsValid() {
				http.Error(w, "invalid mode (use public or private)", http.StatusBadRequest)
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
				i, l, e := c.GroupCreate(visor.GroupCreateArgs{Name: body.Name, Mode: mode, InitialMembers: members})
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

// groupJoinHandler serves POST /group/join {invite}.
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
			Invite string `json:"invite"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(body.Invite) == "" {
			http.Error(w, "invite required", http.StatusBadRequest)
			return
		}
		var info visor.GroupInfo
		if err := pairRPCCall("GroupJoin", func(c visor.API) error {
			i, e := c.GroupJoin(visor.GroupJoinArgs{Invite: body.Invite})
			info = i
			return e
		}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, info)
	}
}

// groupItemHandler serves the per-group subtree:
//
//	GET    /group/<id>          → info
//	DELETE /group/<id>          → delete (owner)
//	GET    /group/<id>/invite   → invite link
//	POST   /group/<id>/send     → publish a message
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
			var msgs []visor.GroupMessage
			if err := pairRPCCall("GroupHistory", func(c visor.API) error {
				out, e := c.GroupHistory(id, limit)
				msgs = out
				return e
			}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, msgs)

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
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
