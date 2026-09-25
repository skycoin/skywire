// Package router pkg/router/route_mux_select.go c2-net-routing
package router

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// selectTransport picks the next transport/rule pair for sending.
// Uses latency-weighted selection when data is available, falls back to round-robin.
// Returns the index in tps[] alongside the tp/rule so the caller can
// record per-leg byte counts after a successful write.
//
// payload is the upcoming packet's payload bytes (or nil for
// handshake / control / retx paths that don't have a meaningful
// payload). Used by WeightModeSizeThreshold, WeightModeSticky5Tuple,
// WeightModeLatencyAdaptive, and WeightModeDSCPPriority — they
// inspect the payload directly. Other modes ignore it and fall
// back to the schedule-based pick.
//
// NOTE: not thread-safe, caller must hold the RouteGroup mu.
// selectTransport picks the leg for the next data frame, then applies the
// FEC-block striping cap so no leg exceeds fecDefaultR frames per K-frame block.
// The cap is a thin, best-effort post-pass over selectTransportRaw: it never fails
// a send (if every alive-ready leg is already block-full it keeps the raw pick),
// and it is inert unless FEC is negotiated on a multi-leg group — so the
// pre-integration selection is preserved byte-for-byte when fecEnabled=false.
func (m *routeMux) selectTransport(tps []*transport.ManagedTransport, fwd []routing.Rule, payload []byte) (*transport.ManagedTransport, routing.Rule, int, error) {
	tp, rule, idx, err := m.selectTransportRaw(tps, fwd, payload)
	if err != nil || idx < 0 {
		return tp, rule, idx, err
	}
	if alt, ok := m.fecStripeReassign(tps, idx); ok && alt < len(fwd) {
		tp, rule, idx = tps[alt], fwd[alt], alt
	}
	m.fecStripeUse(idx)
	return tp, rule, idx, nil
}

