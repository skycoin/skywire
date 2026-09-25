// Package router pkg/router/route_mux_window.go c2-net-routing
package router

import (
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/transport"
)

// rebuildWeights updates transport selection weights based on current latency.
func (m *routeMux) rebuildWeights(tps []*transport.ManagedTransport) {
	if m.tpSelector == nil {
		return
	}
	// Capacity mode: feed the selector each leg's throughput since
	// the last rebuild (bytes sent+recv delta) so it can weight the
	// schedule toward the legs actually moving data. Computed here
	// (not in the selector) because the mux owns the per-leg byte
	// counters. The delta resets each rebuild, so the weights track
	// RECENT throughput, not lifetime totals (which would entrench
	// whichever leg carried first).
	if m.tpSelector.Mode() == WeightModeCapacity {
		m.legMu.Lock()
		// Per-leg recent throughput (bytes moved since the last rebuild).
		deltas := make([]float64, len(m.legs))
		active := make([]bool, len(m.legs))
		for i, lc := range m.legs {
			if lc == nil {
				continue
			}
			total := lc.sentBytes.Load() + lc.recvBytes.Load()
			delta := total - lc.lastTotalBytes
			lc.lastTotalBytes = total
			deltas[i] = float64(delta)
			// A warm-standby leg carries no send traffic — it must get zero weight
			// so the scheduler never steers a packet onto a parked leg (which the
			// receiver isn't expecting on that route and would stall the reorder
			// frontier on). Its byte counter was still sampled above so a later
			// promotion starts from a fresh delta, not a stale backlog.
			active[i] = !(i < len(m.standby) && m.standby[i])
		}
		// SHARED-BOTTLENECK COLLAPSE (RFC 8382): legs that funnel through the same
		// physical pipe (groupOf()) must be counted as ONE unit of capacity, not N
		// competing pipes — otherwise a 3-leg shared group out-weighs a lone
		// independent leg 3:1 and over-subscribes the one pipe while starving the
		// distinct route. For each group, ONE representative (the active member
		// moving the most bytes; ties → lowest index) carries the group's AGGREGATE
		// throughput (the pipe's real rate = sum of its active members' deltas); the
		// other members get zero send weight so the scheduler stops striping the same
		// pipe across redundant legs (which only adds reorder cost). Independent legs
		// are their own singleton group, so this is a no-op for them and the
		// pre-grouping behavior is unchanged when no bottleneck is detected.
		rep := make(map[int]int, len(m.legs)) // group id -> representative leg index
		groupSum := make(map[int]float64, len(m.legs))
		for i, lc := range m.legs {
			if lc == nil || !active[i] {
				continue
			}
			g := m.groupOf(i)
			groupSum[g] += deltas[i]
			cur, ok := rep[g]
			if !ok || deltas[i] > deltas[cur] {
				rep[g] = i
			}
		}
		weights := make([]float64, len(m.legs))
		var maxW float64
		for g, r := range rep {
			weights[r] = groupSum[g]
			if weights[r] > maxW {
				maxW = weights[r]
			}
		}
		// Cold-leg floor (the weighted-RAMP): a just-promoted representative has
		// moved ~no bytes yet, so its raw delta is ~0 — under pure capacity weighting
		// it would get ~no traffic and thus never accumulate the goodput it needs to
		// earn a real share (a starvation deadlock). Give every active group's
		// REPRESENTATIVE a floor share = capacityColdFloorFrac of the fastest group,
		// so a fresh pipe carries a THIN trickle, measures its goodput, and ramps up.
		// Applied per group (one floor unit per distinct pipe, never per redundant
		// co-bottlenecked leg). Skipped when every group is idle (maxW == 0) so an
		// idle mux doesn't manufacture phantom weight.
		if maxW > 0 {
			floor := maxW * capacityColdFloorFrac
			for _, r := range rep {
				if weights[r] < floor {
					weights[r] = floor
				}
			}
		}
		m.legMu.Unlock()
		m.tpSelector.SetCapacityWeights(weights)
	}
	// ECF mode: build the per-leg {rate, RTT, jitter, ready, BDP} snapshot the
	// predictive scheduler reasons over. Rate is the sent-byte delta over the
	// refresh window (computed here, not from snapshotLegs, so it works even
	// when nothing is observing the telemetry page). RTT is the leg's END-TO-END
	// feedback delay — max(first-hop tp.GetLatency(), the leg's measured
	// send→ack delay) — not the near-edge hop alone. Jitter is an EWMA of
	// |RTT-mean|, the ECF sigma margin.
	//
	// OTIAS and STMS reason over the SAME ecfLegState snapshot (rate + RTT +
	// jitter + BDP + the selector-tracked in-flight estimate), so this one
	// branch feeds all three predictive schedulers; only the per-frame pick in
	// the selector differs (ecfPick vs otiasPick vs stmsPick).
	if m.tpSelector.Mode().isPredictive() || (m.sackEnabled && m.retxBuf != nil) {
		m.refreshLegWindows(tps)
	}
	m.tpSelector.Rebuild(tps)
}

