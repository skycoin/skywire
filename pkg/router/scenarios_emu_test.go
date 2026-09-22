// Package router pkg/router/scenarios_emu_test.go c2-net-routing
//
// Scenarios: each one reproduces a finding from the live mux campaign against
// the emulated testbed and asserts the behavior the fix is supposed to give.
// Every scenario prints the same summary block, so an emulated row and a
// bench row can be read side by side.
//
// Adding one: take the leg shapes from the bench row that produced the finding
// (the carrier rows give per-leg rates, the mux events give the delay bases),
// scale rates and transfer size down until the run is a couple of seconds, and
// assert the PROPERTY — never the absolute number.
package router

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/router/emu"
)

const (
	// emuMB scales every rate and transfer in this file. Rates are real
	// bytes per second, so a scenario's duration is transfer/rate.
	emuMB = 1 << 20
	// emuTimeout bounds one transfer. Every scenario here is designed to
	// finish in a couple of seconds; the timeout only turns a wedge into a
	// failed assertion instead of a hung test binary.
	emuTimeout = 25 * time.Second
)

// emuBaseline runs the same transfer over a ONE-leg rig with the given leg —
// the "that leg alone" bar every aggregation claim is measured against.
func emuBaseline(t *testing.T, spec emuLegSpec, dir emuDir, n int64) emu.Summary {
	t.Helper()
	rig := newEmuRig(t, emuOpts{Legs: []emuLegSpec{spec}})
	x := rig.Transfer(dir, n, emuTimeout)
	s := rig.Summary(spec.Name+"-alone", dir, x)
	rig.Close()
	return s
}

// symmetric builds a leg whose two directions have the same shape.
func symmetric(name string, rateBps int64, rtt time.Duration, queue int64) emuLegSpec {
	owd := rtt / 2
	lc := emu.LinkConfig{Delay: owd, RateBps: rateBps, QueueBytes: queue}
	return emuLegSpec{Name: name, Up: lc, Down: lc, LatencyMs: float64(rtt) / float64(time.Millisecond)}
}

// TestEmuDeadLegShareIsBounded is scenario (a) at a scale that fits the fast
// suite: a 7 MB/s leg beside one running at 60 KB/s with ~1 s of queueing —
// the 2026-09-18 composition collapse
// (bench/2026-09-18/1008cc8e5/mux-compose-T2xL2), where the unweighted reverse
// round-robin put 0.5-2.2 MB of every 10 MB download on the slow leg and the
// five trials took 8.3-37.4 s.
//
// What holds today: the stream completes intact, the recovery costs almost
// nothing on the wire, and the slow leg's byte share is small. What does NOT
// hold is the RATE — see TestEmuDeadLegDoesNotDragTheGroup (build tag
// emulong) and docs/design/emulated-testbed.md. The goodput ratio and both
// legs' delay bases are logged here so the numbers move visibly when the
// outclassed-leg gate is fixed.
func TestEmuDeadLegShareIsBounded(t *testing.T) {
	const bytes = 8 * emuMB
	good := symmetric("good-7MBs-45ms", 7*emuMB, 45*time.Millisecond, 2*emuMB)
	slow := symmetric("slow-60KBs-1s", 60*1024, 45*time.Millisecond, 64*1024)

	base := emuBaseline(t, good, emuDown, bytes)
	t.Log(base.Table())

	rig := newEmuRig(t, emuOpts{Legs: []emuLegSpec{good, slow}})
	x := rig.Transfer(emuDown, bytes, emuTimeout)
	s := rig.Summary("dead-leg-share", emuDown, x)
	ratio := 0.0
	if base.GoodputBps() > 0 {
		ratio = s.GoodputBps() / base.GoodputBps()
	}
	s.Notes = append(s.Notes, fmt.Sprintf("goodput x%.2f of the good leg alone; %s", ratio, rig.legBases(rig.B)))
	t.Log(s.Table())

	if !s.HashOK {
		t.Errorf("transfer did not complete intact: got %d/%d bytes", s.Got, s.Bytes)
	}
	if r := s.WireRatio(); r > 1.2 {
		t.Errorf("wire/goodput %.3f over 1.2 — the slow leg cost a retransmit storm as well as time", r)
	}
	if share := s.Legs[1].Share(s.PayloadTotal()); share > 0.10 {
		t.Errorf("the outclassed leg took %.1f%% of the payload", 100*share)
	}
}

