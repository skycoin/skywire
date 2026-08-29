// Package router pkg/router/mux_scheduler.go c2-net-routing
package router

import "math"

// This file holds two additional completion-time predictive mux schedulers that
// reason over the SAME per-leg estimator snapshot ECF maintains (ecfLegState:
// mean/baseline RTT, jitter, send rate, BDP cwnd and the selector-tracked
// in-flight byte estimate — all refreshed by the mux's rebuildWeights ECF branch
// from the continuously-sampled legCounters EWMAs). Both are pure dispatch-side
// decisions: they change only which leg the next DATA frame rides, add no wire
// field, need no peer coordination, and fall back to the schedule when no
// estimator state exists yet (cold start).
//
//   - OTIAS (Out-of-order Transmission for In-order Arrival, Yang et al.):
//     assign each segment to the leg whose ESTIMATED ARRIVAL time is soonest —
//     queueing delay (current backlog draining at the leg's rate) plus one-way
//     propagation (≈RTT/2). Because the chosen leg is charged the frame's bytes,
//     its next estimate rises, so a run of segments fans out across legs by
//     arrival time; OTIAS will deliberately hand a LATER segment to a slower but
//     idle leg once the fast leg's queue would make it arrive later. The reorder
//     buffer restores stream order, so out-of-order transmission yields in-order
//     arrival.
//
//   - STMS (Slide Together Multipath Scheduler, Shi et al., USENIX ATC 2018):
//     keep the HEAD of the stream on the fast leg — fill the fast leg's send
//     window first, so earlier data always rides the fast path — and only once
//     that window is full place the following (later) data on a slower leg,
//     picking the slow leg by soonest arrival so the pieces "slide together" and
//     converge in order at the receiver. The distinction from ECF is the
//     hold-back: ECF, when the fast leg is saturated, may DECLINE the slow leg
//     (hold the frame on the fast leg if the fast leg would drain before the
//     slow leg delivers even one frame — refusing to seed a reorder gap). STMS
//     does not decline; once a leg is in the active set STMS commits later data
//     to it and lets the reorder buffer absorb the gap. Its per-leg send window
//     is the BDP cwnd, adapted as the in-flight estimate drains at the leg's
//     send rate (the ack-progress proxy the mux already uses — skywire has no
//     per-leg ACK attribution on the wire, so both schedulers, like ECF, drain
//     against rate rather than a per-leg ack signal).

// schedDefaultRttMs is the fallback one-way/queueing RTT (ms) used by the
// arrival estimator when a leg has no RTT sample yet, so a cold leg still gets a
// finite arrival estimate instead of sorting to the front or the back
// arbitrarily.
const schedDefaultRttMs = 100.0

// legArrivalMs estimates, in milliseconds, when a frame handed to leg l right
// now would be DELIVERED to the receiver: the time to drain the leg's current
// in-flight backlog at its send rate (queueing delay) plus one-way propagation
// (≈ RTT/2). This is the shared OTIAS/STMS estimator, built entirely from the
// ecfLegState fields the mux already refreshes from legCounters.
//
// A leg with no rate sample yet (cold) is charged a nominal drain rate derived
// from the same bounded cold-probe budget ECF uses (ecfColdBootstrapBytes over
// one baseline RTT), so its queueing term still grows as it is charged and cold
// start fans out across the ready legs instead of dumping the whole stream on
// the single lowest-RTT leg until the first rate refresh.
func legArrivalMs(l ecfLegState) float64 {
	rtt := l.rttMs
	if rtt <= 0 {
		rtt = schedDefaultRttMs
	}
	rate := l.rateBps
	if rate <= 0 {
		rate = ecfColdBootstrapBytes / (rtt / 1000.0)
	}
	queueMs := 0.0
	if rate > 0 {
		queueMs = l.inflightBytes / rate * 1000.0
	}
	return queueMs + rtt/2.0
}

// otiasPick returns the ready leg with the earliest estimated arrival time, or
// -1 when no leg is ready (caller falls back to the schedule). Ties break toward
// the lower-index (primary/fast) leg so a cold flow's first frames ride the fast
// leg deterministically. Charging the chosen leg's in-flight bytes (done by the
// caller) is what makes successive picks fan out by arrival time.
func otiasPick(legs []ecfLegState) int {
	best := -1
	bestArr := math.MaxFloat64
	for i := range legs {
		if !legs[i].ready {
			continue
		}
		arr := legArrivalMs(legs[i])
		if best < 0 || arr < bestArr {
			best = i
			bestArr = arr
		}
	}
	return best
}

// stmsPick implements the Slide-Together placement: the head of the stream rides
// the fast leg while that leg's send window (BDP cwnd) has room; once the fast
// leg's window is full, the following data is committed to a slower leg — chosen
// by soonest arrival among ready legs that still have window room — so the
// pieces converge in order. Returns -1 when no leg is ready.
//
// The contrast with ecfPick is deliberate and is the whole point of STMS: where
// ECF, on a saturated fast leg, runs the hold-back predicate and may return the
// fast leg anyway (declining to seed the slow leg), STMS unconditionally spills
// to the best slow leg with capacity. It only falls back to the fast leg when NO
// slow leg has window room (every leg saturated) — then the frame queues on the
// fast leg's reliable transport rather than being held by the scheduler.
func stmsPick(legs []ecfLegState) int {
	// Fast leg = lowest-RTT ready leg; unknown-RTT legs are worst candidates.
	fast := -1
	for i := range legs {
		if !legs[i].ready {
			continue
		}
		if fast < 0 || ecfBetterRTT(legs[i], legs[fast]) { //nolint:gosec // fast is in range or <0
			fast = i
		}
	}
	if fast < 0 {
		return -1
	}
	// Earlier data on the fast leg: keep filling its send window first.
	if !ecfSaturated(legs[fast]) {
		return fast
	}
	// Fast window full: place the later data on the soonest-arriving slower leg
	// that still has window room. No hold-back decline (the ECF distinction).
	best := -1
	bestArr := math.MaxFloat64
	for i := range legs {
		if i == fast || !legs[i].ready || ecfSaturated(legs[i]) {
			continue
		}
		arr := legArrivalMs(legs[i])
		if best < 0 || arr < bestArr {
			best = i
			bestArr = arr
		}
	}
	if best < 0 {
		// Every ready leg saturated: queue on the fast leg's transport.
		return fast
	}
	return best
}