func (m *routeMux) selectTransportRaw(tps []*transport.ManagedTransport, fwd []routing.Rule, payload []byte) (*transport.ManagedTransport, routing.Rule, int, error) {
	if len(tps) == 0 {
		return nil, nil, -1, ErrNoTransports
	}
	if len(fwd) == 0 {
		return nil, nil, -1, ErrNoRules
	}

	// Unidirectional send selection (CapUniDir).
	// DIRECTION governs which CLASS of leg (direct vs multihop) carries this end's
	// traffic; WITHIN the class the initiator-mirrored ACTIVE set governs which
	// legs. selectByDirection prefers the active (mirrored) class legs — even a
	// mirrored-active reverse leg that never received inbound bulk (so its
	// readiness gate never fired) is preferred over the warm-standby reserve — so
	// the exit's download fan-out stays bounded to the few reverse legs the
	// initiator parked active instead of spraying every warm-standby reverse leg
	// (which over-subscribes the no-skip reorder frontier and wedges the group →
	// the observed collapse-to-0). It returns ok=true (and we use its pick) as long
	// as ANY leg of the wanted class exists, so the download is NEVER handed to the
	// wrong-direction direct leg while a reverse leg is available. Only when there
	// is genuinely no reverse leg does it return ok=false and selection falls
	// through to the standard path.
	if directional, wantDirect, dstPK, srcPK := m.dirConfig(); directional {
		// The FORWARD direction (client -> exit: uploads and requests) rides ONE
		// leg. Which leg is selectConfinedForward's decision — the direct leg when
		// the group has one, else the lowest-latency leg — but THAT it is one leg
		// is decided here, by this end's ROLE, not by the direction->class mapping
		// the flip controller maintains. Reading the class instead was the bug:
		// when the flip controller moved the forward direction onto the multihop
		// class (a sustained upload flips it, and legs-2 has no direct leg for the
		// light direction to sit on), selectByDirection's tier 1 matched BOTH
		// multihop legs and handed the upload straight to the ECF scheduler. That
		// is the measured 79 % / 21 % split across a 44 ms and a 166 ms leg, and
		// the 6 flips in ten rows behind it.
		if m.forwardSender() {
			if tp, rule, idx, ok := m.selectConfinedForwardFor(tps, fwd, payload, wantDirect, dstPK, srcPK); ok {
				return tp, rule, idx, nil
			}
		} else if tp, rule, idx, ok := m.selectByDirection(tps, fwd, wantDirect, dstPK, srcPK); ok {
			// REVERSE (exit -> client: downloads) keeps its fan-out untouched.
			return tp, rule, idx, nil
		}
		// The REVERSE direction with no leg of its class left falls through to the
		// standard path below — there is genuinely nothing to confine a download
		// to, and spreading it is what the reverse direction is meant to do. The
		// FORWARD direction never reaches here: selectConfinedForward widens its
		// own candidate set (class-matching legs first, then any live leg, then
		// any ruled leg) rather than handing the upload to the scheduler.
	}

	// Payload-inspecting modes: ask the selector for a leg
	// derived from the bytes. Empty payload (handshake / retx)
	// falls through to the schedule-based pick.
	if m.tpSelector != nil && len(payload) > 0 {
		switch m.tpSelector.Mode() {
		case WeightModeSizeThreshold,
			WeightModeSticky5Tuple,
			WeightModeLatencyAdaptive,
			WeightModeDSCPPriority,
			WeightModeECF,
			WeightModeOTIAS,
			WeightModeSTMS:
			m.feedInflight(tps)
			idx := m.tpSelector.SelectForPayload(payload)
			// A leg ruled probe-only that has spent its budget declines the
			// frame here and the weighted path below re-homes it; every other
			// leg answers false, so a group of comparable legs is unchanged.
			if idx < len(tps) && !m.legProbeExhausted(idx) {
				tp := tps[idx]
				if tp != nil && !tp.IsClosed() && m.legReadyAt(idx) {
					return tp, fwd[idx], idx, nil
				}
			}
		}
	}

	// Use weighted selector if available
	if m.tpSelector != nil && m.tpSelector.Len() > 0 {
		idx := m.tpSelector.Select()
		// A weighted pick that lands on a leg at its send window moves to a
		// leg with room, so the window bounds every mode, not only the
		// predictive ones that consult saturation themselves.
		if m.retxBuf != nil && m.sackEnabled {
			m.feedInflight(tps)
			if m.tpSelector.Saturated(idx) {
				if alt := m.tpSelector.FirstUnsaturated(); alt >= 0 {
					idx = alt
				}
			}
		}
		// …and a pick that lands on a leg out of probe budget moves to one that
		// still has a share, so a leg whose delay basis is a multiple of its
		// sibling's cannot take every other frame.
		if m.legProbeExhausted(idx) {
			if alt := m.firstProbeReadyLeg(tps); alt >= 0 {
				idx = alt
			}
		}
		if idx < len(tps) {
			tp := tps[idx]
			if tp != nil && !tp.IsClosed() && m.legReadyAt(idx) {
				return tp, fwd[idx], idx, nil
			}
		}
	}

	// Fallback: round-robin with skip-dead and skip-not-ready. An aux leg
	// the peer has not confirmed yet is skipped so we never send the first
	// packets onto a route whose rule the peer has not registered; the
	// primary leg (0) is always ready, so this loop always finds it. When
	// direction filtering is active, non-matching legs are skipped here too.
	n := uint32(len(tps)) //nolint:gosec
	start := atomic.AddUint32(&m.tpIndex, 1) - 1
	for i := uint32(0); i < n; i++ {
		idx := int((start + i) % n) //nolint:gosec
		tp := tps[idx]
		if tp != nil && !tp.IsClosed() && m.legReadyAt(idx) {
			return tp, fwd[idx], idx, nil
		}
	}

	// EMERGENCY FAILOVER: no ACTIVE leg is selectable — every active leg is
	// dead/not-ready. Rather than fail the send (a dead connection), fall through
	// to any alive, ready WARM-STANDBY leg. The 512-deep standby reserve exists
	// precisely so the connection survives the instant its active set is lost,
	// with ZERO promote latency — a parked leg keeps its rules installed and its
	// transport alive, so it can carry a packet immediately. This is what makes
	// the warm reserve a real "switch in at a moment's notice" pool instead of
	// something that only helps on the next 20s rotation tick. The leg-death
	// trigger + rotation tick restore a proper active set right after; this just
	// guarantees no gap. legSelectableIgnoringStandby is legReadyAt WITHOUT the
	// standby exclusion (a parked leg that was active is ready), so it never
	// picks a leg the peer has not confirmed.
	for i := uint32(0); i < n; i++ {
		idx := int((start + i) % n) //nolint:gosec
		tp := tps[idx]
		if tp != nil && !tp.IsClosed() && m.legSelectableIgnoringStandby(idx) {
			return tp, fwd[idx], idx, nil
		}
	}
	return nil, nil, -1, ErrNoSuitableTransport
}