// TestEmuHealthySkewStillAggregates is scenario (b): the first live run of the
// probe-only gate (bench/2026-09-18/9f4848dfa-smoke) cut a HEALTHY 166 ms leg
// beside a 44 ms one five times and the set fell to x0.854. A pair like this
// must aggregate, and neither leg may be ruled probe-only.
func TestEmuHealthySkewStillAggregates(t *testing.T) {
	const bytes = 16 * emuMB
	fast := symmetric("fast-44ms", 3*emuMB, 44*time.Millisecond, 2*emuMB)
	slow := symmetric("slow-166ms", 3*emuMB, 166*time.Millisecond, 2*emuMB)

	base := emuBaseline(t, fast, emuDown, bytes)
	t.Log(base.Table())

	rig := newEmuRig(t, emuOpts{Legs: []emuLegSpec{fast, slow}})
	x := rig.Transfer(emuDown, bytes, emuTimeout)
	s := rig.Summary("healthy-skew", emuDown, x)
	t.Log(s.Table())

	if !s.HashOK {
		t.Errorf("transfer did not complete intact: got %d/%d bytes", s.Got, s.Bytes)
	}
	for i := range s.Legs {
		if s.Legs[i].ProbeOnly {
			t.Errorf("leg %d (%s) was ruled probe-only — a healthy latency skew is not an outclassed leg",
				i, s.Legs[i].Name)
		}
	}
	if want := 1.5 * base.GoodputBps(); s.GoodputBps() < want {
		t.Errorf("two equal-rate legs ran at %.0f B/s, under 1.5x one alone (%.0f B/s): no aggregation across a healthy skew",
			s.GoodputBps(), want)
	}
}

// TestEmuLossBurstKeepsHashesAndBoundsWire is scenario (c): 3 % loss on one of
// two legs. The stream must still reassemble byte for byte, and the recovery
// must cost bounded overhead rather than a retransmit storm (the live
// wire/goodput 1.5 rows with hash failures that the rackCeil clamp and the
// per-SACK re-selection were for).
func TestEmuLossBurstKeepsHashesAndBoundsWire(t *testing.T) {
	const bytes = 4 * emuMB
	clean := symmetric("clean", 3*emuMB, 60*time.Millisecond, 2*emuMB)
	lossy := symmetric("lossy-3pct", 3*emuMB, 60*time.Millisecond, 2*emuMB)
	lossy.Down.LossPct = 3
	lossy.Up.LossPct = 3

	rig := newEmuRig(t, emuOpts{Legs: []emuLegSpec{clean, lossy}, HolRetx: true})
	x := rig.Transfer(emuDown, bytes, emuTimeout)
	s := rig.Summary("loss-burst", emuDown, x)
	t.Log(s.Table())

	if !s.HashOK {
		t.Fatalf("3%% loss on one leg corrupted or truncated the stream: got %d/%d bytes", s.Got, s.Bytes)
	}
	if r := s.WireRatio(); r > 1.2 {
		t.Errorf("wire/goodput %.3f over 1.2 recovering 3%% loss on one of two legs", r)
	}
}

