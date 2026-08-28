// Package router pkg/router/fec_flush.go — partial-block "timer-flush" (Tetrys-
// style tail protection) for the packet-mux FEC layer (fec_mux.go, #4270).
//
// THE WEAKNESS THIS CLOSES. Fixed-block FEC only emits repair when a block fills
// to K data frames (fecStriper.Add). On a unidirectional bulk stream the LAST
// block is almost always partial: after the final j<K frames the sender goes
// idle, so those j frames get NO repair. If one of them was striped onto a slow
// leg, the no-skip reorder frontier stalls at the very tail with no FEC rescue —
// the single documented failure mode of fixed-block FEC on a bulk transfer. A
// retransmit still recovers it, but coupled to the slow leg's RTT, which is
// exactly the wait erasure coding exists to remove.
//
// THE FIX (Tetrys tail idea, adapted to a systematic block code with ZERO wire
// change). When the sender has been idle for T with a partial block pending, we
// COMPLETE the block synthetically: the K-j missing data slots are filled with
// zero-length padding symbols (symbolize(nil) → a length prefix of 0), the now-
// full K symbols are Encoded, and the R repair frames are emitted just as a
// natural block boundary would emit them. The block is then a genuine K-of-(K+R)
// MDS block, so any real tail frame lost on a slow leg is reconstructable from
// the repair that rode a fast leg — the frontier advances at the fast leg's rate.
//
// WHY NO WIRE-FORMAT OR RECEIVER CHANGE IS NEEDED. The padding is not a new frame
// type. The sender emits the K-j padding slots as REAL zero-length sequenced DATA
// frames (seqs filled..K-1 of the block) on the normal send path. The receiver
// records them through the ordinary data path (RecordData with an empty payload,
// identical to the sender's symbolize(nil)) and, when the frontier reaches each,
// delivers it as a 0-byte read — a no-op for a byte stream. So the receiver's
// block becomes genuinely K-full using only frames it already knows how to parse;
// FlushPartial adds a coding path on the SENDER only. FlushPartial returns how
// many such padding frames the caller must emit; scheduling them and the repair
// onto legs is the send loop's job (deliberately kept out of this file so
// route_group.go can own leg selection).
//
// Everything here is pure/deterministic and attaches to fecStriper by method from
// this separate file (Go permits methods on a package type from any file), so it
// is fully unit-testable without the live mesh and without editing fec_mux.go.
package router

import "time"

// fecDefaultIdleFlush is the default inactivity gap after which a pending partial
// block is flushed. It trades a small extra repair-overhead on the tail for tail
// HoL protection; it must be well above normal inter-frame gaps so a merely
// bursty-but-active stream is not flushed mid-block (Add will complete that block
// naturally). A route-group send loop may override it. 50ms comfortably exceeds
// per-frame spacing at any aggregated rate while still bounding tail-stall risk to
// a single idle interval.
const fecDefaultIdleFlush = 50 * time.Millisecond