// refreshLegWindows recomputes each leg's ECF state — RTT/jitter EWMAs, the
// SACK-proven delivery rate and the send window it sizes — and hands it to
// the selector. Every mode with SACK accounting gets a window (the download
// sender on the rig ran the capacity mode and had none). Run from
// rebuildWeights and, so a window can grow between rebuilds, from the route
// group's send-window loop every windowRefreshInterval.
func (m *routeMux) refreshLegWindows(tps []*transport.ManagedTransport) {
	if m.tpSelector == nil {
		return
	}
	// Acked bytes are read BEFORE legMu is taken: the retx buffer's lock is
	// held by the SACK handler while it asks for per-leg thresholds, which
	// read the leg table — taking the two in the opposite order here deadlocked
	// the group (measured live 2026-09-16: a three-leg session froze mid-upload
	// with the refresh waiting on the buffer and the SACK handler on legMu).
	ackedByIdx := make([]uint64, len(tps))
	if m.retxBuf != nil {
		for i, tp := range tps {
			if tp != nil {
				ackedByIdx[i] = m.retxBuf.AckedBytes(tp.Entry.ID)
			}
		}
	}
	// Per-leg send→ack delay, read once here (outside legMu) and used for BOTH
	// the leg's RTT basis and the window's feedback delay below — one lock trip
	// instead of one per leg per use, and the same number in both places.
	adByIdx := make([]float64, len(tps))
	for i, tp := range tps {
		if tp != nil {
			adByIdx[i] = m.ackDelayMsTp(tp.Entry.ID)
		}
	}
	{
		m.legMu.Lock()
		now := time.Now().UnixNano()
		var elapsed float64
		if m.ecfLastRebuildNano != 0 {
			elapsed = float64(now-m.ecfLastRebuildNano) / float64(time.Second)
		}
		states := make([]ecfLegState, len(m.legs))
		for i, lc := range m.legs {
			if lc == nil {
				continue
			}
			// Send rate over the refresh window (bytes/sec).
			sent := lc.sentBytes.Load()
			var rate float64
			if elapsed > 0 {
				rate = float64(byteDelta(sent, lc.ecfLastSentBytes)) / elapsed
			}
			lc.ecfLastSentBytes = sent
			// RTT EWMA + jitter (sigma) EWMA, over the leg's END-TO-END feedback
			// delay: max(first-hop transport RTT, measured send→ack delay).
			//
			// The first-hop RTT alone is the wrong basis for every consumer of
			// this field. Measured live 2026-09-16 (exit sending, two pinned
			// legs): first-hop 10 ms via Amsterdam vs 95 ms via Atlanta, while
			// the real send→ack delay was ~170 ms on BOTH. ECF's hold-back rule
			// (n*rttF < hyst*(rttS+d)) then needed n≈9.5 frames queued before it
			// would spill to the second leg (split 41.5/9.5 MB of a 50 MB
			// download), and RACK's threshold — derived from the same short
			// basis via maxActiveLegRTTms — declared the in-flight frames lost
			// when it finally did (199 retransmit bursts / 1824 packets over
			// five rows, wire/goodput up to 1.19). The window sizing below
			// already used max(baseline, ack delay); this makes the whole leg
			// state agree with it. The first-hop value stays the FLOOR, so a
			// leg with no ack sample yet behaves exactly as before.
			var rttMs, hopMs float64
			if i < len(tps) && tps[i] != nil {
				hopMs = tps[i].GetLatency()
				rttMs = hopMs
				if ad := adByIdx[i]; ad > rttMs {
					rttMs = ad
				}
			}
			// The first-hop minimum is tracked on its own, with the same creep:
			// it is the BDP baseline's ceiling and must not be reachable from
			// the combined value.
			if hopMs > 0 {
				switch {
				case lc.ecfHopRttMinMs == 0 || hopMs < lc.ecfHopRttMinMs:
					lc.ecfHopRttMinMs = hopMs
				default:
					lc.ecfHopRttMinMs += routersettings.EcfRttMinCreep.Ratio() * (hopMs - lc.ecfHopRttMinMs)
				}
			}
			if rttMs > 0 {
				if lc.ecfRttMs == 0 {
					lc.ecfRttMs = rttMs
					lc.ecfRttMinMs = rttMs
				} else {
					dev := rttMs - lc.ecfRttMs
					if dev < 0 {
						dev = -dev
					}
					lc.ecfJitterMs = routersettings.EcfJitterAlpha.Ratio()*dev + (1-routersettings.EcfJitterAlpha.Ratio())*lc.ecfJitterMs
					lc.ecfRttMs = routersettings.EcfRttAlpha.Ratio()*rttMs + (1-routersettings.EcfRttAlpha.Ratio())*lc.ecfRttMs
					// Baseline RTT = running minimum of the SAME end-to-end basis,
					// with a slow upward creep: a transient congestion spike never
					// raises it, but a leg whose true latency rose for good is
					// eventually tracked. It must track the combined value, not the
					// first hop — ecfSaturated judges the live rtt against it, and a
					// first-hop baseline beside an end-to-end live value would read
					// as permanent congestion.
					if rttMs < lc.ecfRttMinMs {
						lc.ecfRttMinMs = rttMs
					} else {
						lc.ecfRttMinMs += routersettings.EcfRttMinCreep.Ratio() * (lc.ecfRttMs - lc.ecfRttMinMs)
					}
				}
			}
			// BDP latency = the baseline (uncongested) RTT, never the live RTT,
			// so a stalling leg's inflating RTT cannot grow its own cwnd and pull
			// more traffic onto itself.
			// ...and it is the FIRST-HOP running minimum, not the combined
			// end-to-end one #4970 put on ecfRttMinMs. The combined baseline
			// tracks the send→ack EWMA, which rises with a queue THIS window
			// created and (having no time decay) carried that queue into the next
			// transfer: measured live 2026-09-17, five consecutive 50 MB uploads
			// over ONE leg through a 470 ms intermediate fell 3.90 → 3.05 → 2.80
			// → 1.59 → 0.62 MB/s with ZERO loss (retx 6→28, send_window_waits 0)
			// while the leg's combined delay reading ratcheted to 13435 ms.
			//
			// The first-hop latency is the one delay on this leg our own window
			// cannot inflate — the transport measures it out of band — so it is
			// the stable floor the window is anchored to. ecfRttMs / rttMinMs keep
			// the combined value for ecfPick, the RACK threshold and the latency
			// band, which is what #4970 was for; only the BDP anchor comes back.
			bdpRttMs := lc.ecfHopRttMinMs
			if bdpRttMs <= 0 {
				bdpRttMs = lc.ecfRttMinMs
			}
			if bdpRttMs <= 0 {
				bdpRttMs = lc.ecfRttMs
			}
			// The send window is what the peer's SACKs PROVE the leg delivers per
			// baseline RTT (× a growth margin), not what we managed to hand the
			// transport: the send rate counts bytes queued into a bloated leg as
			// capacity, which is how a slow leg was fed seconds deep. Cold legs
			// (no acked bytes yet) keep the send-rate BDP and the probe budget.
			cwnd := rate * bdpRttMs / 1000.0
			if m.retxBuf != nil && i < len(tps) && tps[i] != nil {
				acked := ackedByIdx[i]
				switch {
				case acked == 0:
					// cold: nothing acknowledged yet, send-rate BDP + probe budget
				case lc.ecfLastAckedBytes == 0:
					lc.ecfLastAckedBytes = acked
					lc.ecfLastAckedNano = now
				default:
					// The window changes only on EVIDENCE: the delivery rate is the
					// bytes acknowledged since the last refresh that saw an ack, over
					// the time since that refresh. A refresh with no new ack (the
					// SACK cadence is coarser than the refresh at low rates) keeps
					// the last proven window instead of falling back to the send-rate
					// estimate, which would have counted queued bytes as capacity.
					delta := byteDelta(acked, lc.ecfLastAckedBytes)
					if dt := float64(now-lc.ecfLastAckedNano) / float64(time.Second); delta > 0 && dt > 0 {
						deliv := float64(delta) / dt
						// The delivery rate is measured per FEEDBACK delay (send→SACK,
						// which the delayed ack and the SACK cadence stretch past the
						// ping RTT), so the window must be sized over that delay too:
						// sized over the shorter ping RTT it shrank every refresh
						// under load (measured: uploads on a 2-tunnel session fell
						// from 9.7 to 4.5 MB/s with 750 writer parks, and clamping it
						// to a first-hop multiple instead collapsed a 470 ms path to
						// the 128 KiB floor: 0.23 MB/s on a 50 MB upload, wire/
						// goodput 1.00, the starved window shrinking the delivery
						// that sizes it). The ack delay only ever WIDENS the window
						// here, and ackDelayStale expires it once the transfer ends,
						// so the queue one transfer built is not the next one's
						// basis — which is the ratchet, not this max.
						fbMs := bdpRttMs
						if ad := adByIdx[i]; ad > fbMs {
							fbMs = ad
						}
						cwnd = deliv * fbMs / 1000.0 * EcfWindowMargin()
						lc.ecfCwndBytes = cwnd
						lc.ecfLastAckedBytes = acked
						lc.ecfLastAckedNano = now
						lc.ecfDelivBps = foldDeliv(lc.ecfDelivBps, deliv)
					} else if lc.ecfCwndBytes > 0 {
						cwnd = lc.ecfCwndBytes
					}
					// A leg that has gone quiet folds a ZERO into its delivery
					// EWMA once the silence passes a second, so the gate reads
					// "delivering nothing now" instead of the last rate it proved
					// before it stalled. The WINDOW is deliberately left alone —
					// it changes only on ack evidence (above).
					if now-lc.ecfLastAckedNano > int64(time.Second) {
						lc.ecfDelivBps = foldDeliv(lc.ecfDelivBps, 0)
					}
				}
			}
			// Clamp EVERY path to the window bounds, not just the evidence branch
			// above: a cold leg (nothing acked yet), the first-ack seeding branch
			// and a group with no retx buffer all reached here with a raw
			// rate×BDP window and no ceiling, so a just-promoted leg was handed an
			// unbounded send window and over-subscribed the no-skip frontier.
			if lo := float64(EcfMinWindowBytes()); cwnd < lo {
				cwnd = lo
			}
			if hi := float64(EcfMaxWindowBytes()); cwnd > hi {
				cwnd = hi
			}
			ready := true
			if i < len(m.standby) && m.standby[i] {
				ready = false
			}
			if i < len(m.ready) && !m.ready[i] {
				ready = false
			}
			states[i] = ecfLegState{
				rttMs:      lc.ecfRttMs,
				rttMinMs:   lc.ecfRttMinMs,
				jitterMs:   lc.ecfJitterMs,
				rateBps:    rate,
				cwndBytes:  cwnd,
				ready:      ready,
				delivBps:   lc.ecfDelivBps,
				delivKnown: lc.ecfLastAckedNano != 0,
			}
		}
		rulings := m.ruleProbeOnlyLegsLocked(states)
		m.ecfLastRebuildNano = now
		m.legMu.Unlock()
		m.tpSelector.SetECFState(states)
		m.reportProbeRulings(tps, rulings)
	}
}