// TestEmuCutOfBusiestLegCompletes is scenario (d): the busiest leg black-holes
// mid-transfer (a live leg dying without its socket closing). The transfer
// must finish on the survivors, the stream must resume inside a couple of
// seconds, and nothing may rebuild the group — failover comes from the legs
// already there.
func TestEmuCutOfBusiestLegCompletes(t *testing.T) {
	const bytes = 8 * emuMB
	legs := []emuLegSpec{
		symmetric("a", 3*emuMB, 50*time.Millisecond, 2*emuMB),
		symmetric("b", 3*emuMB, 60*time.Millisecond, 2*emuMB),
		symmetric("c", 3*emuMB, 70*time.Millisecond, 2*emuMB),
	}
	rig := newEmuRig(t, emuOpts{Legs: legs})
	legsBeforeA, legsBeforeB := rig.AddedLegs()

	cutAt := make(chan time.Duration, 1)
	go func() {
		deadline := time.Now().Add(emuTimeout)
		for time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
			if rig.busiestSendLeg() >= 0 && rig.downProgress() > bytes/3 {
				break
			}
		}
		idx := rig.busiestSendLeg()
		if idx < 0 {
			idx = 0
		}
		before := rig.downProgress()
		at := time.Now()
		rig.Leg(idx).CutDown()
		// Time to the first byte that lands AFTER the cut.
		for time.Since(at) < emuTimeout {
			if rig.downProgress() > before {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		cutAt <- time.Since(at)
	}()

	x := rig.Transfer(emuDown, bytes, emuTimeout)
	ttfb := <-cutAt
	s := rig.Summary("cut-busiest-leg", emuDown, x)
	s.TTFB = ttfb
	s.Notes = append(s.Notes, "ttfb is measured from the cut to the next byte delivered")
	t.Log(s.Table())

	if !s.HashOK {
		t.Errorf("the transfer did not survive a mid-flight cut: got %d/%d bytes", s.Got, s.Bytes)
	}
	if ttfb > 2*time.Second {
		t.Errorf("the stream resumed %v after the cut, past the 2 s bar", ttfb)
	}
	a, b := rig.AddedLegs()
	if a != legsBeforeA || b != legsBeforeB {
		t.Errorf("the group rebuilt across the cut: legs %d->%d / %d->%d", legsBeforeA, a, legsBeforeB, b)
	}
}

// TestEmuFlappingLegDoesNotWedge is scenario (e): three legs, one cut and
// restored every 500 ms for the whole transfer. The no-skip reorder frontier
// must keep advancing — a flapping leg is the shape that wedged the group at
// collapse-to-0 before the liveness teardown fix.
func TestEmuFlappingLegDoesNotWedge(t *testing.T) {
	const bytes = 6 * emuMB
	legs := []emuLegSpec{
		symmetric("steady-a", 3*emuMB, 50*time.Millisecond, 2*emuMB),
		symmetric("steady-b", 3*emuMB, 60*time.Millisecond, 2*emuMB),
		symmetric("flapping", 3*emuMB, 60*time.Millisecond, 2*emuMB),
	}
	rig := newEmuRig(t, emuOpts{Legs: legs})

	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				rig.Leg(2).Restore()
				return
			case <-time.After(500 * time.Millisecond):
			}
			rig.Leg(2).Cut()
			select {
			case <-stop:
				rig.Leg(2).Restore()
				return
			case <-time.After(500 * time.Millisecond):
			}
			rig.Leg(2).Restore()
		}
	}()

	x := rig.Transfer(emuDown, bytes, emuTimeout)
	close(stop)
	s := rig.Summary("flapping-leg", emuDown, x)
	s.Notes = append(s.Notes, "leg 2 cut and restored every 500 ms for the whole transfer")
	t.Log(s.Table())

	if !s.HashOK {
		t.Errorf("a flapping leg wedged the group: got %d/%d bytes after %v", s.Got, s.Bytes, s.Elapsed)
	}
}

