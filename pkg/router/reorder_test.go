package router

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestReorderBuffer_InOrder(t *testing.T) {
	rb := newReorderBuffer(64)
	d := rb.Insert(0, []byte("a"))
	assert.Equal(t, [][]byte{[]byte("a")}, d)
	d = rb.Insert(1, []byte("b"))
	assert.Equal(t, [][]byte{[]byte("b")}, d)
	d = rb.Insert(2, []byte("c"))
	assert.Equal(t, [][]byte{[]byte("c")}, d)
	assert.Equal(t, 0, rb.Pending())
}

func TestReorderBuffer_OutOfOrder(t *testing.T) {
	rb := newReorderBuffer(64)
	// Packet 1 arrives before packet 0
	d := rb.Insert(1, []byte("b"))
	assert.Nil(t, d)
	assert.Equal(t, 1, rb.Pending())

	// Packet 0 arrives — should deliver both in order
	d = rb.Insert(0, []byte("a"))
	assert.Equal(t, [][]byte{[]byte("a"), []byte("b")}, d)
	assert.Equal(t, 0, rb.Pending())
}

func TestReorderBuffer_GapThenFill(t *testing.T) {
	rb := newReorderBuffer(64)
	// Packets arrive: 0, 2, 3, 1
	d := rb.Insert(0, []byte("a"))
	assert.Equal(t, [][]byte{[]byte("a")}, d)

	d = rb.Insert(2, []byte("c"))
	assert.Nil(t, d)

	d = rb.Insert(3, []byte("d"))
	assert.Nil(t, d)

	// Filling the gap delivers 1, 2, 3
	d = rb.Insert(1, []byte("b"))
	assert.Equal(t, [][]byte{[]byte("b"), []byte("c"), []byte("d")}, d)
}

func TestReorderBuffer_Duplicate(t *testing.T) {
	rb := newReorderBuffer(64)
	rb.Insert(0, []byte("a"))
	// Duplicate of seq 0
	d := rb.Insert(0, []byte("a_dup"))
	assert.Nil(t, d)
}

func TestReorderBuffer_MaxGapDropsExcessNeverSkips(t *testing.T) {
	// At the OOM backstop (maxGap) the buffer DROPS excess out-of-order packets —
	// it never skips the frontier gap (skipping would corrupt the reliable stream).
	rb := newReorderBuffer(3)
	// Gap at seq 0; buffer 1,2,3 fills to maxGap — none delivered (frontier held).
	assert.Nil(t, rb.Insert(1, []byte("b")))
	assert.Nil(t, rb.Insert(2, []byte("c")))
	assert.Nil(t, rb.Insert(3, []byte("d")))
	assert.Equal(t, 3, rb.Pending())
	// At capacity, a further out-of-order packet is DROPPED (not buffered), and the
	// gap is still NOT skipped — seq 4 will be re-requested via SACK.
	assert.Nil(t, rb.Insert(4, []byte("e")))
	assert.Equal(t, 3, rb.Pending(), "buffer bounded at maxGap; excess dropped, gap never skipped")
	// The missing seq 0 finally arrives -> the frontier drains IN ORDER (0..3).
	d := rb.Insert(0, []byte("a"))
	assert.Equal(t, [][]byte{[]byte("a"), []byte("b"), []byte("c"), []byte("d")}, d)
	assert.Equal(t, 0, rb.Pending())
}

// TestReorderLosslessBeyondOldMaxGap proves the reorder buffer no longer skips a
// gap the moment it fills past the old maxGap=64: with reliable leg transports a
// gap is latency skew, so the buffer must HOLD it (any skip corrupts the noise
// stream riding the mux — the mux>1 wedge). Here 499 packets pile up behind a
// single missing sequence; none may be delivered early, and when the missing
// one finally arrives the whole run delivers in order with no hole.
func TestReorderLosslessBeyondOldMaxGap(t *testing.T) {
	rb := newReorderBuffer(reorderWindow) // 2048

	if got := rb.Insert(0, []byte{0}); len(got) != 1 {
		t.Fatalf("seq0 in-order: delivered %d, want 1", len(got))
	}
	// Buffer seq 2..500 behind the gap at seq 1 — far past the old maxGap=64.
	for s := uint32(2); s <= 500; s++ {
		if got := rb.Insert(s, []byte{byte(s)}); got != nil {
			t.Fatalf("seq%d delivered early (len=%d): buffer skipped a still-open gap (corruption)", s, len(got))
		}
	}
	if rb.Pending() != 499 {
		t.Fatalf("pending=%d, want 499: buffer dropped packets instead of holding the gap", rb.Pending())
	}

	// The missing seq 1 finally arrives — 1..500 must now deliver contiguously.
	got := rb.Insert(1, []byte{1})
	if len(got) != 500 {
		t.Fatalf("after gap fill: delivered %d, want 500 contiguous", len(got))
	}
	for i, d := range got {
		if want := byte(uint32(i) + 1); d[0] != want {
			t.Fatalf("delivered[%d]=%d, want %d: out of order", i, d[0], want)
		}
	}
	if rb.Pending() != 0 {
		t.Fatalf("pending=%d after full drain, want 0", rb.Pending())
	}
}