// legProbeRuling is one leg crossing into — or back out of — the probe-only
// state, carried out of the window refresh so the event is recorded with legMu
// dropped.
type legProbeRuling struct {
	idx       int
	probeOnly bool
	reason    string
}

// ruleProbeOnlyLegsLocked decides, once per window refresh, which legs are so
// far behind the group's best ACTIVE leg that they may carry no more than a
// probe per window.
//
// The download on a two-leg group is not scheduled by ECF at all: under
// CapUniDir the reverse direction is picked by selectByDirection, whose tier 1
// is the mirrored round-robin schedule (transportSelector.Rebuild puts the live
// legs in ts.schedule unweighted for every predictive mode), so a leg keeps
// taking every other frame no matter what its delay basis says. Measured live
// 2026-09-18 (bench/2026-09-18/1008cc8e5, compose 2x2): group rg49220 paired a
// 7.4 MB/s leg with one whose own reference ran at 0.06 MB/s, the scheduler put
// 0.5-2.2 MB of each 10 MB download on the slow one, and the five trials took
// 8.3-37.4 s — each one's duration is the bytes placed on the slow leg divided
// by its ~60 KB/s. The group's own detector had the number: an sbd_ruling in
// the same run read "mean 755.9 vs 9291.3 ms over 8/8 samples".
//
// A DELAY reading alone is not enough to rule on, and the first live run
// (bench/2026-09-18/9f4848dfa-smoke) proved it: on legs-2 the healthy 166 ms leg
// beside the 44 ms one was cut five times — "322 ms against 53 ms", "270 vs 43",
// "613 vs 86", "833 vs 122", "843 vs 125" — and the set fell to x0.854 on a
// 50 MB download from x1.0-1.08. Under ECF a slower leg carrying its share sits
// at 6-7x on the send→ack basis routinely, because that basis is window/rate:
// the queue is OUR OWN and the leg was delivering 27-65 % of the bytes while it
// read that way (carrier rows 11-13: 18.5/37.2, 10.7/39.4, 33.7/18.0 MB).
//
// So the ruling takes TWO readings, and a leg has to fail both:
//
//	DELAY — ecfRttMs, the END-TO-END feedback delay (max of first-hop RTT and
//	   measured send→ack delay), above legProbeMinBasisMs in absolute terms and
//	   more than LegStarveRatio times the best ready leg's.
//	GOODPUT — ecfDelivBps, what the peer's SACKs PROVE the leg delivered, below
//	   1/LegStarveRatio of the best ready leg's. The same ratio serves both ends:
//	   a leg that is slow but PRODUCTIVE keeps its share.
//
// The AG case clears both by a wide margin: 9291 ms against 756 ms (12.3x) while
// delivering ~60 KB/s against ~7 MB/s (0.9 % — the goodput test wants under
// 16.7 %). The healthy legs-2 pair fails the second: 6-7x on delay, but 27 % of
// the bytes at worst. A leg with no ack of its own yet (delivKnown false) is
// never ruled — cold is not the same as unproductive.
//
// A probe-only leg is not parked: it keeps its rules, keeps being measured by
// its probe, and its basis decays back toward the first-hop RTT once we stop
// queueing on it (ackDelayStale expires the send→ack term), so the ruling lifts
// by itself when the leg recovers.
//
// Every reading here comes from THIS group's own leg table — states is built
// from m.legs, one routeMux per RouteGroup — so a leg is only ever judged
// against its siblings in the same route group.
//
// Caller holds legMu; returns the transitions for reportProbeRulings to record.
func (m *routeMux) ruleProbeOnlyLegsLocked(states []ecfLegState) []legProbeRuling {
	ratio := LegStarveRatio()
	best, bestDeliv := 0.0, 0.0
	for i := range states {
		if !states[i].ready {
			continue
		}
		if states[i].rttMs > 0 && (best == 0 || states[i].rttMs < best) {
			best = states[i].rttMs
		}
		if states[i].delivKnown && states[i].delivBps > bestDeliv {
			bestDeliv = states[i].delivBps
		}
	}
	var out []legProbeRuling
	for i, lc := range m.legs {
		if lc == nil || i >= len(states) {
			continue
		}
		basis, deliv := states[i].rttMs, states[i].delivBps
		outclassedByDelay := basis >= m.knRatio(routersettings.LegProbeMinBasisMs) && basis > ratio*best
		unproductive := states[i].delivKnown && bestDeliv > 0 && deliv*ratio < bestDeliv
		probeOnly := ratio > 1 && best > 0 && states[i].ready &&
			outclassedByDelay && unproductive
		was := math.Float64frombits(lc.probeBasisBits.Load()) > 0
		switch {
		case probeOnly:
			lc.probeBasisBits.Store(math.Float64bits(basis))
		default:
			lc.probeBasisBits.Store(0)
		}
		if probeOnly == was {
			continue
		}
		r := legProbeRuling{idx: i, probeOnly: probeOnly}
		if probeOnly {
			r.reason = fmt.Sprintf("delay basis %.0f ms against the best active leg's %.0f ms (more than %.1fx) AND delivering %.0f B/s against its %.0f B/s (under 1/%.1f) — capped at %d bytes per %.0f ms window instead of a proportional share; not parked, the probe keeps measuring it",
				basis, best, ratio, deliv, bestDeliv, ratio, LegProbeBytes(), m.probeWindowMs(basis))
		} else {
			why := fmt.Sprintf("delay basis %.0f ms is back within %.1fx of the best active leg's %.0f ms", basis, ratio, best)
			if outclassedByDelay {
				why = fmt.Sprintf("delivering %.0f B/s against the best active leg's %.0f B/s — slow but productive", deliv, bestDeliv)
			}
			r.reason = why + " — full share restored"
		}
		out = append(out, r)
	}
	return out
}

