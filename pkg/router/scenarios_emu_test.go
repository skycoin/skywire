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
