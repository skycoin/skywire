// Package commands cmd/apps/skychat/commands/sendack.go c4-app-chat
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
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/skychat/message"
)

// chatEnvelope / chatTypeMsg / chatTypeAck alias the shared skychat wire types
// (pkg/skychat/message) so this file's ack routing and the envelope construction
// in skychat.go keep their familiar local names while the on-the-wire shape lives
// in exactly one place.
type chatEnvelope = message.Envelope

const (
	chatTypeMsg  = message.TypeMsg
	chatTypeAck  = message.TypeAck
	chatTypeRead = message.TypeRead
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

// tryHandleChatEnvelope attempts to parse payload as a chat-msg,
// chat-ack, or chat-read envelope. The returned kind is the envelope
// type (chatTypeMsg/chatTypeAck/chatTypeRead), or "" when payload is
// not a recognized envelope and the caller should fall through to the
// legacy plain-text path:
//
//   - chat-msg  → (handled=true, body=<text>, id, kind=chatTypeMsg):
//     surface body as a normal chat message; an ack was sent back first
//     when the sender requested one.
//   - chat-ack  → (handled=true, body="", id, kind=chatTypeAck): a
//     receipt for a message WE sent — consumed internally (satisfies a
//     --wait waiter; the caller also emits "received" status).
//   - chat-read → (handled=true, body="", id, kind=chatTypeRead): the
//     peer has now displayed a message WE sent — the caller emits "read"
//     status. Nothing is surfaced to the chat UI.
//
// Envelope recognition is conservative: must start with '{', valid
// JSON, type field matches a known chat-* value. Anything else is
// "not an envelope" so plain-text JSON-looking chat (e.g. someone
// typing literal {} into a chat window) still reaches its peer.
func tryHandleChatEnvelope(payload []byte, peerPKHex string, sendAck func(id string)) (handled bool, body string, id string, kind string) {
	env, ok := message.ParseEnvelope(payload)
	if !ok {
		return false, "", "", ""
	}
	switch env.Type {
	case chatTypeMsg:
		if env.Ack && env.ID != "" && sendAck != nil {
			// Best-effort: a failed ack write is logged by the
			// caller's sendAck implementation; we still surface the
			// message so the recipient sees it.
			sendAck(env.ID)
		}
		return true, env.Body, env.ID, chatTypeMsg
	case chatTypeAck:
		if env.ID != "" {
			deliverAck(env.ID)
		}
		// Consumed; nothing to surface to the chat UI.
		return true, "", env.ID, chatTypeAck
	case chatTypeRead:
		// Consumed; the caller turns it into a "read" status update.
		return true, "", env.ID, chatTypeRead
	}
	_ = peerPKHex // reserved for future per-peer ack policy / rate-limit
	return false, "", "", ""
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