// reportProbeRulings records each probe-only transition as a mux event. Called
// with legMu dropped; the hook is the route group's noteLegEvent, which takes no
// locks of its own.
func (m *routeMux) reportProbeRulings(tps []*transport.ManagedTransport, rulings []legProbeRuling) {
	if len(rulings) == 0 || m.onLegProbeRuling == nil {
		return
	}
	for _, r := range rulings {
		var tp *transport.ManagedTransport
		if r.idx < len(tps) {
			tp = tps[r.idx]
		}
		m.onLegProbeRuling(r.idx, len(tps), tp, r.probeOnly, r.reason)
	}
}

// SetLegProbeRulingFn wires the callback the mux fires when a leg is ruled
// probe-only or restored to a full share, so the route group can record a
// leg_probe_only / leg_full_share mux event. Called once by the route group
// when the mux is built.
func (m *routeMux) SetLegProbeRulingFn(fn func(idx, legs int, tp *transport.ManagedTransport, probeOnly bool, reason string)) {
	m.onLegProbeRuling = fn
}

// foldDeliv folds one delivery-rate sample into a leg's EWMA. Smoothed because
// the ruling reads it against a sibling's: an instantaneous rate over one
// ~100 ms refresh swings far enough for a productive leg to look idle for a
// tick, and the cost of a wrong ruling is a starved leg.
func foldDeliv(prev, sample float64) float64 {
	if prev <= 0 {
		return sample
	}
	a := LegDelivAlpha()
	return a*sample + (1-a)*prev
}

