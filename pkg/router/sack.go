// Package router pkg/router/sack.go c2-net-routing
package router

import (
	"math"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// retxMinAge is the FALLBACK reorder-tolerant retransmit age, used only when the
// caller supplies no RTT-derived threshold (a zero threshold to ProcessSACK).
// The live path now passes routeMux.rackThreshold (RFC 8985 RACK), which derives
// the age from the measured per-leg RTTs instead of this fixed constant — so a
// fast path recovers loss in tens of ms rather than always waiting 750ms, while a
// slow path can wait longer. The rationale is unchanged: the mux legs ride
// reliable, ordered transports, so a sequence missing from the receiver's
// contiguous run is almost always just IN FLIGHT on a slower leg (latency skew),
// not lost; retransmitting on sight is spurious and self-amplifies (the observed
// ~7x traffic storm that wedged mux>1). Holding off until a sequence is overdue
// by more than the realistic inter-leg skew — which the RTT-derived threshold
// measures directly — means a merely reordered packet is acked (purged) first.
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

	// DSACK (duplicate-SACK, RFC 8985 §7.2): a sequence received AGAIN. On the
	// mux's reliable, ordered legs a duplicate is almost always the sender's own
	// spurious retransmit, so we surface the most recent one to the sender in the
	// next SACK; the sender widens its RACK reorder window in response. dsackSeq
	// holds it, dsackPending gates a single report (cleared once taken).
	dsackSeq     uint32
	dsackPending bool
}

func newSACKTracker() *sackTracker {
	return &sackTracker{
		received: make(map[uint32]bool),
	}
}

// alreadyBuffered reports whether seq is already held out-of-order in the
// received set (a buffered duplicate). Read-only; the contiguous/delivered check
// is done separately against the reorder buffer's NextSeq (which, unlike
// lastContiguous, has no seq-0-vs-nothing ambiguity). Used to credit a leg's
// UNIQUE payload only on a seq's FIRST arrival.
func (st *sackTracker) alreadyBuffered(seq uint32) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.received[seq]
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
		// Already delivered in order, now arriving again → a duplicate. Report it
		// as a DSACK so the sender learns its retransmit of this seq was spurious.
		st.dsackSeq = seq
		st.dsackPending = true
		return false
	}

	if st.received[seq] {
		// Duplicate of a still-buffered out-of-order seq → same DSACK signal. Not
		// a new gap, so report no gap (the original already opened one).
		st.dsackSeq = seq
		st.dsackPending = true
		return false
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

// takeDSACK returns a pending DSACK sequence (a duplicate the receiver saw) and
// clears the pending flag, so each duplicate is reported to the sender at most
// once. Returns (0, false) when no duplicate is pending.
func (st *sackTracker) takeDSACK() (seq uint32, ok bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.dsackPending {
		return 0, false
	}
	st.dsackPending = false
	return st.dsackSeq, true
}

// retxEntry holds a sent packet awaiting acknowledgment.
type retxEntry struct {
	seq    uint32
	data   []byte
	sentAt time.Time
	// tpID is the TRANSPORT UUID of the leg this sequence was LAST sent on
	// (uuid.Nil = unknown). Recorded at Store time and re-tagged on every
	// retransmit, so the demote-time flush can resend only the sequences
	// actually stranded on the demoted leg(s) instead of the whole in-flight
	// window (measured live: a whole-window flush duplicated 33.8MB against
	// 14.7MB of payload on one leg of a 20MB transfer). The tag is the
	// transport identity, NOT the leg index: indices shift whenever
	// pruneDeadTransports compacts tps[] after a leg drop, and an index tag
	// went stale exactly then — the flush missed genuinely stranded sequences
	// and the receiver's no-skip frontier wedged into a retransmit storm.
	tpID uuid.UUID
	// lastTxAt is when this sequence was last handed out for retransmission
	// (zero until the first retx). retxCount is how many times it has been.
	// Together they gate re-retransmission: without them every SACK arriving
	// within one bloated RTT re-selects the same overdue hole (SACKs come every
	// ~25ms, a queued-up path can hold a seq for many seconds), and the same
	// packet is resent hundreds of times — the observed 20×+ wire amplification
	// of a retransmit storm.
	lastTxAt  time.Time
	retxCount uint8
}

// retxBackoffMaxShift caps the per-entry exponential backoff between successive
// retransmissions of the SAME sequence at threshold×2^this. Doubling with a cap
// bounds the worst-case waste per sequence at ~4 copies even when the loss
// threshold underestimates the true (bufferbloated) RTT, while a genuinely lost
// retransmit is still retried on a TCP-like doubling schedule.
const retxBackoffMaxShift = 3

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
func (rb *retxBuffer) Store(seq uint32, data []byte, tpID uuid.UUID) bool {
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
		tpID:   tpID,
	}
	return true
}

// SetTpID re-tags seq's last-send transport (a retransmit moved it). No-op for
// a seq no longer held.
func (rb *retxBuffer) SetTpID(seq uint32, tpID uuid.UUID) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	if e, ok := rb.entries[seq]; ok {
		e.tpID = tpID
	}
}

