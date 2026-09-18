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
// least SBDTrialWindow, given aggRate — the group's aggregate delivered-bytes
// rate (B/s) over the tick just sampled, measured with the parked legs OUT.
// A trial whose rate fell by more than SBDTrialLoss UNPARKS its leg and marks
// the pair verified-independent; any other trial simply ends and the park
// stands. Trials for legs that are gone are dropped.
//
// Called at the top of enforceBottleneckGroups, before the detector rules again,
// so a leg the evidence just vindicated is active (and its pair suppressed)
// before this tick's grouping is computed.
func (rg *RouteGroup) evaluateSBDTrials(aggRate float64, tps []*transport.ManagedTransport) {
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
	var ripe []sbdTrialVerdict
	rg.sbdTrialMu.Lock()
	for id, tr := range rg.sbdTrials {
		i, live := idx[id]
		if !live {
			delete(rg.sbdTrials, id) // the leg was removed; nothing to arbitrate
			continue
		}
		if now.Sub(tr.at) < window {
			continue
		}
		delete(rg.sbdTrials, id)
		ripe = append(ripe, sbdTrialVerdict{tpID: id, keeper: tr.keeper, leg: i, base: tr.baseRate,
			failed: sbdTrialFailed(tr.baseRate, aggRate, loss)})
	}
	rg.sbdTrialMu.Unlock()
	if len(ripe) == 0 {
		return
	}
	sort.Slice(ripe, func(a, b int) bool { return ripe[a].leg < ripe[b].leg })

	for _, v := range ripe {
		if !v.failed {
			rg.logger.Debugf("shared-bottleneck: park of leg %d stands — aggregate goodput %.0f→%.0f B/s over the %v trial, no capacity was lost",
				v.leg, v.base, aggRate, window)
			continue
		}
		if !rg.mux.isLegStandby(v.leg) {
			continue // something already re-admitted it; nothing to undo
		}
		span := rg.noteSBDIndependent(v.tpID, v.keeper)
		dropPct := 0.0
		if v.base > 0 {
			dropPct = (v.base - aggRate) / v.base * 100
		}
		reason := fmt.Sprintf("shared-bottleneck park trial FAILED: aggregate goodput %.0f→%.0f B/s (-%.1f%%, limit %.0f%%) with leg %d parked — the legs carry independent capacity; unparked, pair exempt from SBD for %v",
			v.base, aggRate, dropPct, loss*100, v.leg, span)
		rg.logger.Infof("%s", reason)

		// Undo the park. This deliberately bypasses promoteLegAdaptive: its
		// legParkMinHold gate exists to stop two controllers trading a leg inside
		// one tick, not to hold a park the measurement just refuted — so the park
		// record is cleared and the leg goes straight back into the active set,
		// mirrored to the peer so the bulk SENDER stripes over it again (a
		// download is exit-sent, and both ends run this code).
		rg.mux.setLegStandby(v.leg, false)
		rg.clearAdaptivePark(v.tpID)
		rg.sendLegState(v.leg, false)

		var tp *transport.ManagedTransport
		if v.leg < len(tps) {
			tp = tps[v.leg]
		}
		rg.noteLegEvent(MuxEventParkTrialFailed, reason, MuxByAdaptive, v.leg, len(tps), tp, nil)
		rg.noteLegEvent(MuxEventLegPromoted, "shared-bottleneck park trial failed: aggregate goodput fell, leg re-admitted",
			MuxByAdaptive, v.leg, len(tps), tp, nil)
	}
}
