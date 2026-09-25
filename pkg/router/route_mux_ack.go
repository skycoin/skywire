// Package router pkg/router/route_mux_ack.go c2-net-routing
package router

import (
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/router/routersettings"
)

// sackMinInterval is the minimum spacing between receiver-side SACKs. It is
// well under retxMinAge so a genuine loss is still signaled several times
// before the sender's retransmit timer fires, while collapsing the flood of
// per-packet SACKs that latency-skew reordering would otherwise produce.
const sackMinIntervalDefault = 25 * time.Millisecond

// shouldSendSACK reports whether enough time has elapsed since the last SACK to
// send another, rate-limiting SACK feedback under heavy cross-leg reordering.
// Concurrency-safe: only the goroutine that wins the CAS returns true.
func (m *routeMux) shouldSendSACK() bool {
	now := time.Now().UnixNano()
	prev := m.lastSACKNano.Load()
	if now-prev < int64(m.knDur(routersettings.SackMinInterval)) {
		return false
	}
	return m.lastSACKNano.CompareAndSwap(prev, now)
}

// shouldSendHolSACK reports whether enough time has elapsed since the last
// PROACTIVE HoL SACK to send another, rate-limited to about one fast-leg RTT
// (interval) so a persistent frontier stall re-nudges the sender roughly once
// per round-trip rather than on every arriving out-of-order packet. Uses its own
// holSACKNano clock, independent of the window-ack SACK limiter (shouldSendSACK),
// so the two never rate-limit each other out. Concurrency-safe: only the
// goroutine that wins the CAS returns true.
func (m *routeMux) shouldSendHolSACK(interval time.Duration) bool {
	now := time.Now().UnixNano()
	prev := m.holSACKNano.Load()
	if now-prev < int64(interval) {
		return false
	}
	return m.holSACKNano.CompareAndSwap(prev, now)
}

// generateSACK returns the current SACK state for sending to the peer:
// the last contiguous sequence plus a full-window received bitmap.
func (m *routeMux) generateSACK() (lastContig uint32, words []uint64) {
	if !m.sackEnabled || m.sackTracker == nil {
		return 0, nil
	}
	return m.sackTracker.GenerateSACK()
}

// processSACK processes a received SACK and returns sequences that need
// retransmission. Retained for callers/tests that don't carry the DSACK/ack-edge
// side effects; the live receive path uses onSACKReceived (see rack_tlp.go).
func (m *routeMux) processSACK(lastContig uint32, words []uint64) []uint32 {
	if !m.sackEnabled || m.retxBuf == nil {
		return nil
	}
	return m.retxBuf.ProcessSACK(lastContig, words, m.rackThreshold())
}

// takeDSACK returns a pending DSACK sequence (a duplicate the receiver saw) to
// attach to the next outgoing SACK, clearing it so it is reported once. Returns
// (0, false) when SACK is off or no duplicate is pending.
func (m *routeMux) takeDSACK() (uint32, bool) {
	if !m.sackEnabled || m.sackTracker == nil {
		return 0, false
	}
	return m.sackTracker.takeDSACK()
}

// rackThreshold computes the current reorder-tolerant retransmit threshold from
// the live ACTIVE-leg RTTs: a sequence is presumed lost (not merely reordered on
// a slower leg) once it has been outstanding longer than this. The reordering
// window on a multi-leg mux IS the inter-leg RTT skew, so we bound the threshold
// at the SLOWEST active leg's RTT × rackReorderFactor — a frame striped onto any
// leg should arrive within the slow leg's RTT plus a jitter margin; past that it
// is lost. Adapts per-SACK to the measured path (RFC 8985's RTT-derived expiry),
// floored/capped to avoid a self-amplifying early-retransmit storm or an
// unbounded wait. Falls back to a conservative default until RTTs are known.
func (m *routeMux) rackThreshold() time.Duration {
	maxRtt := m.maxActiveLegRTTms()
	if maxRtt <= 0 {
		return m.knDur(routersettings.RackDefaultNoRTT)
	}
	// The reordering/feedback window is the LARGER of the idle-ping RTT and the
	// measured send→ack delay (ackDelayMs): a saturated leg's queue delays acks
	// by seconds while its ping RTT stays at wire level, and presuming loss
	// inside the real feedback delay is spurious by construction.
	if ad := m.ackDelayMs(); ad > maxRtt {
		maxRtt = ad
	}
	th := time.Duration(maxRtt*m.rackFactor()) * time.Millisecond
	if th < m.knDur(routersettings.RackFloor) {
		th = m.knDur(routersettings.RackFloor)
	}
	// The absolute ceiling bounds the wait for a genuine loss, but it must
	// never undercut one measured RTT: when queueing delay inflates the
	// slow-leg EWMA past rackCeil (bufferbloat under load can push it to many
	// seconds), presuming loss at a fixed 1.5s declares EVERY in-flight packet
	// lost forever — the observed retransmit storm, which the DSACK-widened
	// factor could not counter because this same clamp overrode it. Floor the
	// ceiling at one measured RTT: waiting less than one RTT for an ack is
	// definitionally spurious, and the wait stays bounded (max(rackCeil, RTT))
	// rather than unbounded.
	ceil := m.knDur(routersettings.RackCeil)
	if rttDur := time.Duration(maxRtt) * time.Millisecond; rttDur > ceil {
		ceil = rttDur
	}
	if th > ceil {
		th = ceil
	}
	return th
}

