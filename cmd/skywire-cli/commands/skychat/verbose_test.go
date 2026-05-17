// Package cliskychat — verbose_test.go: pins counter-delta rendering.
// Operator-facing: a botched delta render is worse than no verbose
// output because it gives a false reading of which layer absorbed
// the failure during baseline testing.
package cliskychat

import (
	"strings"
	"testing"
)

func TestStatusCountersDeltaNoChange(t *testing.T) {
	c := &statusCounters{OutboundMsgCount: 5, InboundMsgCount: 3}
	prev := &statusCounters{OutboundMsgCount: 5, InboundMsgCount: 3}
	got := c.delta(prev)
	if got != "<no change>" {
		t.Errorf("delta no-change: got %q, want %q", got, "<no change>")
	}
}

func TestStatusCountersDeltaSurfacesOnlyChanges(t *testing.T) {
	// A clean send: outbound_msg +1, nothing else moves. Operator
	// should see exactly that — not zero-deltas for every counter.
	c := &statusCounters{OutboundMsgCount: 6, InboundMsgCount: 3}
	prev := &statusCounters{OutboundMsgCount: 5, InboundMsgCount: 3}
	got := c.delta(prev)
	if got != "outbound_msg=+1" {
		t.Errorf("delta clean-send: got %q, want %q", got, "outbound_msg=+1")
	}
}

func TestStatusCountersDeltaProblematicSend(t *testing.T) {
	// A send that took the retry path + the fallback path BUT
	// ultimately succeeded. Operator should see msg=+1 AND
	// retry=+1 AND fallback=+1 — that's the diagnostic value.
	c := &statusCounters{
		OutboundMsgCount:      6,
		OutboundRetryCount:    8,
		OutboundFallbackCount: 4,
	}
	prev := &statusCounters{
		OutboundMsgCount:      5,
		OutboundRetryCount:    7,
		OutboundFallbackCount: 3,
	}
	got := c.delta(prev)
	for _, want := range []string{"outbound_msg=+1", "outbound_retry=+1", "outbound_fallback=+1"} {
		if !strings.Contains(got, want) {
			t.Errorf("delta missing %q in %q", want, got)
		}
	}
}

func TestStatusCountersDeltaNilPrev(t *testing.T) {
	// Edge case: status fetch failed pre-send, so prev is nil.
	// Render <no baseline> rather than panic or compare against
	// zeros (which would surface false +N deltas on every counter).
	c := &statusCounters{OutboundMsgCount: 5}
	if got := c.delta(nil); got != "<no baseline>" {
		t.Errorf("delta nil-prev: got %q, want %q", got, "<no baseline>")
	}
}

func TestStatusCountersSummaryIncludesAllCounters(t *testing.T) {
	c := &statusCounters{
		OutboundMsgCount: 1, OutboundFailCount: 2, OutboundRetryCount: 3, OutboundFallbackCount: 4,
		InboundMsgCount: 5, InboundDropCount: 6,
		SSESubscribers: 7, SSEDropCount: 8, ActivePeerConns: 9,
	}
	s := c.summary()
	for _, label := range []string{"outbound", "inbound", "sse", "peers"} {
		if !strings.Contains(s, label) {
			t.Errorf("summary missing %q section: %s", label, s)
		}
	}
}