// selectFastestTransport picks the live, ready, non-standby leg with the LOWEST
// measured latency — the pick for the RETRANSMIT path, independent of the
// group's configured distribution mode.
//
// A retransmitted segment is one the receiver's reorder buffer is head-of-line
// blocked on: every later segment that already arrived on a fast leg is being
// withheld until this gap fills. Healing that gap on the FASTEST leg (rather
// than the normal spray/weight pick, which might re-send it down the very slow
// leg that stalled it) advances the delivery window in one fast RTT instead of
// waiting out the reorder timeout. This is what keeps a slow leg from dragging
// the whole stream: it still carries its share of new data, but its stragglers
// are rescued on a fast path. Falls back to the first ready leg when no leg has
// a latency measurement yet.
func (m *routeMux) selectFastestTransport(tps []*transport.ManagedTransport, fwd []routing.Rule) (*transport.ManagedTransport, routing.Rule, int, error) {
	if len(tps) == 0 {
		return nil, nil, -1, ErrNoTransports
	}
	if len(fwd) == 0 {
		return nil, nil, -1, ErrNoRules
	}
	bestIdx, firstReady := -1, -1
	bestLat := -1.0
	for idx, tp := range tps {
		if tp == nil || tp.IsClosed() || !m.legReadyAt(idx) {
			continue
		}
		if firstReady < 0 {
			firstReady = idx
		}
		lat := tp.GetLatency()
		if lat <= 0 {
			continue // unknown latency — only a last resort
		}
		if bestLat < 0 || lat < bestLat {
			bestLat, bestIdx = lat, idx
		}
	}
	if bestIdx < 0 {
		bestIdx = firstReady
	}
	if bestIdx < 0 {
		return nil, nil, -1, ErrNoSuitableTransport
	}
	return tps[bestIdx], fwd[bestIdx], bestIdx, nil
}

// selectRetxTransport is selectFastestTransport with one extra rule: it refuses
// the leg the sequence was LAST sent on.
//
// A retransmit exists because that frame did not arrive, so the leg that
// carried it is the single worst candidate to carry it again — and because
// selectFastestTransport is a pure latency pick, it is also the leg the retry
// deterministically lands on. When a leg black-holes mid-flight (a live route
// dying without its socket closing) that costs the whole group: the frontier
// sequence is re-sent onto the black hole, every retry, for as long as the leg
// still reads as ready. Measured on the emulator (scenario (d), a 3-leg group,
// busiest leg cut at 1/3): all 93 retransmits went out on the cut leg, the
// no-skip reorder frontier never advanced past the missing sequence, and the
// transfer stopped for good at 3,014,646 of 8,388,608 bytes with 4.6 MB already
// buffered behind the gap — with the other two legs healthy and idle
// (skycoin/skywire#5113).
//
// Avoiding the leg costs nothing when the leg is merely slow (the frame is
// re-sent on the next-fastest leg, which is what the caller wanted anyway) and
// is the whole recovery when the leg is dead. It never strands a retransmit:
// with no other ready leg — a single-leg group, or every sibling parked — it
// falls back to the unrestricted pick.
func (m *routeMux) selectRetxTransport(tps []*transport.ManagedTransport, fwd []routing.Rule, avoid uuid.UUID) (*transport.ManagedTransport, routing.Rule, int, error) {
	if avoid == uuid.Nil || len(tps) < 2 {
		return m.selectFastestTransport(tps, fwd)
	}
	if len(fwd) == 0 {
		return nil, nil, -1, ErrNoRules
	}
	bestIdx, firstReady := -1, -1
	bestLat := -1.0
	for idx, tp := range tps {
		if tp == nil || tp.IsClosed() || tp.Entry.ID == avoid || !m.legReadyAt(idx) || idx >= len(fwd) {
			continue
		}
		if firstReady < 0 {
			firstReady = idx
		}
		lat := tp.GetLatency()
		if lat <= 0 {
			continue // unknown latency — only a last resort
		}
		if bestLat < 0 || lat < bestLat {
			bestLat, bestIdx = lat, idx
		}
	}
	if bestIdx < 0 {
		bestIdx = firstReady
	}
	if bestIdx < 0 {
		// Nothing else is ready: better to retry down the same leg than to
		// drop the frame the receiver's frontier is waiting for.
		return m.selectFastestTransport(tps, fwd)
	}
	return tps[bestIdx], fwd[bestIdx], bestIdx, nil
}

