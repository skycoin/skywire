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

func TestReorderBuffer_ForceFlush(t *testing.T) {
	rb := newReorderBuffer(3)
	// Skip seq 0, send 1, 2, 3 — triggers flush at maxGap=3
	rb.Insert(1, []byte("b"))
	rb.Insert(2, []byte("c"))
	d := rb.Insert(3, []byte("d"))
	// Should flush all buffered in order
	assert.Equal(t, 3, len(d))
	assert.Equal(t, []byte("b"), d[0])
	assert.Equal(t, []byte("c"), d[1])
	assert.Equal(t, []byte("d"), d[2])
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

// TestReorderTimeBasedRelease proves a gap that stays open longer than
// reorderTimeout is released (delivered past the missing sequence) so a dead leg
// degrades to lossy-but-moving instead of stalling the stream at 0 bytes.
func TestReorderTimeBasedRelease(t *testing.T) {
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

	// Before the timeout: another out-of-order packet must NOT release the gap.
	if got := rb.Insert(4, []byte{4}); got != nil {
		t.Fatalf("gap released before timeout (len=%d): normal skew would be corrupted", len(got))
	}

	// After the timeout: the next out-of-order packet releases the gap, skipping
	// the missing seq 1 so the stream keeps moving.
	time.Sleep(150 * time.Millisecond)
	got := rb.Insert(5, []byte{5})
	if len(got) == 0 {
		t.Fatalf("gap not released after timeout: dead leg would hard-stall the stream at 0 bytes")
	}
	if rb.NextSeq() <= 1 {
		t.Fatalf("nextSeq=%d did not advance past the skipped gap", rb.NextSeq())
	}
	if rb.Pending() != 0 {
		t.Fatalf("pending=%d after release, want 0", rb.Pending())
	}
}
