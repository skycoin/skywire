package router

import (
	"bytes"
	"fmt"
	"testing"
	"time"
)

// TestFECFlushPartialReconstruct is the core proof: a partial block of j<K real
// frames, once flushed (padding + repair), lets the receiver reconstruct a
// MISSING real tail frame — the tail HoL stall FlushPartial exists to remove.
//
// It builds the receiver EXACTLY as the wire would: the j real frames arrive via
// RecordData (one dropped, the slow-leg victim), the K-j padding frames arrive as
// zero-length RecordData (what the sender emits on its normal data path), and the
// R repair frames arrive via RecordRepair. With any K of the K+R symbols present
// the missing real frame decodes byte-identical.
func TestFECFlushPartialReconstruct(t *testing.T) {
	const k, r = 8, 2
	const j = 5       // partial block: 5 real frames, seqs 0..4 (block 0)
	const missing = 2 // a real tail frame stuck on a slow leg

	st := newFECStriper(k, r, fecSymLen)
	re := newFECReassembler(k, r, fecSymLen)
	if st == nil || re == nil {
		t.Fatal("nil striper/reassembler")
	}

	payloads := make([][]byte, j)
	for seq := 0; seq < j; seq++ {
		p := []byte(fmt.Sprintf("tail-frame-%d-%x", seq, seq*104729))
		payloads[seq] = p
		if rf := st.Add(uint32(seq), p); rf != nil {
			t.Fatalf("seq %d (<K) should not complete a block, got %d repair frames", seq, len(rf))
		}
	}

	// Idle → flush the partial block.
	paddingNeeded, frames, ok := st.FlushPartial()
	if !ok {
		t.Fatal("FlushPartial returned ok=false on a partial block")
	}
	if paddingNeeded != k-j {
		t.Fatalf("paddingNeeded=%d want K-j=%d", paddingNeeded, k-j)
	}
	if len(frames) != r {
		t.Fatalf("got %d repair frames want R=%d", len(frames), r)
	}

	// Receiver: real frames except the missing one.
	for seq := 0; seq < j; seq++ {
		if seq == missing {
			continue
		}
		re.RecordData(uint32(seq), payloads[seq])
	}
	// The K-j padding frames the sender emits as real zero-length data frames.
	for seq := j; seq < k; seq++ {
		re.RecordData(uint32(seq), nil)
	}
	// With (j-1) real + (K-j) padding = K-1 symbols, still one short → no decode.
	if _, ok := re.Reconstruct(missing); ok {
		t.Fatal("reconstructed with only K-1 symbols — MDS violated")
	}
	// One repair frame (rode a fast leg) → K symbols present → reconstruct.
	re.RecordRepair(frames[0].blockID, frames[0].idx, frames[0].symbol)
	got, ok := re.Reconstruct(missing)
	if !ok {
		t.Fatal("reconstruction of missing tail frame failed with K symbols present")
	}
	if !bytes.Equal(got, payloads[missing]) {
		t.Fatalf("reconstructed tail payload mismatch:\n got=%q\nwant=%q", got, payloads[missing])
	}
}

// TestFECFlushIdempotent: a second FlushPartial on the same unchanged (now
// advanced) block returns ok=false — the reset-to-next-block IS the flushed marker.
func TestFECFlushIdempotent(t *testing.T) {
	const k, r = 8, 2
	st := newFECStriper(k, r, fecSymLen)
	for seq := 0; seq < 3; seq++ {
		st.Add(uint32(seq), []byte{byte(seq)})
	}
	if _, _, ok := st.FlushPartial(); !ok {
		t.Fatal("first FlushPartial should succeed on a partial block")
	}
	if pad, frames, ok := st.FlushPartial(); ok || pad != 0 || frames != nil {
		t.Fatalf("second FlushPartial should be a no-op, got ok=%v pad=%d frames=%d", ok, pad, len(frames))
	}
}

