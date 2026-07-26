// Package commands cmd/apps/skychat/commands/sse_hub_test.go
//
// Unit coverage for the legacy /sse hub surface — the string replay
// ring, live fan-out, the no-subscribers drop-count regression guard,
// publishEvent reaching both /sse and /events, and the event-id
// generator. The structured /events ring is covered separately in
// events_hub_test.go.
package commands

import (
	"strings"
	"testing"
	"time"
)

// drainStrings collects everything currently buffered in a legacy /sse
// client channel, returning once the channel goes idle briefly.
func drainStrings(ch <-chan string) []string {
	var got []string
	for {
		select {
		case s := <-ch:
			got = append(got, s)
		case <-time.After(20 * time.Millisecond):
			return got
		}
	}
}

func TestSSEHub_ReplayToLateSubscriber(t *testing.T) {
	h := newSSEHub()
	// Broadcast before anyone subscribes.
	h.broadcast("m1")
	h.broadcast("m2")
	// A subscriber connecting afterwards receives the replay, oldest first.
	ch, unsub := h.subscribe()
	defer unsub()
	got := drainStrings(ch)
	if len(got) != 2 || got[0] != "m1" || got[1] != "m2" {
		t.Fatalf("replay = %v, want [m1 m2]", got)
	}
}

func TestSSEHub_BroadcastFansOutToAllSubscribers(t *testing.T) {
	h := newSSEHub()
	c1, u1 := h.subscribe()
	defer u1()
	c2, u2 := h.subscribe()
	defer u2()
	h.broadcast("live")
	// Both subscribers see exactly the one live message (no prior replay).
	for i, ch := range []<-chan string{c1, c2} {
		got := drainStrings(ch)
		if len(got) != 1 || got[0] != "live" {
			t.Errorf("subscriber %d got %v, want [live]", i, got)
		}
	}
}

func TestSSEHub_NoSubscribersCountsDrop(t *testing.T) {
	h := newSSEHub()
	// sseDropCount is a process global; reset immediately before and read
	// immediately after so no other (sequential) test interleaves.
	counterMu.Lock()
	sseDropCount = 0
	counterMu.Unlock()

	h.broadcast("nobody-listening")

	counterMu.Lock()
	drops := sseDropCount
	counterMu.Unlock()
	if drops != 1 {
		t.Errorf("broadcast with no subscribers: sseDropCount = %d, want 1", drops)
	}
	// The message still lands in the replay ring for a future reconnect.
	ch, unsub := h.subscribe()
	defer unsub()
	if got := drainStrings(ch); len(got) != 1 || got[0] != "nobody-listening" {
		t.Errorf("dropped message should still be replayable, got %v", got)
	}
}

func TestSSEHub_ClientCount(t *testing.T) {
	h := newSSEHub()
	if h.clientCount() != 0 {
		t.Fatalf("fresh hub clientCount = %d, want 0", h.clientCount())
	}
	_, u1 := h.subscribe()
	_, u2 := h.subscribe()
	if h.clientCount() != 2 {
		t.Errorf("clientCount = %d, want 2", h.clientCount())
	}
	u1()
	if h.clientCount() != 1 {
		t.Errorf("clientCount after one unsub = %d, want 1", h.clientCount())
	}
	u2()
	if h.clientCount() != 0 {
		t.Errorf("clientCount after all unsub = %d, want 0", h.clientCount())
	}
}

func TestSSEHub_PublishEventReachesBothSurfaces(t *testing.T) {
	h := newSSEHub()
	legacy, unsubL := h.subscribe()
	defer unsubL()
	events, unsubE := h.subscribeEvents(nil, 0)
	defer unsubE()

	h.publishEvent(chatEvent{
		ID: "e1", Channel: channelDM, Transport: "dmsg", Dir: "in", From: "alice", Text: "hi",
	})

	// Legacy /sse gets the rendered back-compat JSON.
	got := drainStrings(legacy)
	if len(got) != 1 || !strings.Contains(got[0], `"sender":"alice"`) || !strings.Contains(got[0], `"message":"hi"`) {
		t.Errorf("legacy surface got %v, want rendered sender/message", got)
	}
	// Structured /events gets the chatEvent.
	select {
	case ev := <-events:
		if ev.ID != "e1" || ev.Text != "hi" || ev.From != "alice" {
			t.Errorf("events surface got %+v, want id=e1 text=hi from=alice", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("publishEvent did not reach the /events surface")
	}
}

func TestNewEventID_UniqueAndHex(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for i := range 1000 {
		id := newEventID()
		if len(id) != 16 {
			t.Fatalf("event id %q len=%d, want 16", id, len(id))
		}
		if strings.TrimLeft(id, "0123456789abcdef") != "" {
			t.Errorf("event id %q is not lowercase hex", id)
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate event id %q at iteration %d", id, i)
		}
		seen[id] = struct{}{}
	}
}