// retxLastTp is the transport a held sequence was last sent on, or uuid.Nil when
// the buffer no longer holds it. Feeds selectRetxTransport.
func (m *routeMux) retxLastTp(seq uint32) uuid.UUID {
	if m.retxBuf == nil {
		return uuid.Nil
	}
	_, tpID, ok := m.retxBuf.SentInfo(seq)
	if !ok {
		return uuid.Nil
	}
	return tpID
}

// SetLegLatencyFn wires the per-leg END-TO-END route latency lookup (ms by
// transport id; 0 = unmeasured). Called once by the route group when the mux is
// built. Nil-safe: a mux without it simply has no end-to-end basis and falls
// back to the first-hop transport RTT.
func (m *routeMux) SetLegLatencyFn(fn func(uuid.UUID) float64) { m.legLatencyFn = fn }

// forwardSender reports whether THIS end sends the FORWARD direction of the
// route group — client → exit: uploads and requests. That is the INITIATOR's
// send, always: the flip controller moves which CLASS of leg each direction
// prefers, never which end is the client. Reading the class mapping here
// instead of the role is what let a sustained upload flip the forward direction
// onto the multihop class and straight into the ECF scheduler.
func (m *routeMux) forwardSender() bool {
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	return m.directional && m.initiator
}

// SetForwardRehomeFn wires the callback the mux fires when the forward
// direction moves to a different leg, so the route group can record a
// forward_rehomed mux event. Called once by the route group when the mux is
// built; the callback runs under the route group's mu.
func (m *routeMux) SetForwardRehomeFn(fn func(prev, next int, tp *transport.ManagedTransport, legs int, reason string)) {
	m.onForwardRehome = fn
}

// SetForwardFanoutFn wires the callback the mux fires when the forward
// direction fans out over its sibling legs under load, or returns to its single
// leg. Called once by the route group when the mux is built.
func (m *routeMux) SetForwardFanoutFn(fn func(on bool, idx int, reason string)) {
	m.onForwardFanout = fn
}

// confinedForwardIdx is the leg the forward direction is currently confined to,
// or -1 when none is held. Lock-free, for readers outside the route group's mu
// (waitSendWindow runs on the writer with rg.mu dropped).
func (m *routeMux) confinedForwardIdx() int { return int(m.confinedFwdCur.Load()) }

// selectConfinedForward is the FORWARD direction's whole send decision: one leg,
// every frame. The leg is confinedForwardLeg's pick; the only thing that may
// move a frame off it is the --forward-spill knob, which is OFF by default.
//
// With spill off, a frame that arrives while the confined leg is at its send
// window is not re-homed onto another leg — the writer WAITS for the window
// (waitSendWindow, bounded by --send-window-wait-max). That is the correct
// trade: the alternative is a 10 MB upload split across a 44 ms and a 166 ms
// leg, and every such row measured on the live rig collapsed (x0.68 at 10 MB,
// x0.24 at 50 MB) because the peer's no-skip reorder frontier waits out the
// skew. Spilling is kept as a knob because it is what the code did before, not
// because it is the better default.
//
// payload is the frame's application bytes, or nil on the retransmit path: it
// is only read by the fan-out's scheduler pick, which is the one decision here
// that depends on how big the frame is.
func (m *routeMux) selectConfinedForward(tps []*transport.ManagedTransport, fwd []routing.Rule,
	wantDirect bool, dst, src cipher.PubKey) (*transport.ManagedTransport, routing.Rule, int, bool) {
	return m.selectConfinedForwardFor(tps, fwd, nil, wantDirect, dst, src)
}

