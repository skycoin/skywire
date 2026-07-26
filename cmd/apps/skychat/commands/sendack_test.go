// Package commands cmd/apps/skychat/commands/sendack_test.go
//
// Unit coverage for the opt-in chat-msg/chat-ack receipt protocol: the
// wait clamp, the ack-waiter register/deliver plumbing, and the
// envelope-recognition path that decides whether a frame is a chat
// envelope or plain text.
package commands

import (
	"encoding/json"
	"testing"
	"time"
)

func TestClampAckWait(t *testing.T) {
	cases := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{"zero -> floor", 0, chatAckTimeoutFloor},
		{"negative -> floor", -5 * time.Second, chatAckTimeoutFloor},
		{"below floor -> floor", 50 * time.Millisecond, chatAckTimeoutFloor},
		{"at floor -> floor", chatAckTimeoutFloor, chatAckTimeoutFloor},
		{"mid unchanged", 5 * time.Second, 5 * time.Second},
		{"at ceiling -> ceiling", chatAckTimeoutCeiling, chatAckTimeoutCeiling},
		{"above ceiling -> ceiling", 2 * time.Minute, chatAckTimeoutCeiling},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := clampAckWait(c.in); got != c.want {
				t.Errorf("clampAckWait(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestAckWaiterDeliverAndUnregister(t *testing.T) {
	ch, unregister := registerAckWaiter("ack-1")
	deliverAck("ack-1")
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("deliverAck did not signal the registered waiter")
	}
	unregister()
	// deliverAck after unregister must be a silent no-op (not a panic).
	deliverAck("ack-1")
}

func TestDeliverAckUnknownIsNoop(t *testing.T) {
	// No waiter registered for this id — must not panic or block.
	deliverAck("never-registered-xyz")
}

func TestDeliverAckIsNonBlockingWhenBufferFull(t *testing.T) {
	ch, unregister := registerAckWaiter("ack-full")
	defer unregister()
	// The waiter channel is buffered with depth 1. Two deliveries
	// without a drain: the first fills the buffer, the second must be
	// dropped via the select-default rather than block.
	deliverAck("ack-full")
	deliverAck("ack-full") // must not block

	select {
	case <-ch:
	default:
		t.Fatal("expected exactly one buffered signal")
	}
	select {
	case <-ch:
		t.Fatal("second signal should have been dropped (buffer was full)")
	default:
	}
}

func TestTryHandleChatEnvelope_ChatMsgWithAck(t *testing.T) {
	env := chatEnvelope{Type: chatTypeMsg, ID: "m1", Body: "hello", Ack: true}
	payload, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var ackedID string
	handled, body, id, kind := tryHandleChatEnvelope(payload, "peerhex", func(id string) { ackedID = id })
	if !handled || body != "hello" || id != "m1" || kind != chatTypeMsg {
		t.Fatalf("handled=%v body=%q id=%q kind=%q, want true/hello/m1/chat-msg", handled, body, id, kind)
	}
	if ackedID != "m1" {
		t.Errorf("sendAck called with %q, want m1", ackedID)
	}
}

func TestTryHandleChatEnvelope_ChatMsgNoAck(t *testing.T) {
	env := chatEnvelope{Type: chatTypeMsg, ID: "m2", Body: "hi"} // Ack:false
	payload, _ := json.Marshal(env)                              //nolint:errcheck
	acked := false
	handled, body, _, _ := tryHandleChatEnvelope(payload, "peer", func(string) { acked = true })
	if !handled || body != "hi" {
		t.Fatalf("handled=%v body=%q, want true/hi", handled, body)
	}
	if acked {
		t.Error("sendAck must not be called when Ack=false")
	}
}

func TestTryHandleChatEnvelope_ChatMsgNilSendAckSafe(t *testing.T) {
	env := chatEnvelope{Type: chatTypeMsg, ID: "m3", Body: "hi", Ack: true}
	payload, _ := json.Marshal(env) //nolint:errcheck
	// A nil sendAck with Ack=true must not panic — the message is still
	// surfaced.
	handled, body, _, _ := tryHandleChatEnvelope(payload, "peer", nil)
	if !handled || body != "hi" {
		t.Fatalf("handled=%v body=%q, want true/hi", handled, body)
	}
}

func TestTryHandleChatEnvelope_ChatAckDelivers(t *testing.T) {
	ch, unregister := registerAckWaiter("a1")
	defer unregister()
	env := chatEnvelope{Type: chatTypeAck, ID: "a1"}
	payload, _ := json.Marshal(env) //nolint:errcheck
	handled, body, id, kind := tryHandleChatEnvelope(payload, "peer", nil)
	if !handled || body != "" || id != "a1" || kind != chatTypeAck {
		t.Fatalf("handled=%v body=%q id=%q kind=%q, want true/empty/a1/chat-ack", handled, body, id, kind)
	}
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("chat-ack envelope did not deliver to the registered waiter")
	}
}

func TestTryHandleChatEnvelope_PlainAndMalformed(t *testing.T) {
	// None of these are recognized chat-* envelopes, so they must fall
	// through (handled=false) to the legacy plain-text path.
	cases := [][]byte{
		[]byte("hello plain text"),
		[]byte("{not valid json"),
		[]byte(`{"type":"unknown-kind","id":"x"}`),
		[]byte(`{"foo":"bar"}`),
		[]byte(""),
	}
	for _, p := range cases {
		handled, _, _, _ := tryHandleChatEnvelope(p, "peer", nil)
		if handled {
			t.Errorf("payload %q should NOT be handled as an envelope", p)
		}
	}
}

func TestTryHandleChatEnvelope_ChatRead(t *testing.T) {
	env := chatEnvelope{Type: chatTypeRead, ID: "r1"}
	payload, _ := json.Marshal(env) //nolint:errcheck
	// A chat-read is consumed (not surfaced) and reports its id + kind so the
	// caller can emit a "read" status for the referenced message.
	handled, body, id, kind := tryHandleChatEnvelope(payload, "peer", nil)
	if !handled || body != "" || id != "r1" || kind != chatTypeRead {
		t.Fatalf("handled=%v body=%q id=%q kind=%q, want true/empty/r1/chat-read", handled, body, id, kind)
	}
}
