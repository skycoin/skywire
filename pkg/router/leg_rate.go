// Package router pkg/router/leg_rate.go c2-net-routing
//
// A BBR-style path model per mux leg: the two quantities a scheduler actually
// needs about a path, measured separately, instead of the one number it had.
//
// Every scheduler failure this week came from judging a leg by its send→ack
// DELAY BASIS, which is queue depth in disguise. A basis is window/rate: put a
// megabyte on a leg and its basis rises whatever the path is doing, so a
// healthy 166 ms leg beside a 44 ms one reads 6-7x while it is delivering a
// third of the bytes (bench/2026-09-18/9f4848dfa-smoke, legs-2 rows 11-13), and
// #5037 had to bolt a delivered-goodput term onto the probe-only ruling before
// that leg stopped being cut. ECF's hold-back predicate, the RACK thresholds,
// the probe ruling and SBD all read the same conflated number.
//
// BBR (Cardwell et al.) separates them:
//
//	BtlBw  — the bottleneck bandwidth: a windowed MAX filter of the measured
//	         delivery rate (bytes the peer's SACKs acknowledged / the interval
//	         they were acknowledged over). A max filter because a delivery-rate
//	         sample can only ever UNDER-state the path (the sender may not have
//	         had data to send), never overstate it.
//	RTprop — the round-trip propagation delay: a windowed MIN filter of the
//	         path's RTT samples. A min filter because queueing delay only ever
//	         ADDS, so the smallest recent sample is the closest estimate of the
//	         delay our own window cannot inflate.
//
// From the pair: BDP = BtlBw x RTprop (the bytes in flight that fill the pipe
// exactly once), and QUEUE = inflight / BtlBw (how long the bytes already on
// the leg take to drain — the honest queueing delay, which is what the delay
// basis was being asked for and answered badly).
//
// Two rules make the estimates usable:
//
//   - APP-LIMITED samples. When the sender had less data outstanding than the
//     leg's window allowed, the measured delivery rate is the application's
//     rate, not the path's. Such a sample may only RAISE BtlBw (a rate we
//     actually achieved is a lower bound on the path whatever limited us), never
//     lower it — otherwise an idle moment would erase a leg's capacity estimate
//     and the scheduler would shed a perfectly good path.
//   - RTT SOURCE. The min filter is fed the leg's OUT-OF-BAND path measurement —
//     the end-to-end liveness pong across all hops, or the first-hop transport
//     RTT when the leg has no pong yet — and NOT the send→ack delay. The
//     send→ack series is the one our own queue inflates; on a leg under
//     sustained load its minimum is queue-contaminated, which is exactly the
//     ratchet that walked one leg's reading to 13435 ms over five uploads
//     (#5011). The pong shares the leg's FIFO queue and swings, but its MINIMUM
//     over a window is the standard escape, the same one legRTTWindow takes for
//     the latency band.
package router

import (
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/router/routersettings"
)

// Path-model filter shape. The delivery-rate max filter is windowed in ROUND
// TRIPS (BBR's unit: capacity is a per-round property, and a leg whose RTT is
// 400 ms must not have its estimate expired by a wall clock tuned for a 40 ms
// one), the RTT min filter in wall-clock seconds (a path's propagation delay
// changes on a route-change timescale, not a per-round one).
//
// pathBDPGain is the cwnd_gain the send window targets: gain x BtlBw x RTprop,
// two being BBR's own (enough in-flight to keep the bottleneck busy through one
// round of feedback delay). It is applied as a FLOOR under the window the
// SACK-proven delivery already sizes — see refreshLegWindows — so turning the
// model on can only stop a window collapsing, never shrink one; 0 removes the
// term entirely and the window is sized exactly as it was before this file.
const (
	pathBtlBwWindowRoundsDefault = 10
	pathRTpropWindowDefault      = 10 * time.Second
	pathAppLimitedFracDefault    = 0.75
	pathBDPGainDefault           = 2.0
	// pathSamplesCap bounds each filter's retained samples. Both are fed from
	// the window refresh (~10 Hz), far under this, but a faster sampler must not
	// grow them without limit.
	pathSamplesCap = 64
)

