package router

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/skycoin/skywire/pkg/routing"
)

func TestSACKTracker_InOrder(t *testing.T) {
	st := newSACKTracker()
	assert.False(t, st.RecordReceived(0))
	assert.False(t, st.RecordReceived(1))
	assert.False(t, st.RecordReceived(2))

	last, bm := st.GenerateSACK()
	assert.Equal(t, uint32(2), last)
	assert.Equal(t, uint64(0), bm)
}

func TestSACKTracker_GapDetection(t *testing.T) {
	st := newSACKTracker()
	st.RecordReceived(0) // in order
	gap := st.RecordReceived(2)
	assert.True(t, gap) // gap at seq 1

	gap = st.RecordReceived(3)
	assert.True(t, gap) // still gap at seq 1

	last, bm := st.GenerateSACK()
	assert.Equal(t, uint32(0), last) // last contiguous is 0
	// bit 0 = seq 1 (missing) = 0
	// bit 1 = seq 2 (received) = 1
	// bit 2 = seq 3 (received) = 1
	assert.Equal(t, uint64(0b110), bm)
}

func TestSACKTracker_GapFill(t *testing.T) {
	st := newSACKTracker()
	st.RecordReceived(0)
	st.RecordReceived(2)
	st.RecordReceived(3)

	// Fill the gap
	gap := st.RecordReceived(1)
	assert.False(t, gap) // seq 1 fills gap, advances contiguous to 3

	last, bm := st.GenerateSACK()
	assert.Equal(t, uint32(3), last)
	assert.Equal(t, uint64(0), bm)
}

func TestSACKTracker_AdvanceContiguous(t *testing.T) {
	st := newSACKTracker()
	st.RecordReceived(0)
	st.RecordReceived(2)
	st.RecordReceived(3)

	// Simulate reorder buffer delivering up to seq 4 (nextSeq=4)
	st.AdvanceContiguous(4)
	last, bm := st.GenerateSACK()
	assert.Equal(t, uint32(3), last)
	assert.Equal(t, uint64(0), bm)
}

func TestRetxBuffer_StoreAndPurge(t *testing.T) {
	rb := newRetxBuffer(128)
	for i := uint32(0); i < 10; i++ {
		assert.True(t, rb.Store(i, []byte{byte(i)}))
	}
	assert.Equal(t, 10, rb.Len())

	// SACK: lastContiguous=5, all above received
	retx := rb.ProcessSACK(5, 0xF) // bits 0-3 set = seqs 6,7,8,9 received
	assert.Empty(t, retx)
	assert.Equal(t, 0, rb.Len())
}

func TestRetxBuffer_RetransmitList(t *testing.T) {
	rb := newRetxBuffer(128)
	for i := uint32(0); i < 8; i++ {
		rb.Store(i, []byte{byte(i)})
	}

	// SACK: lastContiguous=2, bitmap=0b1010 (seq 3 missing, 4 received, 5 missing, 6 received)
	retx := rb.ProcessSACK(2, 0b1010)
	assert.Contains(t, retx, uint32(3))
	assert.Contains(t, retx, uint32(5))
	assert.NotContains(t, retx, uint32(4))
	assert.NotContains(t, retx, uint32(6))
}

func TestRetxBuffer_Capacity(t *testing.T) {
	rb := newRetxBuffer(3)
	assert.True(t, rb.Store(0, []byte("a")))
	assert.True(t, rb.Store(1, []byte("b")))
	assert.True(t, rb.Store(2, []byte("c")))
	assert.False(t, rb.Store(3, []byte("d"))) // full
}

func TestRetxBuffer_Get(t *testing.T) {
	rb := newRetxBuffer(128)
	rb.Store(42, []byte("hello"))
	assert.Equal(t, []byte("hello"), rb.Get(42))
	assert.Nil(t, rb.Get(99))
}

func TestSACKPacket_RoundTrip(t *testing.T) {
	p := routing.MakeSACKPacket(7, 42, 0xDEADBEEF)
	assert.Equal(t, routing.SACKPacket, p.Type())
	assert.Equal(t, routing.RouteID(7), p.RouteID())
	assert.Equal(t, uint32(42), p.SACKLastContiguousSeq())
	assert.Equal(t, uint64(0xDEADBEEF), p.SACKBitmap())
}