// probeWindowMs is how long one probe budget lasts on a leg with this delay
// basis: the leg's own basis, floored at legProbeMinWindow so a fast-but-thin
// leg is not re-probed thousands of times a second.
func (m *routeMux) probeWindowMs(basisMs float64) float64 {
	if lo := float64(m.knDur(routersettings.LegProbeMinWindow)) / float64(time.Millisecond); basisMs < lo {
		return lo
	}
	return basisMs
}

// legProbeExhausted reports whether leg idx is ruled probe-only AND has already
// carried its probe budget in the current window. It is the selection gate: a
// leg it answers true for is skipped while any other leg can take the frame,
// so an outclassed leg is fed a probe's worth per window rather than a
// proportional share. False for every leg that is not ruled probe-only, so a
// group of comparable legs picks exactly as it did before.
//
// The window rolls here (the first pick past its end resets the byte count)
// rather than on the refresh tick, so the budget is paced by the leg's own
// delay basis and not by windowRefreshInterval.
func (m *routeMux) legProbeExhausted(idx int) bool {
	if idx < 0 {
		return false
	}
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	if idx >= len(m.legs) || m.legs[idx] == nil {
		return false
	}
	lc := m.legs[idx]
	basis := math.Float64frombits(lc.probeBasisBits.Load())
	if basis <= 0 {
		return false
	}
	now := time.Now().UnixNano()
	win := int64(m.probeWindowMs(basis) * float64(time.Millisecond))
	start := lc.probeWinNano.Load()
	if now-start >= win && lc.probeWinNano.CompareAndSwap(start, now) {
		lc.probeWinBytes.Store(0)
	}
	return lc.probeWinBytes.Load() >= uint64(LegProbeBytes()) //nolint:gosec // LegProbeBytes is refused unless positive
}

