// Package commands cmd/apps/skychat/commands/dmstatus.go c4-app-chat
//
// DM message-status lifecycle: failed / pending / sent / received / read.
//
// pending/sent/failed are decided at send time and owned by the browser (the
// optimistic bubble flips on the /message response). received/read arrive
// asynchronously as peer receipts and are surfaced here as "dm-status" control
// events on the legacy /sse stream, so the sender's UI can advance a bubble's
// tick long after the send returned:
//
//   - received: the recipient's app auto-sends a chat-ack when it processes our
//     chat-msg (ack=true). handleConn turns that ack into a received status.
//   - read:     the recipient's UI, on displaying the message, POSTs
//     /read-receipt, which sends a chat-read envelope back. handleConn turns
//     that into a read status.
//
// A status event is {channel:"dm-status", id, status, peer}: id is the sender-
// side message id (the chat-msg envelope id, echoed by the receipt), peer is
// the other party's PK so the browser routes the update to the right thread.
// Line-based /sse consumers (cli skychat listen) ignore channel-tagged control
// events, exactly as they do for group / pair-invite events.
package commands

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
)

// DM status values (also the strings the browser renders as ticks).
const (
	dmStatusReceived = "received"
	dmStatusRead     = "read"
)

// broadcastDMStatus emits a dm-status control event on the legacy /sse stream.
// id is the sender-side message id; peer is the other party's PK hex. No-op on
// an empty id (nothing to correlate against).
func broadcastDMStatus(id, status, peer string) {
	if id == "" {
		return
	}
	body, err := json.Marshal(map[string]string{
		"channel": "dm-status",
		"id":      id,
		"status":  status,
		"peer":    peer,
	})
	if err != nil {
		appLog("dm-status marshal failed: %v", err)
		return
	}
	hub.broadcast(string(body))
}

// sendReadReceipt writes a chat-read envelope for message id to peer over a
// reused/fresh framed conn. Best-effort: the peer turns it into a "read" status
// on its side; a delivery failure just means the tick never advances.
func sendReadReceipt(ctx context.Context, peer cipher.PubKey, id string) error {
	env := chatEnvelope{Type: chatTypeRead, ID: id}
	payload, err := env.Marshal()
	if err != nil {
		return err
	}
	conn, err := dialOrReusePeerConn(ctx, peer)
	if err != nil {
		return err
	}
	return conn.WriteFrame(payload)
}

// readReceiptHandler serves POST /read-receipt {pk, ids:[...]} (or a single
// id): the recipient's UI reports which inbound messages from pk it has now
// displayed, and we send a chat-read envelope per id back to that peer so the
// original sender's bubbles advance to "read".
func readReceiptHandler(ctx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			PK  string   `json:"pk"`
			ID  string   `json:"id,omitempty"`
			IDs []string `json:"ids,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
			return
		}
		peer, err := parsePK(body.PK)
		if err != nil {
			http.Error(w, "invalid pk: "+err.Error(), http.StatusBadRequest)
			return
		}
		if appCl == nil {
			http.Error(w, "standalone mode: no visor transport for read receipts", http.StatusServiceUnavailable)
			return
		}
		ids := body.IDs
		if strings.TrimSpace(body.ID) != "" {
			ids = append(ids, body.ID)
		}
		for _, id := range ids {
			if strings.TrimSpace(id) == "" {
				continue
			}
			if err := sendReadReceipt(ctx, peer, id); err != nil {
				appLog("skychat: read-receipt %s to %s failed: %v", id, peer, err)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