// TestFECFlushEmptyBlock: FlushPartial on a block with no frames is a no-op. Both
// a fresh (never-added) striper and one sitting at a clean block boundary qualify.
func TestFECFlushEmptyBlock(t *testing.T) {
	const k, r = 8, 2

	fresh := newFECStriper(k, r, fecSymLen)
	if pad, frames, ok := fresh.FlushPartial(); ok || pad != 0 || frames != nil {
		t.Fatalf("FlushPartial on empty striper should be a no-op, got ok=%v pad=%d", ok, pad)
	}

	// Fill a full block (Add emits repair, resets to a clean empty next block),
	// then FlushPartial must still be a no-op — there is nothing partial pending.
	full := newFECStriper(k, r, fecSymLen)
	for seq := 0; seq < k; seq++ {
		full.Add(uint32(seq), []byte{byte(seq)})
	}
	if pad, frames, ok := full.FlushPartial(); ok || pad != 0 || frames != nil {
		t.Fatalf("FlushPartial at a clean block boundary should be a no-op, got ok=%v pad=%d", ok, pad)
	}
}

// TestFECFlushPaddingCount: paddingNeeded == K-j across every partial fill level.
func TestFECFlushPaddingCount(t *testing.T) {
	const k, r = 8, 2
	for j := 1; j < k; j++ {
		st := newFECStriper(k, r, fecSymLen)
		base := uint32(j * k) // start at an arbitrary non-zero block to exercise blockID math
		for i := 0; i < j; i++ {
			st.Add(base+uint32(i), []byte{byte(i)})
		}
		pad, frames, ok := st.FlushPartial()
		if !ok {
			t.Fatalf("j=%d: FlushPartial ok=false", j)
		}
		if pad != k-j {
			t.Fatalf("j=%d: paddingNeeded=%d want %d", j, pad, k-j)
		}
		wantBlk := base / uint32(k)
		for _, f := range frames {
			if f.blockID != wantBlk {
				t.Fatalf("j=%d: repair blockID=%d want %d", j, f.blockID, wantBlk)
			}
		}
	}
}

// TestFECFlushFullBlockNoop: a block that reaches j==K needs no FlushPartial — Add
// already emitted its repair, and FlushPartial afterward is a no-op.
func TestFECFlushFullBlockNoop(t *testing.T) {
	const k, r = 8, 2
	st := newFECStriper(k, r, fecSymLen)
	var repairs []fecRepairFrame
	for seq := 0; seq < k; seq++ {
		if rf := st.Add(uint32(seq), []byte{byte(seq)}); rf != nil {
			repairs = rf
		}
	}
	if len(repairs) != r {
		t.Fatalf("full block should emit %d repair via Add, got %d", r, len(repairs))
	}
	if _, _, ok := st.FlushPartial(); ok {
		t.Fatal("FlushPartial should be a no-op right after a full block completed")
	}
}

// TestFECFlushPolicy exercises the pure idle-gap decision and the MaybeFlush entry
// point with injected timestamps (no time.Now() in the logic under test).
func TestFECFlushPolicy(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	const idle = 50 * time.Millisecond

	// Pure policy truth table.
	if fecShouldFlush(base.Add(idle), base, idle, true) != true {
		t.Fatal("exactly-idle with a pending block should flush")
	}
	if fecShouldFlush(base.Add(idle-time.Nanosecond), base, idle, true) != false {
		t.Fatal("under the idle gap should not flush")
	}
	if fecShouldFlush(base.Add(time.Hour), base, idle, false) != false {
		t.Fatal("no pending block should never flush")
	}
	if fecShouldFlush(base.Add(time.Hour), base, 0, true) != false {
		t.Fatal("idle<=0 disables flushing")
	}

	// MaybeFlush: below the gap does nothing; at/above the gap flushes a partial.
	st := newFECStriper(8, 2, fecSymLen)
	lastAdd := base
	for seq := 0; seq < 4; seq++ {
		st.Add(uint32(seq), []byte{byte(seq)})
	}
	if _, _, ok := st.MaybeFlush(base.Add(idle/2), lastAdd, idle); ok {
		t.Fatal("MaybeFlush should not fire below the idle gap")
	}
	pad, frames, ok := st.MaybeFlush(base.Add(idle), lastAdd, idle)
	if !ok || pad != 4 || len(frames) != 2 {
		t.Fatalf("MaybeFlush at idle should flush: ok=%v pad=%d frames=%d", ok, pad, len(frames))
	}
	// Nothing partial remains → a further MaybeFlush is a no-op.
	if _, _, ok := st.MaybeFlush(base.Add(time.Hour), lastAdd, idle); ok {
		t.Fatal("MaybeFlush should be a no-op once no partial block is pending")
	}
}
