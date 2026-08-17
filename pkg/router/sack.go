// Package router pkg/router/sack.go c2-net-routing
package router

import (
	"sync"
	"time"
)

// retxMinAge is how long an un-acknowledged sequence must have been
// outstanding before a SACK gap will retransmit it. The mux legs ride
// reliable, ordered transports, so a sequence missing from the receiver's
// contiguous run is almost always just IN FLIGHT on a slower leg (latency
// skew), not lost. Retransmitting it on sight is spurious and self-amplifies:
// each early retx arrives out of order, provoking another SACK, provoking more
// retx (the observed ~7x traffic storm that wedged mux>1). Holding off until a
// sequence is overdue by more than any realistic inter-leg skew means a merely
// reordered packet arrives and is acked (purged) first, and only a genuinely
// lost one — a dead leg, which liveness prunes — is ever retransmitted.
const retxMinAge = 750 * time.Millisecond

// sackTracker tracks received sequence numbers on the receiver side and
// generates SACK feedback (last contiguous seq + 64-bit bitmap).
type sackTracker struct {
	mu             sync.Mutex
	lastContiguous uint32
	received       map[uint32]bool
}

func newSACKTracker() *sackTracker {
	return &sackTracker{
		received: make(map[uint32]bool),
	}
}

// RecordReceived records that seq has been received. Returns true if this
// created an out-of-order gap (caller should trigger immediate SACK).
func (st *sackTracker) RecordReceived(seq uint32) (gapDetected bool) {
	st.mu.Lock()
	defer st.mu.Unlock()

	if seq <= st.lastContiguous {
		return false // duplicate or old
	}

	if seq == st.lastContiguous+1 {
		// In-order: advance contiguous pointer
		st.lastContiguous = seq
		// Drain any consecutive buffered seqs
		for st.received[st.lastContiguous+1] {
			delete(st.received, st.lastContiguous+1)
			st.lastContiguous++
		}
		return false
	}

	// Out-of-order: buffer it
	st.received[seq] = true
	return true
}

// AdvanceContiguous advances lastContiguous to match the reorder buffer's
// delivery state. This synchronizes the SACK tracker with actual delivery.
func (st *sackTracker) AdvanceContiguous(nextSeq uint32) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if nextSeq > 0 && nextSeq-1 > st.lastContiguous {
		st.lastContiguous = nextSeq - 1
		// Clean up any received entries that are now contiguous
		for seq := range st.received {
			if seq <= st.lastContiguous {
				delete(st.received, seq)
			}
		}
	}
}

// GenerateSACK returns the current SACK state: last contiguous sequence
// number and a 64-bit bitmap where bit i == 1 means
// (lastContiguous + 1 + i) has been received.
func (st *sackTracker) GenerateSACK() (lastContiguous uint32, bitmap uint64) {
	st.mu.Lock()
	defer st.mu.Unlock()

	lastContiguous = st.lastContiguous
	for i := uint32(0); i < 64; i++ {
		if st.received[lastContiguous+1+i] {
			bitmap |= 1 << i
		}
	}
	return
}

// retxEntry holds a sent packet awaiting acknowledgment.
type retxEntry struct {
	seq    uint32
	data   []byte
	sentAt time.Time
}

// retxBuffer is a bounded buffer of unacknowledged sent packets for
// retransmission on SACK-detected loss.
type retxBuffer struct {
	mu       sync.Mutex
	entries  map[uint32]*retxEntry
	capacity int
}

func newRetxBuffer(capacity int) *retxBuffer {
	return &retxBuffer{
		entries:  make(map[uint32]*retxEntry),
		capacity: capacity,
	}
}

// Store saves a sent packet for potential retransmission.
// Returns false if the buffer is full (backpressure signal).
func (rb *retxBuffer) Store(seq uint32, data []byte) bool {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if len(rb.entries) >= rb.capacity {
		return false
	}

	cp := make([]byte, len(data))
	copy(cp, data)
	rb.entries[seq] = &retxEntry{
		seq:    seq,
		data:   cp,
		sentAt: time.Now(),
	}
	return true
}

// ProcessSACK processes a received SACK and returns sequence numbers that
// need retransmission (gaps in the bitmap where we have stored data).
// Also purges acknowledged entries.
func (rb *retxBuffer) ProcessSACK(lastContiguous uint32, bitmap uint64) []uint32 {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	// Purge all entries up to and including lastContiguous
	for seq := range rb.entries {
		if seq <= lastContiguous {
			delete(rb.entries, seq)
		}
	}

	// Check bitmap for acknowledged and missing packets
	var retransmit []uint32
	for i := uint32(0); i < 64; i++ {
		checkSeq := lastContiguous + 1 + i
		if bitmap&(1<<i) != 0 {
			// Bit is set: packet received, purge from buffer
			delete(rb.entries, checkSeq)
		} else {
			// Bit is not set: packet missing. Retransmit only if it is
			// genuinely overdue — a sequence still within retxMinAge of when it
			// was sent is presumed in flight on a slower leg (mux latency skew),
			// not lost, so retransmitting it would be spurious.
			if e, ok := rb.entries[checkSeq]; ok && time.Since(e.sentAt) >= retxMinAge {
				retransmit = append(retransmit, checkSeq)
			}
		}
	}

	return retransmit
}

// Get returns the stored payload for a given sequence number, or nil.
func (rb *retxBuffer) Get(seq uint32) []byte {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if entry, ok := rb.entries[seq]; ok {
		return entry.data
	}
	return nil
}

// Len returns current buffer occupancy.
func (rb *retxBuffer) Len() int {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return len(rb.entries)
}