// TestEmuUploadConfinementHoldsOnLowestLatencyLeg is scenario (f): with
// CapUniDir the client-sent direction rides ONE leg, and with no direct leg in
// the group that leg is the lowest-latency one. The live failure this guards
// is the 79 %/21 % split across a 44 ms and a 166 ms leg when the flip
// controller moved the forward direction onto the multihop class.
func TestEmuUploadConfinementHoldsOnLowestLatencyLeg(t *testing.T) {
	const bytes = 4 * emuMB
	legs := []emuLegSpec{
		symmetric("mid-90ms", 3*emuMB, 90*time.Millisecond, 2*emuMB),
		symmetric("fast-30ms", 3*emuMB, 30*time.Millisecond, 2*emuMB),
		symmetric("slow-200ms", 3*emuMB, 200*time.Millisecond, 2*emuMB),
	}
	rig := newEmuRig(t, emuOpts{Legs: legs, Directional: true})
	x := rig.Transfer(emuUp, bytes, emuTimeout)
	s := rig.Summary("upload-confinement", emuUp, x)
	t.Log(s.Table())

	if !s.HashOK {
		t.Errorf("upload did not complete intact: got %d/%d bytes", s.Got, s.Bytes)
	}
	total := s.PayloadTotal()
	if total == 0 {
		t.Fatal("no payload was credited to any leg")
	}
	if share := s.Legs[1].Share(total); share < 0.9 {
		t.Errorf("the upload put only %.1f%% on the lowest-latency leg — confinement did not hold (shares %.1f/%.1f/%.1f%%)",
			100*share, 100*s.Legs[0].Share(total), 100*s.Legs[1].Share(total), 100*s.Legs[2].Share(total))
	}
}