// HeldSeqsOnTps returns the held sequences whose last send rode one of the
// given transports, sorted ascending. Entries with an unknown transport
// (uuid.Nil) are included conservatively — better a redundant resend than a
// stranded gap. Used by the demote-time flush to rescue only what the demoted
// leg(s) actually strand; keyed by transport UUID because leg INDICES shift on
// slice compaction and a stale index tag wedged live sessions.
func (rb *retxBuffer) HeldSeqsOnTps(tps map[uuid.UUID]bool) []uint32 {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	seqs := make([]uint32, 0, len(rb.entries))
	for seq, e := range rb.entries {
		if e.tpID == uuid.Nil || tps[e.tpID] {
			seqs = append(seqs, seq)
		}
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	return seqs
}

// ProcessSACK processes a received SACK (last contiguous seq + full-window
// received bitmap) and returns sequence numbers that need retransmission (holes
// in the bitmap where we still hold data). It purges every acknowledged entry,
// including received sequences ABOVE a persistent frontier gap — that whole-
// window purge is what keeps the buffer from filling behind a stuck gap (#86).
// threshold is the reorder-tolerant loss-detection age (RACK): a still-missing
// sequence is retransmitted only once it has been outstanding at least this long,
// so a merely-reordered packet on a slower leg is acked (purged) first. The mux
// supplies an RTT-derived value (routeMux.rackThreshold); a zero/negative
// threshold falls back to the fixed retxMinAge for any caller that doesn't.
func (rb *retxBuffer) ProcessSACK(lastContiguous uint32, words []uint64, threshold time.Duration) []uint32 {
	if threshold <= 0 {
		threshold = retxMinAge
	}
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
	//
	// A hole is aged from its LAST transmission (original send or most recent
	// retx), with per-entry exponential backoff — otherwise a seq that crossed
	// the threshold once is re-selected by every subsequent SACK until acked,
	// and under a feedback delay of several RTTs that is a self-sustaining
	// retransmit storm. Selection marks the entry here, under rb.mu, so the
	// decision and the mark are atomic; if the caller then fails to send (no
	// active leg), the seq simply retries after its backoff.
	now := time.Now()
	var retransmit []uint32
	for w, word := range words {
		base := lastContiguous + 1 + uint32(w)*64
		for i := uint32(0); i < 64; i++ {
			checkSeq := base + i
			if word&(1<<i) != 0 {
				delete(rb.entries, checkSeq)
			} else if e, ok := rb.entries[checkSeq]; ok {
				ref := e.sentAt
				if e.lastTxAt.After(ref) {
					ref = e.lastTxAt
				}
				shift := e.retxCount
				if shift > retxBackoffMaxShift {
					shift = retxBackoffMaxShift
				}
				if now.Sub(ref) >= threshold<<shift {
					retransmit = append(retransmit, checkSeq)
					e.lastTxAt = now
					if e.retxCount < math.MaxUint8 {
						e.retxCount++
					}
				}
			}
		}
	}

	return retransmit
}

// MaxSeq returns the highest unacknowledged sequence still held (the in-flight
// tail) and true, or (0, false) when the buffer is empty. It is the sequence a
// tail-loss probe re-sends: if the tail of a burst was lost the receiver never
// reports it (its bitmap ends at the last seq it actually got), so only a
// sender-driven probe of this seq can elicit the SACK that recovers it.
func (rb *retxBuffer) MaxSeq() (uint32, bool) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	if len(rb.entries) == 0 {
		return 0, false
	}
	var max uint32
	first := true
	for s := range rb.entries {
		if first || s > max {
			max, first = s, false
		}
	}
	return max, true
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

// SentAt returns when seq was originally sent, and whether the buffer still
// holds it (an acked/purged seq returns ok=false). The proactive HoL path uses
// the age to distinguish a seq merely in flight on a slower leg (younger than a
// reorder window — retransmitting it is spurious by construction) from one
// genuinely stalling the frontier.
func (rb *retxBuffer) SentAt(seq uint32) (time.Time, bool) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	if entry, ok := rb.entries[seq]; ok {
		return entry.sentAt, true
	}
	return time.Time{}, false
}

// Len returns current buffer occupancy.
func (rb *retxBuffer) Len() int {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return len(rb.entries)
}

// Seqs returns the sequence numbers of every currently-held unacknowledged
// entry, in ascending order. The buffer is keyed by sequence, not by the leg a
// packet was sent on, so this is the whole outstanding in-flight window — the
// input to the demote-time forced retx flush, which re-sends that window onto an
// active leg before the receiver's SACK round-trip can lose the race with the
// buffer's aging (the #86 retx-window aged-out gap). Ascending so the lowest gap
// — the one the receiver's reorder buffer is head-of-line blocked on — heals
// first.
func (rb *retxBuffer) Seqs() []uint32 {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	out := make([]uint32, 0, len(rb.entries))
	for s := range rb.entries {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