// rateSample is one delivery-rate observation tagged with the round trip it was
// taken in; rttSample is one path-RTT observation tagged with its arrival time.
type rateSample struct {
	bps   float64
	round uint64
}

type rttSample struct {
	ms float64
	at int64 // UnixNano
}

// legPathModel is one leg's BBR-style path model. Not safe for concurrent use:
// it is written only by refreshLegWindows and read only by snapshot readers,
// both under the mux's legMu (it lives inside legCounters).
type legPathModel struct {
	// rates is the delivery-rate max filter, rtts the path-RTT min filter.
	rates []rateSample
	rtts  []rttSample
	// round counts elapsed round trips for the rate filter's window;
	// roundStartNano opens the current one.
	round          uint64
	roundStartNano int64
	// delivBps is the most recent delivery-rate sample (NOT the filter), kept so
	// a reader can tell "delivering nothing right now" from "capacity unknown";
	// inflightBytes is the leg's real unacknowledged bytes at that sample and
	// appLimited says the sender was not window-limited when it was taken.
	delivBps      float64
	inflightBytes float64
	appLimited    bool
}

// legPathSnapshot is the model as an observer reads it — `mux info` per leg and
// the tests. Known is false until the leg has both a capacity and a delay
// estimate, which is the gate every consumer uses to fall back to the
// pre-model behavior rather than reason from a half-measured path.
type legPathSnapshot struct {
	BtlBwBps      float64 `json:"btlbw_bps"`
	RTpropMS      float64 `json:"rtprop_ms"`
	BDPBytes      float64 `json:"bdp_bytes"`
	InflightBytes float64 `json:"inflight_bytes"`
	QueueMS       float64 `json:"queue_ms"`
	AppLimited    bool    `json:"app_limited"`
	Known         bool    `json:"known"`
}

// advanceRound closes the current round trip once one RTprop has elapsed (the
// refresh cadence flooring it, so a leg with no RTT estimate yet still ages its
// filter). BBR counts a round as "the data outstanding when the round opened is
// now acknowledged"; the mux has no per-round delivered counter on the wire, and
// one RTprop of wall clock is the same interval by definition.
func (pm *legPathModel) advanceRound(now int64, floor time.Duration) {
	if pm.roundStartNano == 0 {
		pm.roundStartNano = now
		return
	}
	d := time.Duration(pm.rtpropMs() * float64(time.Millisecond))
	if d < floor {
		d = floor
	}
	if now-pm.roundStartNano >= int64(d) {
		pm.round++
		pm.roundStartNano = now
	}
}

// sampleRTT folds one PATH round-trip sample (ms) into the min filter and drops
// everything older than the window. Non-positive samples are ignored.
func (pm *legPathModel) sampleRTT(ms float64, now int64, window time.Duration) {
	if ms <= 0 {
		return
	}
	pm.rtts = append(pm.rtts, rttSample{ms: ms, at: now})
	cutoff := now - int64(window)
	i := 0
	for i < len(pm.rtts) && pm.rtts[i].at < cutoff {
		i++
	}
	if i > 0 {
		pm.rtts = append(pm.rtts[:0], pm.rtts[i:]...)
	}
	if over := len(pm.rtts) - pathSamplesCap; over > 0 {
		pm.rtts = append(pm.rtts[:0], pm.rtts[over:]...)
	}
}

