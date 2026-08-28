package router

import (
	"bytes"
	"fmt"
	"sort"
	"testing"
)

// TestFECMuxRoundTripReconstruct: the core mechanism. A block of K data frames
// is striped; the receiver gets K-1 data frames (one "stuck on a slow leg") plus
// one repair frame that arrived on a fast leg. The missing frame must be
// reconstructed byte-identical — the frontier need never wait for the slow leg.
func TestFECMuxRoundTripReconstruct(t *testing.T) {
	const k, r = 8, 2
	st := newFECStriper(k, r)
	re := newFECReassembler(k, r)
	if st == nil || re == nil {
		t.Fatal("nil striper/reassembler")
	}

	// K data frames with distinct payloads, seqs 0..K-1 (block 0).
	payloads := make([][]byte, k)
	var repairs []fecRepairFrame
	for seq := 0; seq < k; seq++ {
		p := []byte(fmt.Sprintf("frame-%d-payload-%x", seq, seq*7919))
		payloads[seq] = p
		if rf := st.Add(uint32(seq), p); rf != nil {
			repairs = rf
		}
	}
	if len(repairs) != r {
		t.Fatalf("expected %d repair frames on block completion, got %d", r, len(repairs))
	}

	// Receiver gets every data frame EXCEPT seq=3 (the slow-leg victim), plus one
	// repair frame. seq=3 must be reconstructable once ≥K symbols are present.
	const missing = 3
	for seq := 0; seq < k; seq++ {
		if seq == missing {
			continue
		}
		re.RecordData(uint32(seq), payloads[seq])
	}
	// Before any repair: only K-1 symbols → cannot reconstruct.
	if _, ok := re.Reconstruct(missing); ok {
		t.Fatal("reconstructed with only K-1 symbols — MDS violated")
	}
	// One repair frame arrives (on a fast leg) → K symbols present → reconstruct.
	re.RecordRepair(repairs[0].blockID, repairs[0].idx, repairs[0].symLen, repairs[0].symbol)
	got, ok := re.Reconstruct(missing)
	if !ok {
		t.Fatal("reconstruction failed with K symbols present")
	}
	if !bytes.Equal(got, payloads[missing]) {
		t.Fatalf("reconstructed payload mismatch:\n got=%q\nwant=%q", got, payloads[missing])
	}
}