// selectConfinedForwardFor is selectConfinedForward with the frame in hand.
func (m *routeMux) selectConfinedForwardFor(tps []*transport.ManagedTransport, fwd []routing.Rule, payload []byte,
	wantDirect bool, dst, src cipher.PubKey) (*transport.ManagedTransport, routing.Rule, int, bool) {
	idx := m.confinedForwardLeg(tps, wantDirect, dst, src)
	if idx < 0 || idx >= len(fwd) || idx >= len(tps) {
		return nil, nil, -1, false
	}
	// Load-triggered fan-out: the direction it is confined to has been filling
	// its leg's send window long enough that one leg is demonstrably not enough,
	// and the group has a sibling close enough in latency to stride with it
	// (unidir.go). The frame goes to whichever band leg has room; when none has,
	// the pick falls through to the confined leg and the writer waits on it,
	// exactly as a confined direction does.
	//
	// The in-flight estimates are re-read per frame (as the spill path does):
	// nothing charges the selector for a frame this path places, so without the
	// refresh a band leg keeps reading as full from the previous frame and the
	// stride collapses back onto the confined leg (measured: 75 %/25 % and
	// x1.14 instead of 48 %/52 % and x1.39).
	if m.tpSelector != nil && m.retxBuf != nil && m.sackEnabled && m.forwardFanoutActive() {
		m.feedInflight(tps)
		if alt := m.pickFanoutLeg(tps, payload, idx); alt >= 0 && alt < len(fwd) && alt < len(tps) {
			return tps[alt], fwd[alt], alt, true
		}
	}
	if ForwardSpill() && m.tpSelector != nil && m.retxBuf != nil && m.sackEnabled {
		m.feedInflight(tps)
		if m.tpSelector.Saturated(idx) {
			if alt := m.tpSelector.FirstUnsaturated(); alt >= 0 && alt < len(fwd) && alt < len(tps) {
				if tp := tps[alt]; tp != nil && !tp.IsClosed() && m.legReadyAt(alt) {
					return tp, fwd[alt], alt, true
				}
			}
		}
	}
	return tps[idx], fwd[idx], idx, true
}

// forwardCandidates lists the legs the forward direction may be confined to, in
// three widening passes: the legs of the wanted CLASS that are live, ready and
// active; then any live, ready, active leg (a multihop-only group has no direct
// leg to sit on, which is the legs-2 shape); then any live leg with a rule at
// all, so a group whose whole active set was just parked still sends.
func (m *routeMux) forwardCandidates(tps []*transport.ManagedTransport, wantDirect bool, dst, src cipher.PubKey) []int {
	live := func(idx int) bool {
		tp := tps[idx]
		return tp != nil && !tp.IsClosed()
	}
	for _, match := range []func(int) bool{
		func(idx int) bool {
			return live(idx) && legIsDirect(tps[idx], dst, src) == wantDirect && m.legReadyAt(idx)
		},
		func(idx int) bool { return live(idx) && m.legReadyAt(idx) },
		func(idx int) bool { return live(idx) && m.legSelectableIgnoringStandby(idx) },
	} {
		cand := make([]int, 0, len(tps))
		for idx := range tps {
			if match(idx) {
				cand = append(cand, idx)
			}
		}
		if len(cand) > 0 {
			return cand
		}
	}
	return nil
}

// forwardLatencies measures every candidate on ONE comparable basis: the
// END-TO-END route latency (all hops, from the leg-liveness pong) when every
// candidate has a sample — the number that actually describes a multihop leg —
// else the FIRST-HOP transport RTT when every candidate has one. The two are
// never mixed across legs: they are different quantities, and comparing them
// would hand the direction to whichever leg happened to lack a pong. ok=false
// means "not comparably measured", and the caller keeps the lowest-indexed
// candidate (leg 0 in practice) exactly as an unmeasured group did before.
func (m *routeMux) forwardLatencies(tps []*transport.ManagedTransport, cand []int) ([]float64, bool) {
	lat := make([]float64, len(cand))
	if m.legLatencyFn != nil {
		ok := true
		for i, idx := range cand {
			ms := m.legLatencyFn(tps[idx].Entry.ID)
			if ms <= 0 {
				ok = false
				break
			}
			lat[i] = ms
		}
		if ok {
			return lat, true
		}
	}
	for i, idx := range cand {
		ms := tps[idx].GetLatency()
		if ms <= 0 {
			return nil, false
		}
		lat[i] = ms
	}
	return lat, true
}