// sampleDelivery folds one delivery-rate sample (bytes/sec) into the max filter
// and drops everything older than windowRounds round trips. inflight is the
// leg's unacknowledged bytes when the sample was taken and window the send
// window it was allowed; the sample is APP-LIMITED when the sender was holding
// well under that window, and an app-limited sample may only raise the estimate
// (see the file comment).
func (pm *legPathModel) sampleDelivery(bps, inflight, window float64, windowRounds uint64) {
	pm.delivBps = bps
	pm.inflightBytes = inflight
	pm.appLimited = window > 0 && inflight < routersettings.PathAppLimitedFrac.Ratio()*window
	if bps <= 0 {
		return
	}
	if pm.appLimited && bps <= pm.btlbwBps() {
		// An idle moment cannot lower a leg's measured capacity.
		return
	}
	pm.rates = append(pm.rates, rateSample{bps: bps, round: pm.round})
	if windowRounds > 0 {
		var cutoff uint64
		if pm.round > windowRounds {
			cutoff = pm.round - windowRounds
		}
		i := 0
		for i < len(pm.rates) && pm.rates[i].round < cutoff {
			i++
		}
		if i > 0 {
			pm.rates = append(pm.rates[:0], pm.rates[i:]...)
		}
	}
	if over := len(pm.rates) - pathSamplesCap; over > 0 {
		pm.rates = append(pm.rates[:0], pm.rates[over:]...)
	}
}

// btlbwBps is the max of the retained delivery-rate samples (0 = unmeasured).
func (pm *legPathModel) btlbwBps() float64 {
	best := 0.0
	for _, s := range pm.rates {
		if s.bps > best {
			best = s.bps
		}
	}
	return best
}

// rtpropMs is the min of the retained path-RTT samples (0 = unmeasured).
func (pm *legPathModel) rtpropMs() float64 {
	best := 0.0
	for _, s := range pm.rtts {
		if best == 0 || s.ms < best {
			best = s.ms
		}
	}
	return best
}

// bdpBytes is BtlBw x RTprop — the bytes that fill this path exactly once.
// 0 when either term is unmeasured.
func (pm *legPathModel) bdpBytes() float64 {
	bw, rt := pm.btlbwBps(), pm.rtpropMs()
	if bw <= 0 || rt <= 0 {
		return 0
	}
	return bw * rt / 1000.0
}

// queueMs is how long the bytes already outstanding on this leg take to drain
// at its measured bottleneck rate: the honest queueing delay, the quantity the
// send→ack delay basis was standing in for. 0 when capacity is unmeasured.
func (pm *legPathModel) queueMs() float64 {
	bw := pm.btlbwBps()
	if bw <= 0 {
		return 0
	}
	return pm.inflightBytes / bw * 1000.0
}

// snapshot renders the model for an observer. Caller holds legMu.
func (pm *legPathModel) snapshot() legPathSnapshot {
	bw, rt := pm.btlbwBps(), pm.rtpropMs()
	return legPathSnapshot{
		BtlBwBps:      bw,
		RTpropMS:      rt,
		BDPBytes:      pm.bdpBytes(),
		InflightBytes: pm.inflightBytes,
		QueueMS:       pm.queueMs(),
		AppLimited:    pm.appLimited,
		Known:         bw > 0 && rt > 0,
	}
}

// pathModelSnapshot returns leg idx's path model, or a zero (Known=false)
// snapshot for an index the mux does not hold.
func (m *routeMux) pathModelSnapshot(idx int) legPathSnapshot {
	if m == nil || idx < 0 {
		return legPathSnapshot{}
	}
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	if idx >= len(m.legs) || m.legs[idx] == nil {
		return legPathSnapshot{}
	}
	return m.legs[idx].model.snapshot()
}

// PathModelEnabled reports whether the schedulers read the path model (BtlBw /
// RTprop) where they previously read the conflated delay basis. Off restores
// the pre-model predicates exactly; the model is still measured and reported.
func PathModelEnabled() bool { return routersettings.PathModel.Bool() }

// SetPathModelEnabled turns that consumption on or off. Always accepted.
func SetPathModelEnabled(on bool) bool { setBool(routersettings.PathModel, on); return true }