// recordAckDelay folds one measured send→ack delay sample into the ackDelay
// EWMA. Asymmetric on purpose: a sample ABOVE the running value takes it over
// half-way (α=0.5) so the threshold tracks a queue building in a few SACKs
// (each spurious retransmit before it catches up is pure waste), while a
// sample below decays it gently (α=0.125) so one lucky fast ack doesn't
// re-arm eager retransmission while the queue is still draining.
func (m *routeMux) recordAckDelay(d time.Duration) {
	ms := float64(d) / float64(time.Millisecond)
	for {
		cur := m.ackDelayMilli.Load()
		curMs := float64(cur) / 1000
		alpha := 0.125
		if ms > curMs {
			alpha = 0.5
		}
		next := int64((curMs + alpha*(ms-curMs)) * 1000)
		if m.ackDelayMilli.CompareAndSwap(cur, next) {
			return
		}
	}
}

// ackDelayMs returns the EWMA send→ack delay in milliseconds (0 = no sample).
func (m *routeMux) ackDelayMs() float64 {
	return float64(m.ackDelayMilli.Load()) / 1000
}

// ackDelayEst is one leg's send→ack delay estimate: the asymmetric EWMA the
// loss detectors judge holes against, and when the last sample landed.
//
// The EWMA rises fast, falls slowly, and had no time decay at all, so the
// queue one transfer built was still the basis when the next one started:
// measured live 2026-09-17, five consecutive 50 MB uploads over ONE leg
// through a 470 ms intermediate ran 3.90, 3.05, 2.80, 1.59, 0.62 MB/s with
// ZERO loss (retx 6→28, send_window_waits 0), the leg's reported delay
// ratcheting to 13435 ms while sacks_recv per row went 125→476 at constant
// frames. lastNano is what ends that: the estimate expires with the transfer.
type ackDelayEst struct {
	ms       float64
	lastNano int64
}

// ackDelayStale is how long a leg's send→ack estimate survives without a new
// sample. Past it the leg reads as unsampled again — the first-hop transport
// latency is the basis until the next transfer proves otherwise — so an idle
// gap between transfers resets both the loss threshold and the window's
// feedback delay instead of carrying the previous transfer's queue into the
// next one.
const ackDelayStale = 5 * time.Second

// recordAckDelayTp folds one send→ack delay sample into the leg's own EWMA
// (same asymmetric α as recordAckDelay: fast up, slow down).
func (m *routeMux) recordAckDelayTp(tpID uuid.UUID, d time.Duration) {
	ms := float64(d) / float64(time.Millisecond)
	now := time.Now().UnixNano()
	m.ackDelayByTpMu.Lock()
	if m.ackDelayByTp == nil {
		m.ackDelayByTp = make(map[uuid.UUID]*ackDelayEst)
	}
	cur := m.ackDelayByTp[tpID]
	if cur == nil {
		cur = new(ackDelayEst)
		m.ackDelayByTp[tpID] = cur
	}
	if cur.ms == 0 || now-cur.lastNano > int64(ackDelayStale) {
		// First sample — or the first after an idle gap — seeds the estimate
		// whole: an EWMA from zero would halve it, a freshly added leg is judged
		// against this very number while its first packets are still in flight,
		// and a stale estimate describes a queue that has since drained.
		cur.ms = ms
	} else {
		alpha := 0.125
		if ms > cur.ms {
			alpha = 0.5
		}
		cur.ms += alpha * (ms - cur.ms)
	}
	cur.lastNano = now
	// One sample per leg per SBDSampleInterval is also the shared-bottleneck
	// detector's delay series while data flows (see bottleneck.go): the SACK
	// feedback is the only per-leg delay signal fast enough for SBD to rule
	// before a transfer is over. The decision is taken under this lock (so two
	// concurrent SACK handlers cannot both pass it) and the hook is called after
	// the unlock (it takes the route group's legLivenessMu).
	fold := false
	if m.onLegDelaySample != nil {
		if iv := int64(SBDSampleInterval()); now-m.sbdFoldNano[tpID] >= iv {
			if m.sbdFoldNano == nil {
				m.sbdFoldNano = make(map[uuid.UUID]int64)
			}
			m.sbdFoldNano[tpID] = now
			fold = true
		}
	}
	m.ackDelayByTpMu.Unlock()
	if fold {
		m.onLegDelaySample(tpID, ms)
	}
}