// FlushPartial completes and codes the current partial block so its tail frames
// gain FEC protection, instead of waiting for K frames that (on an idle stream)
// will never come.
//
// Behaviour, under s.mu:
//   - If the current block has 0 filled slots (nothing to protect) or is already
//     full (Add resets to filled==0 on completion, so this is defensive), it does
//     nothing and returns ok=false.
//   - Otherwise it fills the K-filled empty data slots with zero-length padding
//     symbols, Encodes the now-full K symbols, and returns:
//     paddingNeeded = K-filled — the number of REAL zero-length sequenced data
//     frames the caller must emit (seqs filled..K-1 of this block) so the
//     receiver's block is genuinely complete;
//     frames        = the R repair frames to schedule (blockID = this block);
//     ok            = true.
//
// Idempotency: on success it advances to the next block (reset(block+1)), which
// zeroes filled. A repeated call on the same unchanged block therefore falls into
// the filled==0 short-circuit and returns ok=false — the reset IS the "already
// flushed" marker, so no extra field on fecStriper is required (and none may be
// added here: fec_mux.go is edited concurrently). A later Add naturally starts a
// fresh block.
func (s *fecStriper) FlushPartial() (paddingNeeded int, frames []fecRepairFrame, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// filled==0: empty block (never added, or already flushed/advanced) → no-op.
	// filled>=k: a full block; Add already emitted its repair and reset filled to
	// 0, so this is only reachable defensively — treat as nothing to do.
	if s.filled == 0 || s.filled >= s.k {
		return 0, nil, false
	}

	blk := s.block
	paddingNeeded = s.k - s.filled

	// Complete the block: every empty data slot becomes a zero-length padding
	// symbol (length prefix 0). These are legitimate coded symbols, so the block
	// is a real K-of-(K+R) MDS block — a genuine tail frame lost on a slow leg is
	// reconstructable from any K of the K+R symbols. The receiver reproduces these
	// same padding symbols from the real zero-length data frames the caller emits
	// (RecordData of an empty payload == symbolize(nil) here), so encoder and
	// decoder agree bit-for-bit with no wire change.
	for i := range s.symbols {
		if s.symbols[i] == nil {
			s.symbols[i], _ = symbolize(nil, s.symLen)
		}
	}
	repair, err := s.coder.Encode(s.symbols)
	if err != nil {
		// Should not happen (dims are fixed at construction). Advance anyway so we
		// do not spin re-flushing an un-encodable block; the tail falls back to
		// retransmit recovery, exactly as pre-flush behaviour.
		s.reset(blk + 1)
		return 0, nil, false
	}
	frames = make([]fecRepairFrame, s.r)
	for i := 0; i < s.r; i++ {
		frames[i] = fecRepairFrame{blockID: blk, idx: uint8(i), symbol: repair[i]}
	}
	// Advance to the next block: fresh state for any later Add, and the idempotency
	// short-circuit above for a repeat FlushPartial.
	s.reset(blk + 1)
	return paddingNeeded, frames, true
}

// hasPartialBlock reports whether a block is currently mid-fill (1..K-1 slots),
// i.e. whether there is anything for FlushPartial to protect. Cheap and safe for
// the send loop to poll. Full blocks are handled by Add; empty blocks have nothing
// to flush.
func (s *fecStriper) hasPartialBlock() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.filled > 0 && s.filled < s.k
}

// --- flush POLICY (pure, deterministic — no time.Now() inside) ---

// fecShouldFlush is the pure timer-flush decision the send loop consults. It is
// deliberately free of time.Now(): the caller passes both the current time and
// the timestamp of the last Add, so the policy is fully deterministic and
// unit-testable (the codebase forbids Date.now-style nondeterminism in tests).
//
// It returns true when a partial block is pending AND the stream has been idle for
// at least idle since the last frame. idle<=0 disables flushing (opt-out); a
// zero-value lastAdd (no frame yet seen) never flushes because pending would be
// false in that case anyway.
func fecShouldFlush(now, lastAdd time.Time, idle time.Duration, pending bool) bool {
	if !pending || idle <= 0 {
		return false
	}
	return now.Sub(lastAdd) >= idle
}

// MaybeFlush is the single entry point a route-group send loop calls on its
// periodic tick: if the idle-gap policy says the pending partial block should be
// flushed, it flushes and returns the padding count + repair frames to schedule;
// otherwise it returns ok=false and no work. Timestamps are passed in so the loop
// owns the clock (and tests can drive it deterministically). The loop remains
// responsible for actually emitting the paddingNeeded zero-length data frames on
// its normal sequenced send path and scheduling frames onto legs.
//
// The hasPartialBlock pre-check is only an optimisation; FlushPartial re-validates
// under the lock and returns ok=false if the block filled or emptied in between,
// so a race with a concurrent Add is harmless.
func (s *fecStriper) MaybeFlush(now, lastAdd time.Time, idle time.Duration) (paddingNeeded int, frames []fecRepairFrame, ok bool) {
	if !fecShouldFlush(now, lastAdd, idle, s.hasPartialBlock()) {
		return 0, nil, false
	}
	return s.FlushPartial()
}
