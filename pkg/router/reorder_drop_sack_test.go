// Package router pkg/router/reorder_drop_sack_test.go c2-net-routing
package router

import (
	"sync/atomic"
	"testing"

	"github.com/skycoin/skywire/pkg/logging"
)

// TestReorderDropIsNotSACKed proves the receiver never acknowledges a packet its
// reorder buffer THREW AWAY. deliverData used to call RecordReceived BEFORE
// Insert, so a packet dropped at maxGap was still marked received: the next SACK
// set its bit, the sender purged it from the retransmit buffer, and the no-skip
// frontier then waited forever on a sequence nobody could ever resend — a
// permanent 0 B/s wedge with no event and no counter. Insert now reports the
// drop, RecordReceived runs only on a successful insert, and the drop is
// counted.
func TestReorderDropIsNotSACKed(t *testing.T) {
	log := logging.NewMasterLogger().PackageLogger("reorder-drop-test")
	m := newRouteMux(log, true)
	m.growLegs(1)
	// Tiny reorder window so the cap is reachable in a unit test.
	m.reorderBuf = newReorderBuffer(2)

	p := func(b byte) []byte { return []byte{b} }

	// seq 0 in order, then a gap at seq 1 with seq 2 and 3 buffered behind it —
	// the buffer is now at its cap.
	if delivered, _ := m.deliverData(0, 0, p(0)); len(delivered) != 1 {
		t.Fatalf("seq 0: delivered %d, want 1", len(delivered))
	}
	m.deliverData(0, 2, p(2))
	m.deliverData(0, 3, p(3))
	if got := m.reorderPending(); got != 2 {
		t.Fatalf("pending = %d, want 2 (buffer at cap)", got)
	}

	// seq 4 arrives with the buffer full: it is DROPPED.
	delivered, gap := m.deliverData(0, 4, p(4))
	if len(delivered) != 0 {
		t.Fatalf("seq 4: delivered %d, want 0 (frontier still gapped at seq 1)", len(delivered))
	}
	if !gap {
		t.Error("seq 4 dropped at maxGap but gapDetected = false: no SACK goes out, so the frontier is never re-requested")
	}
	if got := atomic.LoadUint64(&m.reorderDrops); got != 1 {
		t.Errorf("reorderDrops = %d, want 1: the drop must be countable from MuxRecovery", got)
	}
	if got := m.reorderPending(); got != 2 {
		t.Fatalf("pending = %d after the drop, want 2 (the drop must not evict a held packet)", got)
	}

	// The SACK must NOT claim seq 4. lastContiguous is 0, so bitmap bit i is
	// seq 1+i: bit 1 = seq 2, bit 2 = seq 3, bit 3 = seq 4.
	last, words := m.sackTracker.GenerateSACK()
	if last != 0 {
		t.Fatalf("lastContiguous = %d, want 0", last)
	}
	w := word0(words)
	if w&(1<<3) != 0 {
		t.Errorf("SACK bitmap %#b claims seq 4, which the reorder buffer dropped — the sender would purge it and the frontier would wedge forever", w)
	}
	if w&(1<<1) == 0 || w&(1<<2) == 0 {
		t.Errorf("SACK bitmap %#b lost seq 2 / seq 3, which ARE buffered", w)
	}
}

// TestSACKTrackerReceivedCapped proves the out-of-order set is bounded. It is
// pruned only by AdvanceContiguous, which cannot run while the frontier is
// stuck, so without a cap a wedge added one map entry per arriving sequence for
// its whole duration — unbounded memory per stalled transfer. Above the cap the
// sequence is refused (not recorded, so not SACKed), matching Insert's drop.
func TestSACKTrackerReceivedCapped(t *testing.T) {
	st := newSACKTracker()
	st.maxReceived = 8

	// seq 0 in order; seq 2..20 all pile up behind the gap at seq 1.
	st.RecordReceived(0)
	for seq := uint32(2); seq <= 20; seq++ {
		st.RecordReceived(seq)
	}

	st.mu.Lock()
	n := len(st.received)
	st.mu.Unlock()
	if n > 8 {
		t.Fatalf("received set holds %d entries, want <= 8: the out-of-order set is unbounded while the frontier is stuck", n)
	}

	// The refused sequences must not be acknowledged: bit i is seq 1+i.
	last, words := st.GenerateSACK()
	if last != 0 {
		t.Fatalf("lastContiguous = %d, want 0", last)
	}
	w := word0(words)
	if w&(1<<19) != 0 {
		t.Errorf("SACK bitmap %#b claims seq 20, which was refused above the cap", w)
	}

	// The gap still fills normally, and the refused sequences are simply
	// retransmitted later.
	if gap := st.RecordReceived(1); gap {
		t.Error("seq 1 filled the frontier gap but was reported as a new gap")
	}
	if last, _ := st.GenerateSACK(); last < 1 {
		t.Errorf("lastContiguous = %d after the gap filled, want >= 1", last)
	}
}