// setLegE2ERTT records one leg's smoothed end-to-end round-trip latency (ms),
// as measured by the leg-liveness pong. Non-positive values are ignored.
func (m *routeMux) setLegE2ERTT(tpID uuid.UUID, ms float64) {
	if ms <= 0 || tpID == uuid.Nil {
		return
	}
	m.legE2EMu.Lock()
	if m.legE2EByTp == nil {
		m.legE2EByTp = make(map[uuid.UUID]float64)
	}
	m.legE2EByTp[tpID] = ms
	m.legE2EMu.Unlock()
}

// legE2ERttMsTp returns the leg's smoothed end-to-end round-trip latency in ms
// (0 = no pong sample for that transport yet). Unlike ackDelayMsTp it does not
// expire: the liveness pong keeps measuring an idle leg, and the route latency
// it reports is a property of the path, not of a transfer.
func (m *routeMux) legE2ERttMsTp(tpID uuid.UUID) float64 {
	m.legE2EMu.RLock()
	defer m.legE2EMu.RUnlock()
	return m.legE2EByTp[tpID]
}

// legDelayBasisMs is the delay a frame in flight on this leg is judged against:
// the larger of the leg's measured send→ack delay (the real feedback delay
// under load, when fresh) and its end-to-end pong RTT (the path's latency,
// always current). Either alone under-reports — the first expires between
// transfers, the second does not see a queue — and judging a frame against a
// number smaller than its leg's own delay declares it lost while it is in
// ordinary flight. 0 when the leg has neither.
func (m *routeMux) legDelayBasisMs(tpID uuid.UUID) float64 {
	basis := m.ackDelayMsTp(tpID)
	if e2e := m.legE2ERttMsTp(tpID); e2e > basis {
		basis = e2e
	}
	return basis
}

// ackDelayMsTp returns the leg's EWMA send→ack delay in milliseconds (0 = no
// sample yet for that transport, or none for ackDelayStale).
func (m *routeMux) ackDelayMsTp(tpID uuid.UUID) float64 {
	now := time.Now().UnixNano()
	m.ackDelayByTpMu.Lock()
	defer m.ackDelayByTpMu.Unlock()
	e := m.ackDelayByTp[tpID]
	if e == nil || now-e.lastNano > int64(ackDelayStale) {
		return 0
	}
	return e.ms
}

// rackThresholdFor is rackThreshold judged for one leg: when the leg's own
// measured delay exceeds the group-wide basis, its holes wait for that delay
// (× the reorder factor, the ceiling raised to it) before they are presumed
// lost. Never below the group-wide threshold, which stays the floor for a leg
// with no delay sample of its own.
func (m *routeMux) rackThresholdFor(tpID uuid.UUID) time.Duration {
	return m.rackThresholdForWith(m.rackThreshold(), tpID)
}