// TestFECMuxUpToRErasures: any combination of up to R missing DATA frames is
// recoverable once R repair frames are present; R+1 missing with only R repair
// is not (MDS boundary). Also exercises variable-length payloads through the
// length-prefixed symbol format.
func TestFECMuxUpToRErasures(t *testing.T) {
	const k, r = 6, 3
	st := newFECStriper(k, r)
	re := newFECReassembler(k, r)

	payloads := make([][]byte, k)
	var repairs []fecRepairFrame
	for seq := 0; seq < k; seq++ {
		// deliberately varied lengths incl. empty and near-symbol-size
		p := bytes.Repeat([]byte{byte(seq + 1)}, seq*250)
		payloads[seq] = p
		if rf := st.Add(uint32(seq), p); rf != nil {
			repairs = rf
		}
	}

	_ = re // re is rebuilt per-target below so caching does not mask a sibling

	// Drop exactly R data frames {1,2,4}; supply all R repairs → each recovers.
	// A fresh reassembler per target keeps each reconstruction fully independent.
	drop := map[int]bool{1: true, 2: true, 4: true}
	for target := range drop {
		rt := newFECReassembler(k, r)
		for seq := 0; seq < k; seq++ {
			if !drop[seq] {
				rt.RecordData(uint32(seq), payloads[seq])
			}
		}
		for _, rf := range repairs {
			rt.RecordRepair(rf.blockID, rf.idx, rf.symLen, rf.symbol)
		}
		got, ok := rt.Reconstruct(uint32(target))
		if !ok {
			t.Fatalf("seq %d: reconstruction failed with R repairs for R erasures", target)
		}
		if !bytes.Equal(got, payloads[target]) {
			t.Fatalf("seq %d: mismatch len got=%d want=%d", target, len(got), len(payloads[target]))
		}
	}

	// Multi-erasure on ONE reassembler: every dropped sibling must reconstruct
	// INDEPENDENTLY and correctly from the same block. Reconstruct deliberately
	// does NOT cache decoded siblings back as present (that stranded uncached
	// siblings and corrupted the block via Decode's in-place mutation — the
	// multi-erasure bug the end-to-end test exposed), so a second Reconstruct on
	// the same block re-decodes from the pristine received symbols and succeeds.
	reMulti := newFECReassembler(k, r)
	for seq := 0; seq < k; seq++ {
		if !drop[seq] {
			reMulti.RecordData(uint32(seq), payloads[seq])
		}
	}
	for _, rf := range repairs {
		reMulti.RecordRepair(rf.blockID, rf.idx, rf.symLen, rf.symbol)
	}
	for target := range drop {
		got, ok := reMulti.Reconstruct(uint32(target))
		if !ok {
			t.Fatalf("multi-erasure: sibling seq %d failed to reconstruct independently", target)
		}
		if !bytes.Equal(got, payloads[target]) {
			t.Fatalf("multi-erasure: sibling seq %d payload mismatch (in-place-mutation regression?)", target)
		}
	}

	// Fresh block, drop R+1 data frames, supply only R repairs → cannot recover.
	st2 := newFECStriper(k, r)
	re2 := newFECReassembler(k, r)
	var rep2 []fecRepairFrame
	for seq := 0; seq < k; seq++ {
		p := []byte(fmt.Sprintf("b-%d", seq))
		payloads[seq] = p
		if rf := st2.Add(uint32(seq), p); rf != nil {
			rep2 = rf
		}
	}
	drop2 := map[int]bool{0: true, 1: true, 2: true, 3: true} // R+1 = 4
	for seq := 0; seq < k; seq++ {
		if !drop2[seq] {
			re2.RecordData(uint32(seq), payloads[seq])
		}
	}
	for _, rf := range rep2 {
		re2.RecordRepair(rf.blockID, rf.idx, rf.symLen, rf.symbol)
	}
	if _, ok := re2.Reconstruct(0); ok {
		t.Fatal("recovered R+1 erasures with only R repair symbols — MDS violated")
	}
}

// legModel is a leg in the discrete-event throughput model: each frame placed on
// it arrives after (queuePosition+1)*perFrameTime.
type legModel struct {
	perFrameTime float64
	queued       int
}

func (l *legModel) enqueue() float64 {
	l.queued++
	return float64(l.queued) * l.perFrameTime
}

