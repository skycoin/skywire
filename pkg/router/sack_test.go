package router

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/skycoin/skywire/pkg/routing"
)

// word0 is a tiny helper: the first bitmap word, or 0 when the SACK carried no
// words (fully caught up).
func word0(words []uint64) uint64 {
	if len(words) == 0 {
		return 0
	}
	return words[0]
}

func TestSACKTracker_InOrder(t *testing.T) {
	st := newSACKTracker()
	assert.False(t, st.RecordReceived(0))
	assert.False(t, st.RecordReceived(1))
	assert.False(t, st.RecordReceived(2))

	last, words := st.GenerateSACK()
	assert.Equal(t, uint32(2), last)
	assert.Equal(t, uint64(0), word0(words)) // fully contiguous: no bits above
}

func TestSACKTracker_GapDetection(t *testing.T) {
	st := newSACKTracker()
	st.RecordReceived(0) // in order
	gap := st.RecordReceived(2)
	assert.True(t, gap) // gap at seq 1

	gap = st.RecordReceived(3)
	assert.True(t, gap) // still gap at seq 1

	last, words := st.GenerateSACK()
	assert.Equal(t, uint32(0), last) // last contiguous is 0
	// bit 0 = seq 1 (missing) = 0
	// bit 1 = seq 2 (received) = 1
	// bit 2 = seq 3 (received) = 1
	assert.Equal(t, uint64(0b110), word0(words))
}

func TestSACKTracker_GapFill(t *testing.T) {
	st := newSACKTracker()
	st.RecordReceived(0)
	st.RecordReceived(2)
	st.RecordReceived(3)

	// Fill the gap
	gap := st.RecordReceived(1)
	assert.False(t, gap) // seq 1 fills gap, advances contiguous to 3

	last, words := st.GenerateSACK()
	assert.Equal(t, uint32(3), last)
	assert.Equal(t, uint64(0), word0(words))
}

func TestSACKTracker_AdvanceContiguous(t *testing.T) {
	st := newSACKTracker()
	st.RecordReceived(0)
	st.RecordReceived(2)
	st.RecordReceived(3)

	// Simulate reorder buffer delivering up to seq 4 (nextSeq=4)
	st.AdvanceContiguous(4)
	last, words := st.GenerateSACK()
	assert.Equal(t, uint32(3), last)
	assert.Equal(t, uint64(0), word0(words))
}

// TestSACKTracker_FullWindowBitmap proves the SACK covers the whole outstanding
// window, not just the first 64 sequences: with a gap at seq 1 and everything up
// to seq 200 received, the bitmap must span >64 sequences (multiple words) and
// mark those high sequences received. This is what lets the sender purge them.
func TestSACKTracker_FullWindowBitmap(t *testing.T) {
	st := newSACKTracker()
	st.RecordReceived(0)
	// seq 1 missing (frontier gap); receive 2..200
	for seq := uint32(2); seq <= 200; seq++ {
		st.RecordReceived(seq)
	}
	last, words := st.GenerateSACK()
	assert.Equal(t, uint32(0), last)
	assert.GreaterOrEqual(t, len(words), 4, "200 outstanding seqs need >=4 words (>64)")
	// seq 1 (bit 0 of word 0) missing
	assert.Equal(t, uint64(0), words[0]&1)
	// seq 130 received: within word 2, bit (129-128)=1
	assert.NotEqual(t, uint64(0), words[2]&(1<<1))
}

func TestRetxBuffer_StoreAndPurge(t *testing.T) {
	rb := newRetxBuffer(128)
	for i := uint32(0); i < 10; i++ {
		assert.True(t, rb.Store(i, []byte{byte(i)}))
	}
	assert.Equal(t, 10, rb.Len())

	// SACK: lastContiguous=5, bits 0-3 set = seqs 6,7,8,9 received
	retx := rb.ProcessSACK(5, []uint64{0xF})
	assert.Empty(t, retx)
	assert.Equal(t, 0, rb.Len())
}

func TestRetxBuffer_RetransmitList(t *testing.T) {
	rb := newRetxBuffer(128)
	for i := uint32(0); i < 8; i++ {
		rb.Store(i, []byte{byte(i)})
	}

	// Age the stored entries past retxMinAge so ProcessSACK will list missing
	// sequences (a younger gap is presumed in flight on a slower mux leg).
	for _, e := range rb.entries {
		e.sentAt = e.sentAt.Add(-2 * retxMinAge)
	}

	// SACK: lastContiguous=2, bitmap=0b1010 (seq 3 missing, 4 received, 5 missing, 6 received)
	retx := rb.ProcessSACK(2, []uint64{0b1010})
	assert.Contains(t, retx, uint32(3))
	assert.Contains(t, retx, uint32(5))
	assert.NotContains(t, retx, uint32(4))
	assert.NotContains(t, retx, uint32(6))
}

