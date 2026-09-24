// Package commands forget_replay_test.go
package commands

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skychat/history"
)

// drain takes whatever the hub pre-filled a new subscriber with.
func drain(ch <-chan string) []string {
	var out []string
	for {
		select {
		case m := <-ch:
			out = append(out, m)
		default:
			return out
		}
	}
}

// TestForgetAllPurgesTheReplayRing: a forgotten conversation must not come
// back through /sse. The page re-subscribes on every load and the hub replays
// its ring to a new subscriber, so a delete that only emptied the store was
// undone by the ring on the phone's next return to the chat tab.
func TestForgetAllPurgesTheReplayRing(t *testing.T) {
	if appLog == nil {
		appLog = func(string, ...any) {}
	}
	withChatLog(t)
	restoreHistoryStore(t)
	prev := hub
	hub = newSSEHub()
	t.Cleanup(func() { hub = prev })
	historyStore = newTempStore(t, history.Limits{})

	gone, _ := cipher.GenerateKeyPair()
	kept, _ := cipher.GenerateKeyPair()
	hub.publishEvent(chatEvent{ID: "in", Channel: channelDM, Dir: "in", From: gone.Hex(), Text: "from them"})
	hub.publishEvent(chatEvent{ID: "out", Channel: channelDM, Dir: "out", From: "self", To: gone.Hex(), Text: "to them"})
	hub.publishEvent(chatEvent{ID: "other", Channel: channelDM, Dir: "in", From: kept.Hex(), Text: "someone else"})
	// The same peer's post in a group is not part of the conversation.
	hub.broadcast(`{"channel":"group","group_id":"g1","sender":"` + gone.Hex() + `","message":"in a group"}`)
	if err := historyStore.Append(history.Message{Peer: gone.Hex(), ID: "in", Text: "from them", Timestamp: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	forgetHandler(rr, httptest.NewRequest(http.MethodPost, "/history/forget", strings.NewReader(`{"pk":"`+gone.Hex()+`","all":true}`)))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("forget all: code=%d body=%q", rr.Code, rr.Body.String())
	}

	ch, unsubscribe := hub.subscribe()
	defer unsubscribe()
	replayed := drain(ch)
	var sawGroup, sawKept bool
	for _, m := range replayed {
		if strings.Contains(m, `"channel":"group"`) {
			sawGroup = true
			continue
		}
		if strings.Contains(m, gone.Hex()) {
			t.Errorf("the forgotten conversation is still replayed to a new subscriber: %s", m)
		}
		if strings.Contains(m, kept.Hex()) {
			sawKept = true
		}
	}
	if !sawGroup {
		t.Error("the peer's group post was purged with their conversation")
	}
	if !sawKept {
		t.Error("another peer's message was purged")
	}

	// The structured /events ring too.
	hub.mu.Lock()
	for i := 0; i < hub.eventsLen; i++ {
		ev := hub.events[i]
		if ev.Channel == channelDM && (ev.From == gone.Hex() || ev.To == gone.Hex()) {
			t.Errorf("the forgotten conversation is still in the /events ring: %+v", ev)
		}
	}
	n := hub.eventsLen
	hub.mu.Unlock()
	if n != 1 {
		t.Errorf("/events ring holds %d events after the purge, want 1 (the other peer's)", n)
	}
}