// confinedForwardLeg picks the single leg the FORWARD direction is confined to.
//
// The confinement itself is not in question — spraying an upload across every
// forward leg over-subscribes the no-skip reorder frontier and was measured at
// 67-172 MB sent for a 10 MB upload. WHICH leg is decided here: the lowest
// measured latency among the candidates (forwardCandidates, forwardLatencies),
// which for a group that has a direct leg IS the direct leg, since the
// class-matching pass yields it alone.
//
// Two things keep the pick still. The decision is cached for
// confinedForwardRefresh, so a burst of thousands of frames re-measures a
// handful of times. And moving off the leg the direction already holds needs a
// challenger at least ForwardSwitchMargin LOWER for forwardSwitchSamples
// CONSECUTIVE refreshes — a single loaded sample cannot take the upload away,
// which is what produced 6 forward flips in ten live rows and split every one
// of them across a 44 ms and a 166 ms leg.
//
// The incumbent is dropped without hysteresis only when it is no longer a
// candidate at all: dead, parked to standby, or outclassed by a direct leg that
// has just joined. That is a REHOME, and it is reported through onForwardRehome
// so `visor state --select diag` says which leg took the direction and why.
//
// Caller holds the route group's mu (as for selectTransportRaw).
func (m *routeMux) confinedForwardLeg(tps []*transport.ManagedTransport, wantDirect bool, dst, src cipher.PubKey) int {
	now := time.Now().UnixNano()
	if m.confinedFwdIdx >= 0 && m.confinedFwdIdx < len(tps) &&
		now-m.confinedFwdAtNano < int64(confinedForwardRefresh) {
		if tp := tps[m.confinedFwdIdx]; tp != nil && !tp.IsClosed() && m.legReadyAt(m.confinedFwdIdx) {
			return m.confinedFwdIdx
		}
	}

	cand := m.forwardCandidates(tps, wantDirect, dst, src)
	if len(cand) == 0 {
		return -1
	}
	lat, measured := m.forwardLatencies(tps, cand)

	prev := m.confinedFwdIdx
	// Is the leg the direction already holds still eligible?
	held := -1
	for i, idx := range cand {
		if idx == prev {
			held = i
			break
		}
	}

	best, bestAt := cand[0], 0
	if measured {
		for i := range cand {
			if lat[i] < lat[bestAt] {
				best, bestAt = cand[i], i
			}
		}
	} else if held >= 0 {
		// Not comparably measured: never move a direction that is already placed.
		best, bestAt = prev, held
	}

	// An incumbent placed before any leg was comparably measured is a
	// PLACEHOLDER (leg 0, the leg that happened to be added first), not a
	// decision — the first real measurement replaces it outright, with no
	// hysteresis to clear and no rehome to report.
	placeholder := held >= 0 && m.confinedFwdLatMs <= 0

	reason := ""
	switch {
	case placeholder:
		m.confinedFwdChalIdx, m.confinedFwdChalHits = -1, 0
	case held < 0 && prev >= 0:
		reason = fmt.Sprintf("leg %d is no longer selectable (dead, parked to standby, or a direct leg joined) — rehomed to leg %d", prev, best)
	case held >= 0 && best != prev && measured:
		// Hysteresis: the challenger must clear the margin on consecutive samples.
		margin := ForwardSwitchMargin()
		if lat[bestAt] > lat[held]*(1-margin) {
			m.confinedFwdChalIdx, m.confinedFwdChalHits = -1, 0
			best, bestAt = prev, held
			break
		}
		if m.confinedFwdChalIdx == best {
			m.confinedFwdChalHits++
		} else {
			m.confinedFwdChalIdx, m.confinedFwdChalHits = best, 1
		}
		if m.confinedFwdChalHits < m.knInt(routersettings.ForwardSwitchSamples) {
			best, bestAt = prev, held
			break
		}
		reason = fmt.Sprintf("leg %d measured %.0f ms against leg %d's %.0f ms (at least %.0f%% lower for %d consecutive samples)",
			best, lat[bestAt], prev, lat[held], margin*100, m.knInt(routersettings.ForwardSwitchSamples))
		m.confinedFwdChalIdx, m.confinedFwdChalHits = -1, 0
	default:
		m.confinedFwdChalIdx, m.confinedFwdChalHits = -1, 0
	}

	at := now
	if measured {
		m.confinedFwdLatMs = lat[bestAt]
	} else {
		// Nothing was measured, so nothing was decided: hold the leg for this
		// frame but re-measure on the next one rather than sitting on a
		// placeholder for a whole refresh interval.
		m.confinedFwdLatMs, at = 0, 0
	}
	m.confinedFwdIdx, m.confinedFwdAtNano = best, at
	m.confinedFwdCur.Store(int64(best))
	if reason != "" && prev != best && m.onForwardRehome != nil {
		m.onForwardRehome(prev, best, tps[best], len(tps), reason)
	}
	return best
}
