// Package router pkg/router/sbd_trial.go c2-net-routing
//
// The route group's half of "a shared-bottleneck park is a trial": the pending
// trials, the verified-independent pair suppression, and the unpark a failed
// trial performs. The decision arithmetic and the constants live in
// bottleneck.go next to the detector whose ruling they qualify.
package router

import (
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/transport"
)

// sbdTrial is one standing park trial: when the park landed, which active leg
// the parked leg was judged redundant against, and the group's aggregate
// delivered-bytes rate (B/s) over the interval BEFORE the park — the bar the
// post-park rate is measured against.
type sbdTrial struct {
	keeper   uuid.UUID
	at       time.Time
	baseRate float64
}

// sbdSuppression is one pair's verified-independent window: until when no SBD
// ruling may merge the two legs, and the span that window was granted for (kept
// so the next failure doubles it).
type sbdSuppression struct {
	until time.Time
	span  time.Duration
}

// beginSBDTrial records that leg parked (transport ID) was just parked as
// redundant against keeper, with baseRate the aggregate goodput measured over
// the interval before the park. A leg already under trial keeps its ORIGINAL
// reading: re-recording it would move the bar to the already-degraded rate and
// the trial could never fail.
func (rg *RouteGroup) beginSBDTrial(parked, keeper uuid.UUID, baseRate float64) {
	if parked == uuid.Nil {
		return
	}
	rg.sbdTrialMu.Lock()
	defer rg.sbdTrialMu.Unlock()
	if rg.sbdTrials == nil {
		rg.sbdTrials = make(map[uuid.UUID]sbdTrial)
	}
	if _, running := rg.sbdTrials[parked]; running {
		return
	}
	rg.sbdTrials[parked] = sbdTrial{keeper: keeper, at: time.Now(), baseRate: baseRate}
}

// sbdSendDeltas returns each leg's SACK-acknowledged bytes since the previous
// call — the SEND path's delivered-bytes deltas for one data-progress tick,
// which sbdAggRate turns into the rate every shared-bottleneck decision is
// weighed against.
//
// The send path is the side the detector's delay samples come from (a frame's
// own send→ack round trip, see bottleneck.go), so it is the side its evidence
// floor has to be measured on. Reading the RECEIVE deltas instead compares a
// ruling with the opposite end of the transfer: a download is sent by the exit,
// which receives almost nothing, so the exit's own rulings looked idle for the
// whole transfer and the client's looked busy on samples it never took.
//
// A leg seen for the first time yields no delta (there is no baseline to
// difference), and a counter that did not move yields none either. The cumulative
// reads are taken BEFORE sbdTrialMu, which must not be held while another lock is
// taken.
func (rg *RouteGroup) sbdSendDeltas(tps []*transport.ManagedTransport) map[uuid.UUID]uint64 {
	out := make(map[uuid.UUID]uint64, len(tps))
	if rg.mux == nil || rg.mux.retxBuf == nil {
		return out
	}
	cur := make(map[uuid.UUID]uint64, len(tps))
	for _, tp := range tps {
		if tp == nil {
			continue
		}
		cur[tp.Entry.ID] = rg.mux.retxBuf.AckedBytes(tp.Entry.ID)
	}
	rg.sbdTrialMu.Lock()
	defer rg.sbdTrialMu.Unlock()
	if rg.sbdAckedPrev == nil {
		rg.sbdAckedPrev = make(map[uuid.UUID]uint64, len(tps))
	}
	for id, c := range cur {
		prev, seen := rg.sbdAckedPrev[id]
		rg.sbdAckedPrev[id] = c
		if seen && c > prev {
			out[id] = c - prev
		}
	}
	return out
}

// sbdSuppressed reports whether a failed trial already proved these two legs
// independent and the pair's window is still open.
func (rg *RouteGroup) sbdSuppressed(a, b uuid.UUID) bool {
	if a == uuid.Nil || b == uuid.Nil {
		return false
	}
	rg.sbdTrialMu.Lock()
	defer rg.sbdTrialMu.Unlock()
	s, ok := rg.sbdIndependent[sbdPair(a, b)]
	return ok && time.Now().Before(s.until)
}

// noteSBDIndependent opens (or re-opens, doubled) the verified-independent
// window for a pair whose park trial just failed, and returns the span granted.
// The previous span is remembered across expiry on purpose: a pair the detector
// keeps misjudging earns a longer exemption each time, exactly like the
// dead-route exclusion window.
func (rg *RouteGroup) noteSBDIndependent(a, b uuid.UUID) time.Duration {
	key := sbdPair(a, b)
	rg.sbdTrialMu.Lock()
	defer rg.sbdTrialMu.Unlock()
	if rg.sbdIndependent == nil {
		rg.sbdIndependent = make(map[sbdPairKey]sbdSuppression)
	}
	span := nextSBDBackoff(rg.sbdIndependent[key].span)
	rg.sbdIndependent[key] = sbdSuppression{until: time.Now().Add(span), span: span}
	return span
}

// sbdTrialVerdict is one ripe trial's outcome, resolved under the trial lock and
// acted on outside it (the unpark takes the mux and rg locks).
type sbdTrialVerdict struct {
	tpID   uuid.UUID
	keeper uuid.UUID
	leg    int
	base   float64
	failed bool
}