// TestReorderNeverSkipsAcrossTimeout proves a frontier gap is NEVER delivered
// past, no matter how long it stays open. The mux carries a noise-encrypted
// (stateful AEAD) byte stream, and skipping a sequence permanently desyncs the
// cipher — a hard 0-byte wedge, not "lossy but moving". So a stalled leg's gap
// is HELD until the missing seq arrives (the sender's SACK retransmit refills it
// contiguously from a live leg), which is what keeps mux>1 usable under a
// black-holing leg. This is the inverse of the old time-based skip.
func TestReorderNeverSkipsAcrossTimeout(t *testing.T) {
	old := reorderTimeout
	reorderTimeout = 100 * time.Millisecond
	defer func() { reorderTimeout = old }()

	rb := newReorderBuffer(reorderWindow)

	if got := rb.Insert(0, []byte{0}); len(got) != 1 {
		t.Fatalf("seq0: delivered %d, want 1", len(got))
	}
	// Open a gap at seq 1 and buffer some successors.
	rb.Insert(2, []byte{2})
	rb.Insert(3, []byte{3})

	// Well past the timeout: further out-of-order packets must STILL NOT release
	// the gap — holding, not skipping, is the only noise-safe behavior.
	time.Sleep(150 * time.Millisecond)
	if got := rb.Insert(4, []byte{4}); got != nil {
		t.Fatalf("gap released across timeout (len=%d): skipping seq corrupts the noise stream", len(got))
	}
	if rb.NextSeq() != 1 {
		t.Fatalf("nextSeq=%d advanced past the held gap, want 1", rb.NextSeq())
	}

	// The missing seq 1 finally arrives (SACK retransmit over a live leg): now the
	// buffer drains in order, contiguously, cipher intact.
	got := rb.Insert(1, []byte{1})
	if len(got) != 4 {
		t.Fatalf("after refill: delivered %d, want 4 (seq 1,2,3,4 in order)", len(got))
	}
	for i, d := range got {
		if want := byte(i + 1); d[0] != want {
			t.Fatalf("delivered[%d]=%d, want %d: not in order", i, d[0], want)
		}
	}
	if rb.NextSeq() != 5 {
		t.Fatalf("nextSeq=%d after drain, want 5", rb.NextSeq())
	}
	if rb.Pending() != 0 {
		t.Fatalf("pending=%d after full drain, want 0", rb.Pending())
	}
}

// TestReorderBuffer_NeverSkipsGap pins the reliability contract: the reorder
// buffer NEVER delivers past a frontier gap on a time basis, no matter how long
// the gap has been open. The RouteGroup is a reliable ordered stream (TCP/TLS
// rides it), so skipping a sequence would leave a hole that corrupts the upper
// protocol (the observed "bad record mac"). The gap is only ever closed by the
// missing sequence actually arriving — recovery comes from SACK retransmit + the
// leg-dataprogress prune, not from skipping.
func TestReorderBuffer_NeverSkipsGap(t *testing.T) {
	orig := reorderTimeout
	reorderTimeout = 20 * time.Millisecond
	defer func() { reorderTimeout = orig }()

	rb := newReorderBuffer(64)
	assert.Equal(t, [][]byte{[]byte("a")}, rb.Insert(0, []byte("a"))) // seq 0 in order
	_ = rb.Insert(2, []byte("c"))                                     // gap at seq 1, buffered
	_ = rb.Insert(3, []byte("d"))                                     // still buffered behind the gap
	time.Sleep(40 * time.Millisecond)                                 // well past reorderTimeout
	assert.Equal(t, 2, rb.Pending(), "gap must stay held past reorderTimeout — never skip")
	assert.True(t, rb.GapAge() > reorderTimeout, "gap is aged but still held, not released")
	// The missing seq 1 finally arrives → in-order delivery of 1,2,3.
	assert.Equal(t, [][]byte{[]byte("b"), []byte("c"), []byte("d")}, rb.Insert(1, []byte("b")))
	assert.Equal(t, 0, rb.Pending(), "buffer drained in order once the gap filled")
}