// TestRetxBuffer_CapacityEvictsLowest verifies the full-buffer path now evicts
// the lowest (oldest, already force-flushed on the receiver) sequence and keeps
// the newest, instead of silently refusing to store the newest packet.
func TestRetxBuffer_CapacityEvictsLowest(t *testing.T) {
	rb := newRetxBuffer(3)
	assert.True(t, rb.Store(0, []byte("a")))
	assert.True(t, rb.Store(1, []byte("b")))
	assert.True(t, rb.Store(2, []byte("c")))
	assert.True(t, rb.Store(3, []byte("d"))) // full -> evict lowest (0), store 3
	assert.Equal(t, 3, rb.Len())
	assert.Nil(t, rb.Get(0), "lowest seq evicted")
	assert.Equal(t, []byte("d"), rb.Get(3), "newest seq retained")
}

func TestRetxBuffer_Get(t *testing.T) {
	rb := newRetxBuffer(128)
	rb.Store(42, []byte("hello"))
	assert.Equal(t, []byte("hello"), rb.Get(42))
	assert.Nil(t, rb.Get(99))
}

// TestRetxBuffer_NoFillBehindPersistentGap is the #86 regression: a frontier gap
// at seq 1 persists while the sender keeps sending. The full-window SACK must
// purge every received-above-the-gap sequence so the buffer holds only the hole
// plus the not-yet-reported tail — never the whole unackable backlog that used
// to fill it and wedge the stream. (The old 64-bit SACK could only ack seqs
// 1..64, leaving 65.. stuck in the buffer forever.)
func TestRetxBuffer_NoFillBehindPersistentGap(t *testing.T) {
	const sent = 3000
	rb := newRetxBuffer(4096) // large so this isolates purge behavior, not eviction
	for seq := uint32(1); seq <= sent; seq++ {
		rb.Store(seq, []byte{byte(seq)})
	}

	// Receiver got 2..3000 but not seq 1 -> lastContiguous stuck at 0.
	st := newSACKTracker()
	st.RecordReceived(0)
	for seq := uint32(2); seq <= sent; seq++ {
		st.RecordReceived(seq)
	}
	last, words := st.GenerateSACK()
	assert.Equal(t, uint32(0), last)

	rb.ProcessSACK(last, words)

	// seq 1 (the hole) retained for retransmit.
	assert.NotNil(t, rb.Get(1), "the actual gap must stay retransmittable")
	// Everything the SACK could cover (up to 32 words = seqs 1..2048) that was
	// received is purged.
	assert.Nil(t, rb.Get(2), "received seq above the gap must be purged")
	assert.Nil(t, rb.Get(2048), "received seq at window edge must be purged")
	// Buffer no longer holds the acknowledged backlog: only the hole + the tail
	// above the 2048-seq window remain.
	assert.LessOrEqual(t, rb.Len(), 1+(sent-2048)+8,
		"buffer must not retain the acknowledged received backlog")
}

func TestSACKPacket_RoundTrip(t *testing.T) {
	words := []uint64{0xDEADBEEF, 0x0, 0xF00D}
	p := routing.MakeSACKPacket(7, 42, words)
	assert.Equal(t, routing.SACKPacket, p.Type())
	assert.Equal(t, routing.RouteID(7), p.RouteID())
	assert.Equal(t, uint32(42), p.SACKLastContiguousSeq())
	assert.Equal(t, words, p.SACKWords())
}

func TestSACKPacket_EmptyAndTrim(t *testing.T) {
	// No words -> minimal payload, SACKWords returns empty.
	p := routing.MakeSACKPacket(1, 5, nil)
	assert.Equal(t, uint32(5), p.SACKLastContiguousSeq())
	assert.Empty(t, p.SACKWords())

	// Trailing zero words are trimmed on the wire but the meaningful prefix
	// round-trips.
	p2 := routing.MakeSACKPacket(1, 5, []uint64{0b101, 0, 0})
	assert.Equal(t, []uint64{0b101}, p2.SACKWords())
}
