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

// sackMaxWords bounds the generated bitmap to the reorder window (32 words *
// 64 = 2048 sequences). Mirrors routing.SACKMaxWords; kept local to avoid a
// routing import here.
const sackMaxWords = 32

// sackTracker tracks received sequence numbers on the receiver side and
// generates SACK feedback: last contiguous seq + a variable-length bitmap that
// covers the whole outstanding window up to the highest received sequence.
// Acking the full window (not just the first 64) is what lets the sender purge
// received-but-above-the-gap sequences from its retx buffer, so a persistent
// frontier gap can no longer fill that buffer and wedge the stream (#86).
type sackTracker struct {
	mu             sync.Mutex
	lastContiguous uint32
	highest        uint32 // highest sequence ever recorded (0 = none yet)
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

	if seq > st.highest {
		st.highest = seq
	}

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

// GenerateSACK returns the current SACK state: the last contiguous sequence
// number and a variable-length bitmap covering the whole outstanding window
// [lastContiguous+1, highest]. Word w, bit i set means
// (lastContiguous + 1 + w*64 + i) has been received. The window is capped at
// sackMaxWords*64 sequences (the reorder-buffer bound); trailing empty words
// are trimmed by the encoder.
func (st *sackTracker) GenerateSACK() (lastContiguous uint32, words []uint64) {
	st.mu.Lock()
	defer st.mu.Unlock()

	lastContiguous = st.lastContiguous
	if st.highest <= lastContiguous {
		return lastContiguous, nil
	}
	span := st.highest - lastContiguous // number of sequences above the contiguous point
	nWords := int((span + 63) / 64)
	if nWords > sackMaxWords {
		nWords = sackMaxWords
	}
	words = make([]uint64, nWords)
	for w := 0; w < nWords; w++ {
		for i := uint32(0); i < 64; i++ {
			seq := lastContiguous + 1 + uint32(w)*64 + i
			if st.received[seq] {
				words[w] |= 1 << i
			}
		}
	}
	return lastContiguous, words
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

// Store saves a sent packet for potential retransmission. When the buffer is
// full it evicts the LOWEST outstanding sequence to make room, rather than
// refusing the newest: a full buffer means a frontier gap has stayed open for a
// whole reorder window, at which point the receiver has already force-flushed
// past its lowest held sequence, so that entry can never be usefully
// retransmitted — dropping it and keeping the newest packet (which still can be)
// is strictly safer than the reverse. Always returns true (the packet is
// always stored); the bool is retained for callers/tests.
//
// With the full-window SACK (GenerateSACK/ProcessSACK), the sender purges every
// received sequence above the gap each SACK, so under normal operation the
// buffer holds only genuine holes plus in-flight sequences and never reaches
// capacity — this eviction path is a backstop, not the steady state.
func (rb *retxBuffer) Store(seq uint32, data []byte) bool {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if len(rb.entries) >= rb.capacity {
		var lowest uint32
		first := true
		for s := range rb.entries {
			if first || s < lowest {
				lowest, first = s, false
			}
		}
		if !first {
			delete(rb.entries, lowest)
		}
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

// ProcessSACK processes a received SACK (last contiguous seq + full-window
// received bitmap) and returns sequence numbers that need retransmission (holes
// in the bitmap where we still hold data). It purges every acknowledged entry,
// including received sequences ABOVE a persistent frontier gap — that whole-
// window purge is what keeps the buffer from filling behind a stuck gap (#86).
func (rb *retxBuffer) ProcessSACK(lastContiguous uint32, words []uint64) []uint32 {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	// Purge all entries up to and including lastContiguous.
	for seq := range rb.entries {
		if seq <= lastContiguous {
			delete(rb.entries, seq)
		}
	}

	// Walk the full received bitmap: set bit -> received, purge; unset bit ->
	// still missing, retransmit if genuinely overdue (a sequence within
	// retxMinAge of send is presumed in flight on a slower leg, not lost, so
	// retransmitting it would be the spurious self-amplifying storm retxMinAge
	// exists to prevent). Sequences ABOVE the covered window are left in place:
	// they are the still-in-flight tail the receiver has not reported yet.
	var retransmit []uint32
	for w, word := range words {
		base := lastContiguous + 1 + uint32(w)*64
		for i := uint32(0); i < 64; i++ {
			checkSeq := base + i
			if word&(1<<i) != 0 {
				delete(rb.entries, checkSeq)
			} else if e, ok := rb.entries[checkSeq]; ok && time.Since(e.sentAt) >= retxMinAge {
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
