// Package commands cmd/apps/skychat/sendack.go
//
// chat-msg / chat-ack envelope: opt-in peer-receipt acknowledgment.
//
// Wire format (JSON-encoded payload inside a normal framed frame):
//
//	chat-msg: {"type":"chat-msg","id":"<hex>","body":"...","ack":true}
//	chat-ack: {"type":"chat-ack","id":"<hex>"}
//
// Backward compat: when --wait is NOT used, the sender writes the
// plain-text body as before (no envelope). Old peers + new peers see
// the same plaintext message. The envelope is OPT-IN at the sender
// — when --wait is set, the sender wraps in chat-msg and waits for
// chat-ack with the matching id. An old peer that receives a
// chat-msg envelope will surface the entire JSON as chat text (ugly,
// but the agent-coordination use case is all-post-2510 peers anyway).
//
// Why not always envelope: it would force every existing human-user
// chat to look at JSON on old visors during the staggered roll.
// Opt-in keeps the wire byte-identical for default sends, the same
// as it has been since #2504.
//
// chat-ack is sent back over the same framed conn the chat-msg came
// in on (writeMu protects the framing). The sender's handleConn
// recognizes the inbound chat-ack and routes it to a waiter map
// keyed by id. The /message HTTP handler holds the request open
// until either: the ack arrives, the deadline fires, or the conn
// dies.

package commands

import (
	"encoding/json"
	"sync"
	"time"
)

// chatEnvelope is the on-the-wire shape for both chat-msg (carries
// body) and chat-ack (just id). Fields are encoded omitempty so the
// two share one Go struct without leaking irrelevant fields.
type chatEnvelope struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Body string `json:"body,omitempty"`
	Ack  bool   `json:"ack,omitempty"` // chat-msg only: request ack-on-receipt
}

const (
	chatTypeMsg = "chat-msg"
	chatTypeAck = "chat-ack"
)

// ackWaiters routes an inbound chat-ack envelope back to the
// /message HTTP handler that's waiting for it. Keyed by event id;
// value is a one-shot channel that fires on receipt.
//
// Lifetime: the /message handler registers the waiter before writing
// the chat-msg frame, deregisters it before returning regardless of
// outcome. A spurious ack (one nobody is waiting for) is silently
// dropped — that handles late acks after a timeout cleanly.
var (
	ackWaitersMu sync.Mutex
	ackWaiters   = map[string]chan struct{}{}
)

// registerAckWaiter adds a one-shot channel for the given id. Returns
// the channel + an unregister func the caller MUST invoke (typically
// via defer) regardless of whether the wait succeeded or timed out.
func registerAckWaiter(id string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	ackWaitersMu.Lock()
	ackWaiters[id] = ch
	ackWaitersMu.Unlock()
	return ch, func() {
		ackWaitersMu.Lock()
		delete(ackWaiters, id)
		ackWaitersMu.Unlock()
	}
}

// deliverAck signals any waiter registered for id. No-op if no
// waiter exists (late ack / unsolicited ack). Idempotent.
func deliverAck(id string) {
	ackWaitersMu.Lock()
	ch, ok := ackWaiters[id]
	ackWaitersMu.Unlock()
	if !ok {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
		// already signaled
	}
}

// tryHandleChatEnvelope attempts to parse payload as a chat-msg or
// chat-ack envelope. Returns (handled=true, body=...) if it's a
// chat-msg the caller should surface as a normal chat message after
// optionally sending an ack back. Returns (handled=true, body="")
// if it's a chat-ack (consumed internally, no surfacing). Returns
// (handled=false, _) for anything else — caller falls through to
// the legacy plain-text path.
//
// Envelope recognition is conservative: must start with '{', valid
// JSON, type field matches a known chat-* value. Anything else is
// "not an envelope" so plain-text JSON-looking chat (e.g. someone
// typing literal {} into a chat window) still reaches its peer.
func tryHandleChatEnvelope(payload []byte, peerPKHex string, sendAck func(id string)) (handled bool, body string, id string) {
	trimmed := bytesTrimSpace(payload)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false, "", ""
	}
	var env chatEnvelope
	if err := json.Unmarshal(trimmed, &env); err != nil {
		return false, "", ""
	}
	switch env.Type {
	case chatTypeMsg:
		if env.Ack && env.ID != "" && sendAck != nil {
			// Best-effort: a failed ack write is logged by the
			// caller's sendAck implementation; we still surface the
			// message so the recipient sees it.
			sendAck(env.ID)
		}
		return true, env.Body, env.ID
	case chatTypeAck:
		if env.ID != "" {
			deliverAck(env.ID)
		}
		// Consumed; nothing to surface to the chat UI.
		return true, "", env.ID
	}
	_ = peerPKHex // reserved for future per-peer ack policy / rate-limit
	return false, "", ""
}

// chatAckTimeoutFloor / chatAckTimeoutCeiling clamp the wait_ms
// parameter on /message. Lower bound prevents zero-wait misuse;
// upper bound bounds resource consumption on a held HTTP request.
const (
	chatAckTimeoutFloor   = 100 * time.Millisecond
	chatAckTimeoutCeiling = 60 * time.Second
)

// clampAckWait normalizes a caller-supplied wait_ms into the
// allowed range. Zero / negative / unset → floor; over-ceiling → ceiling.
func clampAckWait(d time.Duration) time.Duration {
	if d < chatAckTimeoutFloor {
		return chatAckTimeoutFloor
	}
	if d > chatAckTimeoutCeiling {
		return chatAckTimeoutCeiling
	}
	return d
}