// firstProbeReadyLeg returns the lowest-indexed live, ready leg that is not out
// of probe budget, or -1 when every ready leg is. Used to move a schedule pick
// off an outclassed leg.
func (m *routeMux) firstProbeReadyLeg(tps []*transport.ManagedTransport) int {
	for idx, tp := range tps {
		if tp == nil || tp.IsClosed() || !m.legReadyAt(idx) {
			continue
		}
		if !m.legProbeExhausted(idx) {
			return idx
		}
	}
	return -1
}

// feedInflight hands the predictive selector each leg's REAL unacknowledged
// bytes (from the retx buffer, attributed per transport) before a pick, so
// ecfSaturated judges true backlog rather than a rate-drain estimate.
func (m *routeMux) feedInflight(tps []*transport.ManagedTransport) {
	if m.retxBuf == nil || !m.sackEnabled || m.tpSelector == nil {
		return
	}
	ids := make([]uuid.UUID, len(tps))
	for i, tp := range tps {
		if tp != nil {
			ids[i] = tp.Entry.ID
		}
	}
	m.tpSelector.SetInflight(m.retxBuf.HeldBytes(ids))
}

// signalWindow wakes a writer parked in waitSendWindow (coalescing: one pending
// wake-up at a time). Called when a SACK purged entries or a leg's standby
// state changed.
func (m *routeMux) signalWindow() {
	if m.windowCh == nil {
		return
	}
	select {
	case m.windowCh <- struct{}{}:
	default:
	}
}