// TestEmuTransportRemovedUnderLegCompletes is the bench's standby cut in the
// testbed: the transport under the busiest leg is REMOVED (`skywire cli tp rm`
// → transport.Manager.DeleteTransport → ManagedTransport close → the router's
// close hook), not merely black-holed. The leg must be gone from the group at
// the close, not at a timeout, and the stream must resume inside the 2 s bar.
func TestEmuTransportRemovedUnderLegCompletes(t *testing.T) {
	const bytes = 8 * emuMB
	legs := []emuLegSpec{
		symmetric("a", 3*emuMB, 50*time.Millisecond, 2*emuMB),
		symmetric("b", 3*emuMB, 60*time.Millisecond, 2*emuMB),
		symmetric("c", 3*emuMB, 70*time.Millisecond, 2*emuMB),
	}
	rig := newEmuRig(t, emuOpts{Legs: legs, Liveness: true})

	type cutResult struct{ death, ttfb time.Duration }
	res := make(chan cutResult, 1)
	go func() {
		deadline := time.Now().Add(emuTimeout)
		for time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
			if rig.busiestSendLeg() >= 0 && rig.downProgress() > bytes/3 {
				break
			}
		}
		idx := rig.busiestSendLeg()
		if idx < 0 {
			idx = 0
		}
		before := rig.downProgress()
		legsBefore := rig.LegsOn(false)
		at := time.Now()
		rig.Leg(idx).RemoveTransport()

		// Both clocks run together, off one loop: time to leg death (the
		// sending end no longer counts the removed transport as a leg it may
		// schedule on) and time to the first byte that lands after the removal.
		death, ttfb := emuTimeout, emuTimeout
		for time.Since(at) < emuTimeout {
			if death == emuTimeout && rig.LegsOn(false) < legsBefore {
				death = time.Since(at)
			}
			if ttfb == emuTimeout && rig.downProgress() > before {
				ttfb = time.Since(at)
			}
			if death < emuTimeout && ttfb < emuTimeout {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		res <- cutResult{death: death, ttfb: ttfb}
	}()

	x := rig.Transfer(emuDown, bytes, emuTimeout)
	got := <-res
	s := rig.Summary("transport-removed-under-leg", emuDown, x)
	s.TTFB = got.ttfb
	s.Notes = append(s.Notes,
		"the leg's transport is removed, not black-holed",
		fmt.Sprintf("leg death %v; ttfb measured from the removal to the next byte delivered", got.death))
	t.Log(s.Table())

	if !s.HashOK {
		t.Errorf("the transfer did not survive the removal: got %d/%d bytes", s.Got, s.Bytes)
	}
	if got.death > 2*time.Second {
		t.Errorf("the leg was still schedulable %v after its transport was removed, past the 2 s bar", got.death)
	}
	if got.ttfb > 2*time.Second {
		t.Errorf("the stream resumed %v after the removal, past the 2 s bar", got.ttfb)
	}
}

// TestEmuTransportRemovedUnderSoleLegErrorsAtOnce is the single-leg tunnel the
// standby pool actually runs: every tunnel in the pool is a one-leg group, so
// the removed transport takes the whole group with it. The reader must be
// handed its error at the close — that error IS the failover trigger, and the
// pool promotes a standby in the same tick it arrives.
func TestEmuTransportRemovedUnderSoleLegErrorsAtOnce(t *testing.T) {
	rig := newEmuRig(t, emuOpts{Legs: []emuLegSpec{
		symmetric("only", 3*emuMB, 50*time.Millisecond, 2*emuMB),
	}})

	readErr := make(chan error, 1)
	go func() {
		buf := make([]byte, 64*1024)
		for {
			if _, err := rig.A.rg.Read(buf); err != nil {
				readErr <- err
				return
			}
		}
	}()

	time.Sleep(100 * time.Millisecond) // let the reader park in Read
	at := time.Now()
	rig.Leg(0).RemoveTransport()

	select {
	case err := <-readErr:
		took := time.Since(at)
		t.Logf("the sole leg's removal reached the reader in %v: %v", took, err)
		if took > 2*time.Second {
			t.Errorf("the reader waited %v for a group whose only transport was gone, past the 2 s bar", took)
		}
	case <-time.After(emuTimeout):
		t.Fatalf("the reader was never told: %v after its group's only transport was removed it is still parked in Read", emuTimeout)
	}
}

// emuHopLeg builds a 2-HOP leg: same shape both ways, and a transport whose
// remote is an intermediary rather than the peer. Two of these and no direct
// leg is the legs-2 shape of the live rig, and the shape CapUniDir's
// direction→leg-class mapping cannot act on at all, because both classes are
// then "multihop".
func emuHopLeg(name string, rateBps int64, rtt time.Duration, queue int64) emuLegSpec {
	return symmetric(name, rateBps, rtt, queue)
}

// emuFanoutEvents returns the sender-side forward_fanout / forward_confined
// lines of a summary.
func emuFanoutEvents(s emu.Summary) []string {
	var out []string
	for _, e := range s.Events {
		if strings.Contains(e, MuxEventForwardFanout) || strings.Contains(e, MuxEventForwardConfined) {
			out = append(out, e)
		}
	}
	return out
}

// emuFanoutPair is the leg pair both upload scenarios use: two comparable
// 2-hop legs, no direct leg, a 1.25x latency skew well inside the default
// unidir.fanout_max_skew.
func emuFanoutPair() (fast, slow emuLegSpec) {
	return emuHopLeg("hop-120ms", 3*emuMB, 120*time.Millisecond, 768*1024),
		emuHopLeg("hop-150ms", 3*emuMB, 150*time.Millisecond, 768*1024)
}

// TestEmuUploadHeavyFansForwardOverTheLegs is the upload cell the live campaign
// never won. With CapUniDir the client-sent direction rides ONE leg, which is
// what keeps an interactive session on the low-latency leg — and which also
// caps a bulk upload at that one leg's rate: bench/2026-09-18/898591982-smoke
// measured x0.69 (10 MB) and x0.88 (50 MB) of one leg alone, with 100 % of
// every upload row's forward bytes on a single leg. A sustained upload must
// widen over the sibling instead of waiting on it, and say so in the events.
//
// The bar is 1.25x. It is not higher because the emulated rig's own ceiling is
// not: the same 16 MB over the same pair in the DOWNLOAD direction — which has
// always aggregated — measures x1.42, and the fan-out measures x1.35-1.52
// across runs. The property under test is that the forward direction now
// aggregates at all; how close to the ceiling it gets is the live rig's to say.
func TestEmuUploadHeavyFansForwardOverTheLegs(t *testing.T) {
	const bytes = 16 * emuMB
	fast, slow := emuFanoutPair()

	base := emuBaseline(t, fast, emuUp, bytes)
	t.Log(base.Table())

	rig := newEmuRig(t, emuOpts{Legs: []emuLegSpec{fast, slow}, Directional: true})
	x := rig.Transfer(emuUp, bytes, emuTimeout)
	s := rig.Summary("upload-fanout", emuUp, x)
	ratio := 0.0
	if base.GoodputBps() > 0 {
		ratio = s.GoodputBps() / base.GoodputBps()
	}
	events := emuFanoutEvents(s)
	s.Notes = append(s.Notes, fmt.Sprintf("upload x%.2f of the lowest-latency leg alone; fan-out events: %v", ratio, events))
	t.Log(s.Table())

	if !s.HashOK {
		t.Errorf("upload did not complete intact: got %d/%d bytes", s.Got, s.Bytes)
	}
	if len(events) == 0 {
		t.Errorf("no %s mux event: the forward direction never fanned out, so the upload was capped at one leg",
			MuxEventForwardFanout)
	}
	total := s.PayloadTotal()
	if total == 0 {
		t.Fatal("no payload was credited to any leg")
	}
	if share := s.Legs[1].Share(total); share < 0.25 {
		t.Errorf("the sibling leg carried %.1f%% of the upload — the fan-out did not stride the band", 100*share)
	}
	if want := 1.25 * base.GoodputBps(); s.GoodputBps() < want {
		t.Errorf("a 16 MB upload over two comparable legs ran at %.0f B/s (x%.2f), under the 1.25x bar (%.0f B/s): the forward direction did not aggregate",
			s.GoodputBps(), ratio, want)
	}
}

// TestEmuUploadFanoutRevertsWhenIdle is the other half of the trade the live
// rig rejected in #5050: the widening must not outlive the load. Once the bulk
// upload is over the forward direction goes back to its single lowest-latency
// leg, so the next request does not ride the slower one.
func TestEmuUploadFanoutRevertsWhenIdle(t *testing.T) {
	fast, slow := emuFanoutPair()
	rig := newEmuRig(t, emuOpts{Legs: []emuLegSpec{fast, slow}, Directional: true})

	up := rig.Transfer(emuUp, 16*emuMB, emuTimeout)
	s := rig.Summary("upload-then-idle", emuUp, up)
	t.Log(s.Table())
	if !s.HashOK {
		t.Fatalf("the bulk upload did not complete intact: got %d/%d bytes", s.Got, s.Bytes)
	}
	if len(emuFanoutEvents(s)) == 0 {
		t.Fatalf("the bulk upload never fanned out, so there is no revert to test")
	}

	// The load is gone: a small download, then the wait the latch is entitled
	// to (unidir.fanout_release, 2 s by default, measured from the last frame
	// the upload placed).
	down := rig.Transfer(emuDown, 2*emuMB, emuTimeout)
	if !down.HashOK {
		t.Fatalf("the download did not complete intact: got %d/%d bytes", down.Got, down.Bytes)
	}
	deadline := time.Now().Add(6 * time.Second)
	for rig.A.rg.mux.forwardFanoutActive() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if rig.A.rg.mux.forwardFanoutActive() {
		t.Fatal("the forward direction is still fanned out 6 s after the upload ended")
	}

	before := rig.legSentBytes(rig.A)
	req := rig.Transfer(emuUp, 256*1024, emuTimeout)
	if !req.HashOK {
		t.Fatalf("the request-sized upload did not complete intact: got %d/%d bytes", req.Got, req.Bytes)
	}
	after := rig.legSentBytes(rig.A)
	cur := rig.A.rg.mux.confinedForwardIdx()
	d0, d1 := after[0]-before[0], after[1]-before[1]
	t.Logf("after the revert the request put %d B on leg 0 and %d B on leg 1 (confined leg %d)", d0, d1, cur)

	if directional, flipped := rig.A.rg.mux.dirState(); !directional || flipped {
		t.Errorf("dirState = directional %v flipped %v — the direction→class mapping moved, which the fan-out must never do",
			directional, flipped)
	}
	if cur != 0 {
		t.Errorf("the forward direction is confined to leg %d, not the lowest-latency leg 0", cur)
	}
	if d0 == 0 || d1 > d0/4 {
		t.Errorf("the post-upload request put %d B on leg 0 and %d B on leg 1 — it did not go back to the lowest-latency leg alone", d0, d1)
	}
}

// TestEmuUploadFansOffAFullDirectLeg is the compose shape: the group HAS a
// direct leg, which is the one the forward direction sits on by class, plus a
// multihop sibling. A heavy upload must use both once the direct leg's window
// is full — something the direction→leg-class mapping can never arrange,
// because a flip only ever moves the direction from one single leg to another.
//
// The sibling is 70 ms against the direct leg's 40 ms, inside the default
// unidir.fanout_max_skew of 2.0. A real compose tunnel's multihop leg often
// measures 3-4x its direct leg and stays confined at that default: the band is
// deliberately narrow, because #5050's unconditional widening across a 44 ms /
// 166 ms pair measured x0.24 on the live rig. Where the default belongs is a
// question for the rig, and unidir.fanout_max_skew is where it is asked.
//
// The gain here is small by design and the assertions say so: ECF keeps the
// 40 ms leg primary and the sibling takes only what its window cannot hold, so
// the multihop leg carries ~13 % and the transfer measures x1.05. What the
// scenario proves is that the spill HAPPENS in a group that has a direct leg —
// the case the direction→class mapping cannot reach at all — not that it wins
// the same aggregation two comparable legs do.
func TestEmuUploadFansOffAFullDirectLeg(t *testing.T) {
	const bytes = 16 * emuMB
	direct := symmetric("direct-40ms", 4*emuMB, 40*time.Millisecond, 768*1024)
	direct.Direct = true
	hop := emuHopLeg("hop-70ms", 3*emuMB, 70*time.Millisecond, 768*1024)

	base := emuBaseline(t, direct, emuUp, bytes)
	t.Log(base.Table())

	rig := newEmuRig(t, emuOpts{Legs: []emuLegSpec{direct, hop}, Directional: true})
	x := rig.Transfer(emuUp, bytes, emuTimeout)
	s := rig.Summary("upload-off-full-direct-leg", emuUp, x)
	ratio := 0.0
	if base.GoodputBps() > 0 {
		ratio = s.GoodputBps() / base.GoodputBps()
	}
	s.Notes = append(s.Notes, fmt.Sprintf("upload x%.2f of the direct leg alone; fan-out events: %v",
		ratio, emuFanoutEvents(s)))
	t.Log(s.Table())

	if !s.HashOK {
		t.Errorf("upload did not complete intact: got %d/%d bytes", s.Got, s.Bytes)
	}
	total := s.PayloadTotal()
	if total == 0 {
		t.Fatal("no payload was credited to any leg")
	}
	if share := s.Legs[1].Share(total); share < 0.10 {
		t.Errorf("the multihop leg carried %.1f%% of the upload — the direct leg's full window never spilled", 100*share)
	}
	if s.GoodputBps() < base.GoodputBps() {
		t.Errorf("the upload ran at %.0f B/s (x%.2f) over both legs, under the direct leg alone (%.0f B/s)",
			s.GoodputBps(), ratio, base.GoodputBps())
	}
}
