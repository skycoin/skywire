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

// noteSBDProbation holds a pair apart for exactly d — a SHORT exemption for a
// pair whose park cannot be arbitrated (it was decided while the group was idle,
// so there is no pre-park rate to compare against). It is deliberately NOT
// noteSBDIndependent: nothing has been proven about these two legs, so the
// backoff must not double and the window must not outlast one trial. What it
// buys is one window of two-leg operation UNDER LOAD, after which the detector
// re-rules from loaded delay windows and any park it then makes carries a real
// base rate and a real trial. A standing verified-independent window (which is
// longer and was actually earned) is left alone.
func (rg *RouteGroup) noteSBDProbation(a, b uuid.UUID, d time.Duration) {
	if a == uuid.Nil || b == uuid.Nil || d <= 0 {
		return
	}
	key := sbdPair(a, b)
	rg.sbdTrialMu.Lock()
	defer rg.sbdTrialMu.Unlock()
	if rg.sbdIndependent == nil {
		rg.sbdIndependent = make(map[sbdPairKey]sbdSuppression)
	}
	until := time.Now().Add(d)
	if s, ok := rg.sbdIndependent[key]; ok && s.until.After(until) {
		return
	}
	rg.sbdIndependent[key] = sbdSuppression{until: until, span: rg.sbdIndependent[key].span}
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
	// idle marks a trial whose park was decided with the group carrying nothing:
	// there is no pre-park rate, so no measurement can ever refute it. It is
	// undone on the first loaded tick rather than allowed to stand.
	idle bool
}

// evaluateSBDTrials reads the verdict on every park trial that has run for at
// least SBDTrialWindow, given aggRate — the group's aggregate delivered-bytes
// rate (B/s) over the tick just sampled, measured with the parked legs OUT.
// A trial whose rate fell by more than SBDTrialLoss UNPARKS its leg and marks
// the pair verified-independent; any other trial simply ends and the park
// stands. Trials for legs that are gone are dropped. A trial whose park was
// decided while the group was IDLE has no bar to clear, so it is never ended
// against one: it stays open until traffic arrives and is then undone, which
// defers the A/B to loaded conditions instead of leaving the park permanent.
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
		if tr.baseRate <= 0 && aggRate <= 0 {
			// The park was decided on silence and there is still no traffic to judge
			// it by. Do NOT end the trial: a trial that ends against a base rate of
			// zero can never fail (sbdTrialFailed), so the park would stand for the
			// whole transfer that follows — the measured defect. It stays OPEN.
			continue
		}
		if now.Sub(tr.at) < window {
			continue
		}
		delete(rg.sbdTrials, id)
		if tr.baseRate <= 0 {
			// Traffic has arrived, but this park has no pre-park reading and never
			// will. Undo it and hold the pair apart for one window so the A/B is
			// simply DEFERRED to loaded conditions: the detector re-rules from loaded
			// delay windows and any re-park it makes is a real trial against a real
			// base rate. sbdMinEvidenceRate stops such a park being decided at all;
			// this is the belt for one already standing (an older peer's mirror, or a
			// park made before the floor was raised on a running visor).
			ripe = append(ripe, sbdTrialVerdict{tpID: id, keeper: tr.keeper, leg: i, idle: true})
			continue
		}
		ripe = append(ripe, sbdTrialVerdict{tpID: id, keeper: tr.keeper, leg: i, base: tr.baseRate,
			failed: sbdTrialFailed(tr.baseRate, aggRate, loss)})
	}
	rg.sbdTrialMu.Unlock()
	if len(ripe) == 0 {
		return
	}
	sort.Slice(ripe, func(a, b int) bool { return ripe[a].leg < ripe[b].leg })

	for _, v := range ripe {
		switch {
		case v.idle:
			// Long enough to cover a whole data-progress tick plus a trial: the
			// re-ruling must happen on LOADED delay windows, and this tick's
			// grouping runs immediately after this loop — a probation shorter than
			// the tick would let the detector re-park before a single loaded sample
			// existed, which is the flap the live run showed five seconds apart.
			span := legDataProgressInterval + window
			rg.noteSBDProbation(v.tpID, v.keeper, span)
			reason := fmt.Sprintf("shared-bottleneck park of leg %d was decided with the group carrying nothing (pre-park rate 0 B/s) — no measurement can refute it, so it is undone and the pair is held apart for %v while the detector re-rules under load",
				v.leg, span)
			rg.logger.Infof("%s", reason)
			rg.undoRefutedPark(v, tps, reason,
				"shared-bottleneck park had no evidence behind it, leg re-admitted for a loaded re-ruling")
		case !v.failed:
			rg.logger.Debugf("shared-bottleneck: park of leg %d stands — aggregate goodput %.0f→%.0f B/s over the %v trial, no capacity was lost",
				v.leg, v.base, aggRate, window)
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
				dropPct = (v.base - aggRate) / v.base * 100
			}
			reason := fmt.Sprintf("shared-bottleneck park trial FAILED: aggregate goodput %.0f→%.0f B/s (-%.1f%%, limit %.0f%%) with leg %d parked — the legs carry independent capacity; unparked, pair exempt from SBD for %v",
				v.base, aggRate, dropPct, loss*100, v.leg, span)
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