// evaluateSBDTrials reads the verdict on every park trial that has run for at
// least SBDTrialWindow, given rate — the group's SEND-path delivered-bytes rate
// (B/s) over the tick just sampled, measured with the parked legs OUT.
// A trial whose rate fell by more than SBDTrialLoss UNPARKS its leg and marks
// the pair verified-independent; any other trial simply ends and the park
// stands. Trials for legs that are gone are dropped.
//
// A tick BELOW sbdMinEvidenceRate returns no verdict at all: the trial arbitrates
// a rate, and a tick that carried nothing measures nothing. Two of the four
// compose-set parks on 2026-09-17 were refuted on such a tick — trial rate 2 B/s,
// between two bench rows — which is a statement about the gap between rows, not
// about the legs. The trial stays OPEN until the group is carrying something
// again, which is also what keeps a park from standing unarbitrated.
//
// Called at the top of enforceBottleneckGroups, before the detector rules again,
// so a leg the evidence just vindicated is active (and its pair suppressed)
// before this tick's grouping is computed.
func (rg *RouteGroup) evaluateSBDTrials(rate float64, tps []*transport.ManagedTransport) {
	if rg.mux == nil {
		return
	}
	idx := make(map[uuid.UUID]int, len(tps))
	for i, tp := range tps {
		if tp != nil {
			idx[tp.Entry.ID] = i
		}
	}

	now, window, loss := time.Now(), SBDTrialWindow(), SBDTrialLoss()
	floor := float64(SBDMinEvidenceRate())
	var ripe []sbdTrialVerdict
	rg.sbdTrialMu.Lock()
	for id, tr := range rg.sbdTrials {
		i, live := idx[id]
		if !live {
			delete(rg.sbdTrials, id) // the leg was removed; nothing to arbitrate
			continue
		}
		if rate < floor {
			// NO VERDICT ON AN IDLE TICK — the same floor the ruling itself had to
			// clear, applied to the measurement that arbitrates it. The trial stays
			// OPEN rather than ending either way.
			continue
		}
		if now.Sub(tr.at) < window {
			continue
		}
		delete(rg.sbdTrials, id)
		ripe = append(ripe, sbdTrialVerdict{tpID: id, keeper: tr.keeper, leg: i, base: tr.baseRate,
			failed: sbdTrialFailed(tr.baseRate, rate, loss)})
	}
	rg.sbdTrialMu.Unlock()
	if len(ripe) == 0 {
		return
	}
	sort.Slice(ripe, func(a, b int) bool { return ripe[a].leg < ripe[b].leg })

	for _, v := range ripe {
		switch {
		case !v.failed:
			rg.logger.Debugf("shared-bottleneck: park of leg %d stands — send-path goodput %.0f→%.0f B/s over the %v trial, no capacity was lost",
				v.leg, v.base, rate, window)
		default:
			// The suppression is recorded BEFORE the unpark and regardless of whether
			// the leg is still standby: the measurement refuted the RULING, which is
			// a fact about the pair, not about who happens to hold the leg. On the
			// live rig the peer mirrored a promote immediately after the first park,
			// so the leg was already active when the verdict came in — the old code
			// skipped the whole block, dropped the suppression, and the detector
			// re-parked on the very next tick.
			span := rg.noteSBDIndependent(v.tpID, v.keeper)
			dropPct := 0.0
			if v.base > 0 {
				dropPct = (v.base - rate) / v.base * 100
			}
			reason := fmt.Sprintf("shared-bottleneck park trial FAILED: send-path goodput %.0f→%.0f B/s (-%.1f%%, limit %.0f%%) with leg %d parked — the legs carry independent capacity; unparked, pair exempt from SBD for %v",
				v.base, rate, dropPct, loss*100, v.leg, span)
			rg.logger.Infof("%s", reason)
			rg.undoRefutedPark(v, tps, reason,
				"shared-bottleneck park trial failed: aggregate goodput fell, leg re-admitted")
		}
	}
}

// undoRefutedPark records a refuted park as a mux event and, if the leg is still
// parked, puts it back in the active set. The event is emitted either way: the
// refutation is what the pair's suppression window rests on, and a leg some other
// controller (or a peer's mirrored promote) already re-admitted needs no unpark,
// only the record.
//
// The unpark deliberately bypasses promoteLegAdaptive: its legParkMinHold gate
// exists to stop two controllers trading a leg inside one tick, not to hold a
// park the measurement just refuted — so the park record is cleared and the leg
// goes straight back into the active set, mirrored to the peer so the bulk
// SENDER stripes over it again (a download is exit-sent, and both ends run this
// code).
func (rg *RouteGroup) undoRefutedPark(v sbdTrialVerdict, tps []*transport.ManagedTransport, reason, promoted string) {
	var tp *transport.ManagedTransport
	if v.leg < len(tps) {
		tp = tps[v.leg]
	}
	rg.noteLegEvent(MuxEventParkTrialFailed, reason, MuxByAdaptive, v.leg, len(tps), tp, nil)
	if !rg.mux.isLegStandby(v.leg) {
		return // something already re-admitted it; nothing to undo
	}
	rg.mux.setLegStandby(v.leg, false)
	rg.clearAdaptivePark(v.tpID)
	rg.sendLegState(v.leg, false)
	rg.noteLegEvent(MuxEventLegPromoted, promoted, MuxByAdaptive, v.leg, len(tps), tp, nil)
}
