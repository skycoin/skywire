package commands

import (
	"fmt"
	"testing"
	"time"
)

func drainSeqs(ch <-chan chatEvent) []uint64 {
	var got []uint64
	for {
		select {
		case ev := <-ch:
			got = append(got, ev.Seq)
		case <-time.After(20 * time.Millisecond):
			return got
		}
	}
}

func TestHub_RecordEvent_AssignsMonotonicSeq(t *testing.T) {
	h := newSSEHub()
	for i := 0; i < 3; i++ {
		h.recordEvent(chatEvent{ID: fmt.Sprintf("id%d", i), Channel: channelDM, Text: "x"})
	}
	if h.seq != 3 {
		t.Fatalf("seq=%d want 3", h.seq)
	}
}

func TestHub_SubscribeEvents_ReplaysSinceCursor(t *testing.T) {
	h := newSSEHub()
	for i := 0; i < 4; i++ {
		h.recordEvent(chatEvent{ID: fmt.Sprintf("id%d", i), Channel: channelDM})
	}
	// since=2 → replay seq 3,4 only
	ch, unsub := h.subscribeEvents(nil, 2)
	defer unsub()
	got := drainSeqs(ch)
	if len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Fatalf("replay since=2 got %v want [3 4]", got)
	}
}

func TestHub_DefaultExcludesSystem_OptInIncludes(t *testing.T) {
	h := newSSEHub()
	h.recordEvent(chatEvent{ID: "dm", Channel: channelDM})
	h.recordEvent(chatEvent{ID: "sys", Channel: channelSystem})
	h.recordEvent(chatEvent{ID: "grp", Channel: channelGroup})

	// Default (nil) = dm+group+pair, NOT system.
	def, unsub1 := h.subscribeEvents(nil, 0)
	defer unsub1()
	if got := len(drainSeqs(def)); got != 2 {
		t.Fatalf("default replay got %d events want 2 (dm+group, no system)", got)
	}

	// Opt into system explicitly.
	sys, unsub2 := h.subscribeEvents(map[string]bool{channelSystem: true}, 0)
	defer unsub2()
	if got := drainSeqs(sys); len(got) != 1 {
		t.Fatalf("system-only replay got %v want 1", got)
	}
}

func TestHub_LiveFanoutRespectsChannelFilter(t *testing.T) {
	h := newSSEHub()
	sys, unsub := h.subscribeEvents(map[string]bool{channelSystem: true}, 0)
	defer unsub()
	h.recordEvent(chatEvent{ID: "dm", Channel: channelDM})      // filtered out
	h.recordEvent(chatEvent{ID: "sys", Channel: channelSystem}) // delivered
	got := drainSeqs(sys)
	if len(got) != 1 {
		t.Fatalf("live system sub got %v want exactly the system event", got)
	}
}

func TestParseEventChannels(t *testing.T) {
	if parseEventChannels("") != nil {
		t.Error(`"" should be nil (default)`)
	}
	if parseEventChannels("all") != nil {
		t.Error(`"all" should be nil (default)`)
	}
	sys := parseEventChannels("system")
	if sys == nil || !sys[channelSystem] || sys[channelDM] {
		t.Errorf(`"system" got %v want {system}`, sys)
	}
	mix := parseEventChannels("all,system")
	for _, c := range []string{channelDM, channelGroup, channelPair, channelSystem} {
		if !mix[c] {
			t.Errorf(`"all,system" missing %s in %v`, c, mix)
		}
	}
}

func TestRenderLegacySSE_BackcompatShape(t *testing.T) {
	s := renderLegacySSE(chatEvent{From: "alice", Text: "hi", Transport: "dmsg", Dir: "in", ID: "x", Len: 2})
	for _, want := range []string{`"sender":"alice"`, `"message":"hi"`, `"network":"dmsg"`, `"dir":"in"`, `"id":"x"`, `"len":2`} {
		if !contains(s, want) {
			t.Errorf("legacy SSE %s missing %s", s, want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
