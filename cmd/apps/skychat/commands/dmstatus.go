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
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skychat/message"
)

// DM status values (also the strings the browser renders as ticks).
const (
	dmStatusReceived = "received"
	dmStatusRead     = "read"
	// dmStatusDeleted flags a message deleted for everyone: the browser replaces
	// the bubble with a "this message was deleted" placeholder. Broadcast on
	// both ends — the deleter's own UI and, via OnDelete, the recipient's.
	dmStatusDeleted = "deleted"
)

// staleAckWindow bounds how long a non-blocking receipts send waits for the
// peer's chat-ack before its conn is treated as half-open and evicted (so the
// next send redials). Generous relative to a real ack (sub-second on a live
// conn) to avoid dropping a healthy conn on a momentarily-slow peer. The wait
// itself lives in the controller (dm.Controller.watchAck), which owns the conn
// cache; this is only the policy value the app hands it.
const staleAckWindow = 20 * time.Second

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
	env := message.Envelope{Type: message.TypeRead, ID: id}
	payload, err := env.Marshal()
	if err != nil {
		return err
	}
	if chatCtrl == nil {
		return errors.New("skychat: dm controller not started")
	}
	// SendRaw reuses the controller's cached conn (or dials one) and writes the
	// frame as-is — no envelope wrapping, no persistence, nothing surfaced.
	return chatCtrl.SendRaw(ctx, peer, payload)
}

// forgetPersisted erases the stored copy of message id in peer's conversation,
// so a delete-for-everyone survives a reload instead of coming back the next
// time a client hydrates from /history. Best-effort and symmetric: both the
// deleter (deleteHandler) and the recipient (the controller's OnDelete) call
// it, so neither side keeps bytes the author retracted. No-op when persistence
// is off.
func forgetPersisted(peer, id string) {
	if historyStore == nil || id == "" {
		return
	}
	switch found, err := historyStore.DeleteByID(peer, id); {
	case err != nil:
		appLog("skychat: history delete %s for %s failed: %v", id, peer, err)
	case !found:
		// Nothing stored under that id — persistence off when it arrived, or
		// the record already aged out. The live tombstone still applies.
		chatLog.Debugf("history: no stored copy of %s for %s", id, peer)
	}
}

// readReceiptHandler serves POST /read-receipt {pk, ids:[...]} (or a single
// id): the recipient's UI reports which inbound messages from pk it has now
// displayed, and we send a chat-read envelope per id back to that peer so the
// original sender's bubbles advance to "read".
// deleteHandler serves POST /delete {pk, id}: the operator deletes their own
// message for everyone. Sends a chat-delete to the peer (their UI tombstones it
// via OnDelete), drops our own stored copy, and broadcasts a dm-status
// "deleted" locally so the deleter's own bubble tombstones immediately.
func deleteHandler(ctx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			PK string `json:"pk"`
			ID string `json:"id"`
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
		if strings.TrimSpace(body.ID) == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		if chatCtrl == nil {
			http.Error(w, "chat controller not ready", http.StatusServiceUnavailable)
			return
		}
		if err := chatCtrl.SendDelete(ctx, peer, body.ID); err != nil {
			appLog("skychat: delete %s to %s failed: %v", body.ID, peer, err)
			// Still tombstone locally — the operator's intent is recorded even if
			// the peer is currently unreachable (they can't receive the delete).
		}
		forgetPersisted(peer.Hex(), body.ID)
		broadcastDMStatus(body.ID, dmStatusDeleted, peer.Hex())
		w.WriteHeader(http.StatusNoContent)
	}
}

// forgetHandler serves POST /history/forget {pk, id}: drop OUR stored copy of
// one message and tell nobody.
//
// This is what makes "delete for me" on a DM stick. The browser's thread cache
// is not the durable copy — the visor's history store is, and selectRecipient
// refills an empty thread from it on every open (loadHistoryFor). So a delete
// that only edited the cache came back the moment the user left the
// conversation and returned, which is the reported "deleted chat records
// reappeared when I returned to the chat from another tab". Groups never had
// this: they reload from history too, but they remember a tombstone per
// deleted message and filter it out.
//
// Deliberately not deleteHandler's little sibling: no chat-delete goes to the
// peer (their copy is theirs), and no dm-status is broadcast (a tombstone
// event would tell every other tab to hide a message they may not have chosen
// to delete). Erasing the bytes rather than remembering a tombstone is also
// the better answer for a chat that stores its history on the user's own
// device — a hidden message is still a stored message.
func forgetHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if historyStore == nil {
		// Persistence is off, so there is no stored copy to forget and the
		// caller's local removal is already the whole story.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var body struct {
		PK string `json:"pk"`
		ID string `json:"id"`
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
	if strings.TrimSpace(body.ID) == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	forgetPersisted(peer.Hex(), body.ID)
	w.WriteHeader(http.StatusNoContent)
}

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