// rackThresholdForWith is rackThresholdFor with the group threshold already
// computed. It touches no leg-table lock, so the SACK handler can call it
// while it holds the retx buffer's lock (rackThreshold reads the leg table,
// and the window refresh reads the buffer under that table's lock — the
// inversion that froze a group).
func (m *routeMux) rackThresholdForWith(th time.Duration, tpID uuid.UUID) time.Duration {
	// The leg's own delay is max(send→ack delay, end-to-end pong RTT). Judged on
	// the send→ack delay alone this was per-leg in form only: that estimate is
	// empty until a never-retransmitted frame is acked and expires
	// ackDelayStale after the last one, so the common case fell back to the
	// group threshold — built from ecfRttMs, whose floor is the FIRST-HOP
	// latency. On the measured 2026-09-17 compositions (legs at 151/216 ms and
	// 137/410 ms end-to-end over near first hops) that put every leg's holes on
	// the fast leg's clock and declared the slow leg's in-flight frames lost.
	ad := m.legDelayBasisMs(tpID)
	if ad <= 0 {
		return th
	}
	own := time.Duration(ad*m.rackFactor()) * time.Millisecond
	// The ceiling bounds how long we wait for a frame that really is lost, but
	// it must never pull the wait BELOW the leg's own basis plus the reordering
	// margin. Clamped at one bare basis (the previous max(rackCeil, basis)),
	// a queue-deep leg — basis past rackCeil is exactly the loaded case — waited
	// for the MEAN of its own delay distribution: every frame slower than that
	// mean was SACK-retransmitted while in perfectly ordinary flight on its own
	// leg, and the original then landed as a duplicate. Measured live
	// (2026-09-16 mux-legs-2, legs at 128/152 ms): a 10 MB upload that actually
	// striped 51/49 put 14.6 MB on the wire, retx_sent +337 of which
	// retx_req_sack +306, the duplicates saturating both send windows until the
	// writer parked (send_window_timeouts +6). A row that happened to keep 100 %
	// on one leg sent 10.02 MB with one retransmit.
	//
	// There is no separate cap left to apply: rackCeil is below basis×factor
	// exactly when the clamp used to bite, and the wait is already bounded by
	// the leg's own measured delay, which decays as the queue drains.
	if own < th {
		return th
	}
	return own
}

// maxActiveLegRTTms returns the slowest active (non-standby) leg's EWMA RTT in
// milliseconds, or 0 when no leg has a measured RTT yet. It is the reordering-
// window basis: a frame striped onto any active leg should arrive within the
// slowest leg's RTT plus a margin, so both the RACK loss threshold and the TLP
// probe timeout are derived from it.
//
// ecfRttMs is the leg's END-TO-END feedback delay (refreshLegWindows folds
// max(first-hop RTT, send→ack delay) into it), which is the delay a frame's
// acknowledgement actually has to survive. Judged on the first-hop RTT alone
// this returned ~95 ms while real feedback took ~170 ms, so RACK presumed loss
// inside one round trip and retransmitted everything in flight.
func (m *routeMux) maxActiveLegRTTms() float64 {
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	maxRtt := 0.0
	for i, lc := range m.legs {
		if lc == nil {
			continue
		}
		if i < len(m.standby) && m.standby[i] {
			continue // active legs only
		}
		if lc.ecfRttMs > maxRtt {
			maxRtt = lc.ecfRttMs
		}
	}
	return maxRtt
}

// getRetxPayload retrieves a stored payload for retransmission.
func (m *routeMux) getRetxPayload(seq uint32) []byte {
	if m.retxBuf == nil {
		return nil
	}
	return m.retxBuf.Get(seq)
}

// heldRetxSeqs returns every sequence currently held unACKed in the sender's
// retx buffer, ascending. Nil when SACK/retx is not in play. Used by the
// demote-time forced retx flush to re-send a parked leg's in-flight range onto
// an active leg (see RouteGroup.rotationServiceFn).
func (m *routeMux) heldRetxSeqs() []uint32 {
	if !m.sackEnabled || m.retxBuf == nil {
		return nil
	}
	return m.retxBuf.Seqs()
}

// heldRetxSeqsOnTps returns the held sequences whose LAST send rode one of the
// given transports (unknown-tag entries included conservatively), ascending.
// The demote-time flush uses this to rescue only the sequences the demoted
// leg(s) actually strand, instead of duplicating the whole in-flight window
// onto the surviving legs. Keyed by transport UUID, never leg index — indices
// shift on slice compaction and a stale index tag wedged live sessions.
func (m *routeMux) heldRetxSeqsOnTps(tpIDs []uuid.UUID) []uint32 {
	if !m.sackEnabled || m.retxBuf == nil || len(tpIDs) == 0 {
		return nil
	}
	set := make(map[uuid.UUID]bool, len(tpIDs))
	for _, id := range tpIDs {
		if id != uuid.Nil {
			set[id] = true
		}
	}
	return m.retxBuf.HeldSeqsOnTps(set)
}

// retxSetTp re-tags a held sequence's last-send transport after a retransmit
// moved it to a different leg, keeping the demote-flush attribution honest.
func (m *routeMux) retxSetTp(seq uint32, tpID uuid.UUID) {
	if m.retxBuf == nil {
		return
	}
	m.retxBuf.SetTpID(seq, tpID)
}
