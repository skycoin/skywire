// Package visor pkg/visor/group_deliver_count_test.go
//
// Targeted coverage for the deliverCount counter added to groupInbox
// for drop-site localization. The full layer-counter surface (also
// covered by group_sub_dropcount_test.go for the existing sub_drop
// path) is exercised end-to-end in the 3-agent reliability tests;
// these unit tests pin the bookkeeping invariants in isolation.

package visor

import (
	"testing"
	"time"

	skychatgroup "github.com/skycoin/skywire/cmd/apps/skychat/group"
	"github.com/skycoin/skywire/pkg/cipher"
)

func TestDeliverCountIncrementsOncePerDelivery(t *testing.T) {
	in := newGroupInbox(64)
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()

	if got := in.deliverCount.Load(); got != 0 {
		t.Fatalf("fresh inbox deliverCount: want 0 got %d", got)
	}

	in.deliver("g1", pk1, skychatgroup.Message{Text: "a", TS: time.Now().UTC()})
	in.deliver("g1", pk1, skychatgroup.Message{Text: "b", TS: time.Now().UTC()})
	in.deliver("g2", pk2, skychatgroup.Message{Text: "c", TS: time.Now().UTC()})

	if got := in.deliverCount.Load(); got != 3 {
		t.Fatalf("deliverCount after 3 delivers: want 3 got %d", got)
	}
}

func TestDeliverCountSurvivesRingOverflow(t *testing.T) {
	// Cap=2: the ring drops oldest messages once full, but the
	// deliverCount counter is monotonic and counts every deliver call
	// regardless of whether the ring kept the message.
	in := newGroupInbox(2)
	pk, _ := cipher.GenerateKeyPair()

	for i := 0; i < 5; i++ {
		in.deliver("g", pk, skychatgroup.Message{Text: "x", TS: time.Now().UTC()})
	}
	if got := in.deliverCount.Load(); got != 5 {
		t.Fatalf("deliverCount: want 5 (regardless of ring cap=2 drops), got %d", got)
	}
	if got := len(in.buf); got != 2 {
		t.Fatalf("ring buf len: want 2 (capped), got %d", got)
	}
}

func TestDeliverCountSurvivesSubDrop(t *testing.T) {
	// A subscriber with a 1-deep buffer that doesn't drain. Each
	// delivery enqueues onto the ring AND attempts the fan-out
	// (which drops via select+default on the second message). The
	// deliver counter still ticks for every call; sub_drop ticks
	// for the ones that couldn't enqueue.
	in := newGroupInbox(64)
	sub := in.subscribe(1)
	defer in.unsubscribe(sub)

	pk, _ := cipher.GenerateKeyPair()
	for i := 0; i < 5; i++ {
		in.deliver("g", pk, skychatgroup.Message{Text: "x", TS: time.Now().UTC()})
	}

	if got := in.deliverCount.Load(); got != 5 {
		t.Fatalf("deliverCount: want 5, got %d", got)
	}
	// First message lands in the sub's buffer (capacity 1); the next
	// 4 select+default away. Drop count should be 4.
	if got := sub.dropCount.Load(); got != 4 {
		t.Fatalf("subscriber dropCount: want 4, got %d", got)
	}
	// Aggregate visible via the inbox-wide accessor.
	if got := in.SubDropCount(); got != 4 {
		t.Fatalf("inbox-wide SubDropCount: want 4, got %d", got)
	}
}