// TestFECMuxHoLRemoval is the throughput proof. It models a heterogeneous leg set
// (fast legs + one slow leg) delivering ONE FEC block, and computes the time at
// which the reorder frontier can deliver all K data frames in three regimes:
//
//	single    — all K frames sent sequentially on ONE fast leg (single-route baseline)
//	mux/noFEC — K frames striped across all legs; frontier needs EVERY data frame,
//	            so it is HoL-capped at the slow leg's arrival (the wall)
//	mux/FEC   — K data + R repair; frontier needs any K of K+R symbols, so it
//	            completes at the K-th earliest arrival (fast-leg bound)
//
// It asserts mux/FEC < mux/noFEC (removes HoL) AND mux/FEC < single (aggregates).
func TestFECMuxHoLRemoval(t *testing.T) {
	const (
		k, r     = 8, 2
		fastTime = 1.0  // fast leg: 1 time unit per frame
		slowTime = 12.0 // slow leg: 12x slower (heterogeneous, ~as measured: 205ms vs ~2.5s effective)
		nFast    = 3    // 3 fast legs + 1 slow leg
	)

	// --- arrival time of each symbol under round-robin striping ---
	// Legs: index 0..nFast-1 fast, index nFast slow.
	legs := make([]*legModel, nFast+1)
	for i := 0; i < nFast; i++ {
		legs[i] = &legModel{perFrameTime: fastTime}
	}
	legs[nFast] = &legModel{perFrameTime: slowTime}

	// Stripe K data frames round-robin across ALL legs (incl. slow).
	dataArrival := make([]float64, k)
	for seq := 0; seq < k; seq++ {
		leg := legs[seq%len(legs)]
		dataArrival[seq] = leg.enqueue()
	}
	// R repair frames scheduled onto the FAST legs only (least-loaded fast leg).
	repairArrival := make([]float64, r)
	for i := 0; i < r; i++ {
		// pick the least-loaded fast leg
		best := 0
		for j := 1; j < nFast; j++ {
			if legs[j].queued < legs[best].queued {
				best = j
			}
		}
		repairArrival[i] = legs[best].enqueue()
	}

	// --- regime completion times ---
	// mux/noFEC: frontier delivers all K only when the LAST data frame arrives.
	noFEC := 0.0
	for _, a := range dataArrival {
		if a > noFEC {
			noFEC = a
		}
	}

	// mux/FEC: block decodable when ANY K of the K+R symbols have arrived → the
	// K-th smallest arrival time across data+repair.
	allArr := append(append([]float64{}, dataArrival...), repairArrival...)
	sort.Float64s(allArr)
	withFEC := allArr[k-1] // K-th earliest (0-indexed k-1)

	// single good leg: K frames sequentially on one fast leg.
	single := float64(k) * fastTime

	t.Logf("K=%d R=%d  nFast=%d fastTime=%.0f slowTime=%.0f", k, r, nFast, fastTime, slowTime)
	t.Logf("completion time  single=%.1f  mux/noFEC=%.1f  mux/FEC=%.1f", single, noFEC, withFEC)
	t.Logf("dataArrival=%v repairArrival=%v", dataArrival, repairArrival)

	if !(withFEC < noFEC) {
		t.Fatalf("FEC did not remove HoL: withFEC=%.1f not < noFEC=%.1f", withFEC, noFEC)
	}
	if !(withFEC < single) {
		t.Fatalf("mux/FEC did not aggregate past a single leg: withFEC=%.1f not < single=%.1f", withFEC, single)
	}
	// Quantify: noFEC is dragged to the slow leg (>= slowTime); FEC stays near the
	// fast-parallel bound (well under slowTime).
	if noFEC < slowTime {
		t.Fatalf("model sanity: noFEC=%.1f should be >= slowTime=%.1f (a frame rode the slow leg)", noFEC, slowTime)
	}
	if withFEC >= slowTime {
		t.Fatalf("mux/FEC=%.1f should be well under slowTime=%.1f (fast-leg bound)", withFEC, slowTime)
	}
	speedup := noFEC / withFEC
	t.Logf("HoL removed: mux/FEC is %.1fx faster than mux/noFEC, %.1fx faster than single", speedup, single/withFEC)
}

// TestFECMuxBlockBoundaries: the striper emits repair frames exactly once per K
// frames, at the block-closing seq, with correct blockIDs across many blocks.
func TestFECMuxBlockBoundaries(t *testing.T) {
	const k, r, blocks = 4, 2, 5
	st := newFECStriper(k, r)
	emitted := map[uint32]int{}
	for seq := 0; seq < k*blocks; seq++ {
		rf := st.Add(uint32(seq), []byte{byte(seq)})
		if rf == nil {
			if (seq+1)%k == 0 {
				t.Fatalf("seq %d closes a block but emitted no repair", seq)
			}
			continue
		}
		if (seq+1)%k != 0 {
			t.Fatalf("seq %d emitted repair but is not a block boundary", seq)
		}
		if len(rf) != r {
			t.Fatalf("seq %d: got %d repair frames want %d", seq, len(rf), r)
		}
		wantBlk := uint32(seq / k)
		for _, f := range rf {
			if f.blockID != wantBlk {
				t.Fatalf("seq %d: repair blockID=%d want %d", seq, f.blockID, wantBlk)
			}
			emitted[f.blockID]++
		}
	}
	if len(emitted) != blocks {
		t.Fatalf("expected repairs for %d blocks, got %d", blocks, len(emitted))
	}
}