// sendWindowBlocked reports whether the next frame has nowhere to go under the
// send windows in force. For a FORWARD-confined direction that is one question
// — is the CONFINED leg at its window — because the frame is going on that leg
// and no other: with --forward-spill off (the default) a full window is a
// reason to wait, not a reason to spray the upload onto a slower leg. Every
// other case keeps the original test, every ready leg saturated.
//
// Called from waitSendWindow, which runs on the writer with the route group's
// mu DROPPED, so it reads the confined leg from the atomic mirror.
//
// While the direction is FANNED OUT under load the question widens by exactly
// the legs the fan-out may use: the writer is released as soon as the confined
// leg or an eligible sibling has room, and still parks when neither does. It is
// not AllReadySaturated — a leg outside the skew band has room the upload is
// not allowed to use, and releasing the writer against it would put the frame
// on the confined leg's full window instead.
func (m *routeMux) sendWindowBlocked(tps []*transport.ManagedTransport) bool {
	if !ForwardSpill() {
		if idx := m.confinedForwardIdx(); idx >= 0 && m.forwardSender() {
			if m.forwardFanoutActive() {
				return !m.forwardFanoutRoom(tps, idx)
			}
			return m.tpSelector.Saturated(idx)
		}
	}
	return m.tpSelector.AllReadySaturated()
}

// waitSendWindow parks a writer while the leg(s) it may use are at their
// in-flight window, until a SACK frees capacity, the group closes, or
// sendWindowWaitMax elapses. A no-op unless SACK accounting and a predictive
// scheduler are on.
func (m *routeMux) waitSendWindow(tps []*transport.ManagedTransport, closed <-chan struct{}) {
	if m.retxBuf == nil || !m.sackEnabled || m.tpSelector == nil {
		return
	}
	deadline := time.Now().Add(SendWindowWaitMax())
	waited := false
	for {
		m.feedInflight(tps)
		// One observation of the confined leg's window per poll, blocked or
		// not: this loop is the only place that sees the forward writer's
		// demand with the in-flight estimates fresh, so it is where the
		// load-triggered fan-out engages and releases.
		m.noteForwardLoad(tps)
		if !m.sendWindowBlocked(tps) {
			return
		}
		if !waited {
			m.sendWindowWaits.Add(1)
			waited = true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			m.sendWindowTimeouts.Add(1)
			return
		}
		if remaining > m.knDur(routersettings.SendWindowPoll) {
			remaining = m.knDur(routersettings.SendWindowPoll)
		}
		t := time.NewTimer(remaining)
		select {
		case <-m.windowCh:
		case <-t.C:
		case <-closed:
			t.Stop()
			return
		}
		t.Stop()
	}
}