// PathBDPGain is the gain the per-leg send window targets over the measured
// BDP (gain x BtlBw x RTprop), applied as a floor under the delivery-proven
// window. 0 drops the term.
func PathBDPGain() float64 { return routersettings.PathBDPGain.Ratio() }

// SetPathBDPGain installs that gain. Negative is refused; 0 is accepted and
// means "size the window exactly as it was sized before the path model".
func SetPathBDPGain(v float64) bool { return setRatio(routersettings.PathBDPGain, v) }

// UnidirReverseECF reports whether the REVERSE (acceptor/download) direction
// places frames with the same predictive scheduler the forward path uses,
// instead of selectByDirection's unweighted round-robin schedule.
func UnidirReverseECF() bool { return routersettings.UnidirReverseECF.Bool() }

// SetUnidirReverseECF turns that placement on or off. Always accepted.
func SetUnidirReverseECF(on bool) bool { setBool(routersettings.UnidirReverseECF, on); return true }

// flushStrandedLeg re-sends the window stranded on a leg just ruled STALLED,
// on a healthy leg, off the caller's goroutine.
//
// It is the send-side twin of the demote-time forced retransmit flush: a leg
// ruled stalled is one this group has stopped sending on, so everything it
// still holds is a gap in the receiver's frontier that nothing on that leg is
// going to close in a useful time. Left to RACK, each of those sequences waits
// out its own threshold and then doubles per retry; on the emulated live-scale
// dead leg that alone was seconds of stall on a download the healthy leg
// finished in 1.2 s.
//
// Asynchronous because resendSeqs takes the route group's lock and the ruling
// is delivered from the window refresh, which can already hold it.
func (rg *RouteGroup) flushStrandedLeg(tpID uuid.UUID) {
	if rg == nil || rg.mux == nil || tpID == uuid.Nil {
		return
	}
	seqs := rg.mux.heldRetxSeqsOnTp(tpID)
	if len(seqs) == 0 {
		return
	}
	go func() {
		if err := rg.resendSeqs(seqs); err != nil {
			rg.logger.WithError(err).Debug("stranded-leg flush could not re-send")
		}
	}()
}

// heldRetxSeqsOnTp returns the sequences whose last send rode tpID, ascending.
// Nil when SACK/retx is not in play.
func (m *routeMux) heldRetxSeqsOnTp(tpID uuid.UUID) []uint32 {
	if !m.sackEnabled || m.retxBuf == nil || tpID == uuid.Nil {
		return nil
	}
	return m.retxBuf.HeldSeqsOnTps(map[uuid.UUID]bool{tpID: true})
}

// legSilentLocked reports whether a leg is holding bytes it has sent and has
// had none of them confirmed for longer than the stall bar: stallFactor times
// its own RTprop (the best ready leg's when it has none of its own — a leg that
// never acknowledges may never have measured itself either), floored at
// leg.stall_min_silence so a very short path is not judged inside one
// delayed-ack interval.
//
// It is the one reading a leg queued past any useful depth produces. Such a leg
// never acknowledges, so its send→ack basis is never sampled — it reads as the
// group's FASTEST leg on the stale first-hop value — and delivKnown never turns
// true, so the goodput half declines to rule what looks merely cold. Caller
// holds legMu.
func (m *routeMux) legSilentLocked(l ecfLegState, stallFactor, bestRTpropMs float64) bool {
	if stallFactor <= 0 || !l.ready || l.unackedBytes <= 0 {
		return false
	}
	rtprop := l.rtpropMs
	if rtprop <= 0 {
		rtprop = bestRTpropMs
	}
	if rtprop <= 0 {
		return false
	}
	bar := stallFactor * rtprop
	if lo := float64(m.knDur(routersettings.LegStallMinSilence)) / float64(time.Millisecond); bar < lo {
		bar = lo
	}
	return l.silentMs > bar
}
