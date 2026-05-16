// Package visor — pkg/visor/group_sub_dropcount_test.go: pins the
// groupInbox.SubDropCount accumulator semantics added so the chat-app
// /status response surfaces a live signal when the gRPC
// StreamGroupMessages fan-out is silently dropping messages between
// inbox.deliver and a bounded subscriber channel. Pre-fix, the
// per-subscriber dropCount was only readable at unsubscribe time and
// went nowhere operator-visible — leaving a diagnostic gap where a
// busy group looked healthy by subscriber_alive + last_message_at
// but the listener pipe was silently shedding messages.
//
// Integration coverage (a full gRPC handler reading a slow stream)
// rides the existing E2E plan; this file pins the inbox-side
// counter contract in isolation.
package visor

import (
	"testing"

	skychatgroup "github.com/skycoin/skywire/cmd/apps/skychat/group"
	"github.com/skycoin/skywire/pkg/cipher"
)

func TestGroupInbox_SubDropCount_DefaultsZero(t *testing.T) {
	// Fresh inbox with no subscribers + no deliveries → 0. Both the
	// live-sum and the accumulator paths must initialize empty.
	g := newGroupInbox(0)
	if got := g.SubDropCount(); got != 0 {
		t.Errorf("fresh inbox: SubDropCount = %d, want 0", got)
	}
}

func TestGroupInbox_SubDropCount_LiveSumWhenChannelFull(t *testing.T) {
	// Subscribe with a buf=1 channel, deliver 5 messages without
	// draining. deliver's select+default fires on every overflow
	// and bumps sub.dropCount → SubDropCount returns the running
	// live sum.
	g := newGroupInbox(0)
	sub := g.subscribe(1)
	defer g.unsubscribe(sub)

	pk, _ := cipher.GenerateKeyPair()
	for i := 0; i < 5; i++ {
		g.deliver("group-a", pk, skychatgroup.Message{Text: "x"})
	}
	// One message fits in the buf=1 channel, the other four drop.
	if got := g.SubDropCount(); got != 4 {
		t.Errorf("after 5 deliveries on buf=1 sub: SubDropCount = %d, want 4", got)
	}
}

func TestGroupInbox_SubDropCount_AccumulatesAcrossUnsubscribe(t *testing.T) {
	// Drop count harvested at unsubscribe must persist into the
	// inbox-wide total so a fresh subscriber doesn't reset the
	// operator-visible /status counter. This is the property that
	// makes the rolling total monotonic across stream restarts.
	g := newGroupInbox(0)

	// Stream 1: 3 drops, then teardown.
	sub1 := g.subscribe(1)
	pk, _ := cipher.GenerateKeyPair()
	for i := 0; i < 4; i++ {
		g.deliver("group-a", pk, skychatgroup.Message{Text: "x"})
	}
	if got := g.SubDropCount(); got != 3 {
		t.Fatalf("pre-unsubscribe: SubDropCount = %d, want 3", got)
	}
	g.unsubscribe(sub1)
	if got := g.SubDropCount(); got != 3 {
		t.Errorf("post-unsubscribe: SubDropCount = %d, want 3 (accumulator should carry forward)", got)
	}

	// Stream 2: subscribe afresh, no new drops yet → total unchanged.
	sub2 := g.subscribe(1)
	defer g.unsubscribe(sub2)
	if got := g.SubDropCount(); got != 3 {
		t.Errorf("fresh subscribe after teardown: SubDropCount = %d, want 3", got)
	}

	// Stream 2 drops 2 more.
	for i := 0; i < 3; i++ {
		g.deliver("group-a", pk, skychatgroup.Message{Text: "y"})
	}
	if got := g.SubDropCount(); got != 5 {
		t.Errorf("post-second-stream drops: SubDropCount = %d, want 5 (3 accumulated + 2 live)", got)
	}
}

func TestGroupInbox_SubDropCount_AggregatesMultipleLiveSubs(t *testing.T) {
	// Two concurrent subscribers, both with buf=1, both observe
	// overflows. SubDropCount sums across both — operators tracking
	// the visor-wide total should see both contributions.
	g := newGroupInbox(0)
	sub1 := g.subscribe(1)
	sub2 := g.subscribe(1)
	defer g.unsubscribe(sub1)
	defer g.unsubscribe(sub2)

	pk, _ := cipher.GenerateKeyPair()
	for i := 0; i < 4; i++ {
		g.deliver("group-a", pk, skychatgroup.Message{Text: "x"})
	}
	// Each sub takes 1, drops 3 → total 6.
	if got := g.SubDropCount(); got != 6 {
		t.Errorf("2 live subs each buf=1, 4 deliveries: SubDropCount = %d, want 6", got)
	}
}

func TestGroupInbox_SubDropCount_RoomyChannelDoesNotDrop(t *testing.T) {
	// Sanity-check the inverse: when the subscriber's channel buffer
	// is large enough to hold every delivery, no drops accrue.
	// Sizing the buffer past the deliver count makes the test
	// deterministic — a goroutine-drain variant raced with the
	// deliver tight-loop on slower machines because the scheduler
	// can't always interleave them fast enough.
	//
	// Prevents a regression where the counter would tick on every
	// deliver regardless of channel pressure.
	g := newGroupInbox(0)
	const N = 50
	sub := g.subscribe(N * 2) // headroom; no select+default firings expected
	t.Cleanup(func() { g.unsubscribe(sub) })

	pk, _ := cipher.GenerateKeyPair()
	for i := 0; i < N; i++ {
		g.deliver("group-a", pk, skychatgroup.Message{Text: "x"})
	}
	if got := g.SubDropCount(); got != 0 {
		t.Errorf("buf=2N, N deliveries: SubDropCount = %d, want 0", got)
	}
}
