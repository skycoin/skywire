// Package commands cmd/apps/skychat/commands/sendack.go c4-app-chat
//
// The chat-msg / chat-ack envelope, the per-id ack-waiter map, and the inbound
// envelope decode all moved into the shared controller (pkg/skychat/dm) when
// the DM core was extracted — the native app now calls dm.Controller.Send with
// a WaitAck duration instead of registering waiters itself, and the controller
// routes inbound chat-ack envelopes to those waiters internally.
//
// What remains here is the HTTP-layer clamp for the /message handler's wait_ms
// parameter, which bounds how long a --wait request may block the controller's
// ack channel.
package commands

import "time"

// chatAckTimeoutFloor / chatAckTimeoutCeiling clamp the wait_ms parameter on
// /message. Lower bound prevents zero-wait misuse; upper bound bounds resource
// consumption on a held HTTP request.
const (
	chatAckTimeoutFloor   = 100 * time.Millisecond
	chatAckTimeoutCeiling = 60 * time.Second
)

// clampAckWait normalizes a caller-supplied wait_ms into the allowed range.
// Zero / negative / unset → floor; over-ceiling → ceiling.
func clampAckWait(d time.Duration) time.Duration {
	if d < chatAckTimeoutFloor {
		return chatAckTimeoutFloor
	}
	if d > chatAckTimeoutCeiling {
		return chatAckTimeoutCeiling
	}
	return d
}
