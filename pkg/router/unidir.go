// Package router pkg/router/unidir.go c2-net-routing
package router

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// Flip controller (step 2b). Both ends run unidirFlipServiceFn; each measures the
// ABSOLUTE upload vs download goodput (mapping its local send/recv by role) and,
// when the asymmetry inverts and holds, flips the direction→leg-class mapping so
// the HEAVY direction gets the aggregated mux. Both ends see the same absolute
// asymmetry, so they flip together (a brief transient where only one has flipped
// self-corrects on the next tick). Hysteresis + cooldown keep it from flapping.
const (
	unidirFlipIntervalDefault = 1 * time.Second // flip-controller tick cadence
	flipRatioDefault          = 2.0             // flip when the heavy direction ≥ this × the light one
	flipHysteresisDefault     = 3               // consecutive qualifying ticks before flipping
	flipCooldownTicksDefault  = 3               // ticks to hold after a flip before another
	flipMinGoodputDefault     = 8192.0          // bytes/sec floor — ignore near-idle noise
)

// Unidirectional per-leg send selection (CapUniDir). Each end restricts its OWN
// send to legs matching its direction, so the two directions ride disjoint legs:
//
//   - DEFAULT: the initiator (upload / forward) sends on the DIRECT (1-hop) leg;
//     the acceptor (download / reverse) sends on the MULTIHOP mux legs. So the
//     light direction takes the low-latency direct transport and the heavy
//     direction aggregates over the mux — instead of both directions striping
//     every leg (the head-of-line churn the confound-free telemetry exposed).
//   - FLIPPED: the mapping is swapped (initiator sends on multihop, acceptor on
//     direct) so the HEAVY direction gets the mux when upload outweighs download.
//
// Both ends decide locally from their role + each leg's directness (no per-packet
// signaling); a flip is coordinated out of band. If no leg matches the wanted
// direction, selection falls back to any ready leg so a send never fails.

// setDirectional enables unidirectional send selection and records this end's
// role and the route-group endpoints (used to tell a direct leg from a multihop
// one). Called once at handshake when CapUniDir is negotiated.
func (m *routeMux) setDirectional(initiator bool, dst, src cipher.PubKey) {
	m.legMu.Lock()
	m.directional = true
	m.initiator = initiator
	m.dstPK = dst
	m.srcPK = src
	m.legMu.Unlock()
}

// setFlipped swaps the direction→leg-class mapping (heavy direction gets the
// mux). No-op unless directional. Returns true if the state changed. The bool is
// set both ways by the flip controller (2b); it is only exercised with true so
// far in tests.
//
//nolint:unparam
func (m *routeMux) setFlipped(flipped bool) (changed bool) {
	m.legMu.Lock()
	if m.directional && m.flipped != flipped {
		m.flipped = flipped
		changed = true
	}
	m.legMu.Unlock()
	return changed
}

// isDirectional reports whether unidirectional send selection (CapUniDir) is
// active on this mux.
func (m *routeMux) isDirectional() bool {
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	return m.directional
}

// dirState snapshots the unidirectional send-selection state (directional +
// current flip) under legMu for telemetry (MuxStats / visor state).
func (m *routeMux) dirState() (directional, flipped bool) {
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	return m.directional, m.flipped
}

// setFlipPin applies (or releases) the operator's MANUAL direction pin. mode is
// routing.DirectionAuto/DirectionPinDefault/DirectionPinFlipped. Pinning also
// enforces the pinned mapping immediately (no wait for the next controller
// tick); releasing leaves the mapping where the pin put it — the controller
// resumes from there and re-flips only when the traffic asymmetry says so.
// No-op unless directional (a pin on a symmetric mux would never be read).
func (m *routeMux) setFlipPin(mode byte) {
	m.legMu.Lock()
	if !m.directional {
		m.legMu.Unlock()
		return
	}
	m.flipPin = mode
	m.legMu.Unlock()
	if mode != routing.DirectionAuto {
		m.setFlipped(mode == routing.DirectionPinFlipped)
	}
}

// flipPinMode snapshots the manual direction pin under legMu.
func (m *routeMux) flipPinMode() byte {
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	return m.flipPin
}

// flipPinString renders a pin mode for telemetry: "auto" (controller in
// charge), "default" or "flipped" (operator-pinned mapping).
func flipPinString(mode byte) string {
	switch mode {
	case routing.DirectionPinDefault:
		return "default"
	case routing.DirectionPinFlipped:
		return "flipped"
	default:
		return "auto"
	}
}

// soleBlackHoleExemptRecvFloor is the per-tick GROUP recv above which a
// directional group's sole ACTIVE (light-direction) leg is exempt from the
// sole-leg black-hole reaping.
const soleBlackHoleExemptRecvFloor = 16 * 1024

// soleLegBlackHoleExempt reports whether the sole-leg black-hole reaping should
// be SKIPPED this tick. Under unidirectional assignment the sole active leg
// carries only ONE direction — on a download it is the light FORWARD (upload)
// leg, which sends acks but receives ~nothing because the download flows on the
// REVERSE (send-standby) mux legs. Judging that leg by its own recv would misread
// it as a black-hole and prune the direct leg exactly when unidir is working. So
// skip the reaping when directional AND the GROUP is receiving data on its
// reverse legs (aggregate recv delta above the floor) — the group is not
// black-holing even though the sole active leg is quiet on the receive side.
func soleLegBlackHoleExempt(directional bool, aggRecvDelta uint64) bool {
	return directional && aggRecvDelta > soleBlackHoleExemptRecvFloor
}

// dirConfig snapshots the directional state under legMu so the send path reads it
// once and then uses lock-free pure helpers (avoids re-locking legMu per leg).
func (m *routeMux) dirConfig() (directional, wantDirect bool, dst, src cipher.PubKey) {
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	// This end sends on DIRECT legs when it is the forward sender: the initiator by
	// default, or the acceptor when flipped.
	wantDirect = m.initiator != m.flipped
	return m.directional, wantDirect, m.dstPK, m.srcPK
}

// legIsDirectTp reports whether tp is this route group's DIRECT (1-hop) leg,
// using the directional endpoints recorded at handshake. Returns false when the
// mux is not directional (the endpoints are zero). Used for confinement
// observability (legDataProgressServiceFn).
func (m *routeMux) legIsDirectTp(tp *transport.ManagedTransport) bool {
	m.legMu.RLock()
	dst, src := m.dstPK, m.srcPK
	m.legMu.RUnlock()
	return legIsDirect(tp, dst, src)
}

// legIsDirect reports whether a leg's transport goes straight to a route-group
// endpoint (a 1-hop / direct leg) rather than through an intermediary.
func legIsDirect(tp *transport.ManagedTransport, dst, src cipher.PubKey) bool {
	if tp == nil {
		return false
	}
	r := tp.Remote()
	return r == dst || r == src
}

// unidirFlipTick runs one flip-controller tick: it samples the active legs'
// absolute upload/download goodput and flips (or reverts) the direction mapping
// when the heavy direction has out-weighed the light one by flipRatio for
// flipHysteresis consecutive ticks, then holds for flipCooldownTicks. Returns the
// current flipped state and whether it changed this tick. Called only from the
// single unidir-flip loop goroutine, so the hysteresis counters need no lock.
func (m *routeMux) unidirFlipTick() (flipped, changed bool) {
	m.legMu.RLock()
	directional := m.directional
	initiator := m.initiator
	pinned := m.flipPin != routing.DirectionAuto
	m.legMu.RUnlock()
	if !directional {
		return m.flipped, false
	}
	// Operator pin: skip the goodput sampling entirely — flipStep enforces the
	// pinned mapping and keeps the hysteresis counters zeroed while dormant.
	if pinned {
		return m.flipStep(0, 0)
	}

	stats := m.snapshotLegs() // samples per-leg goodput EWMAs
	var up, down float64      // ABSOLUTE upload / download bytes/sec
	m.legMu.RLock()
	for i, s := range stats {
		if i < len(m.standby) && m.standby[i] {
			continue // active legs only
		}
		if initiator {
			// initiator's local send = upload, local recv = download
			up += s.GoodputUpBps
			down += s.GoodputDownBps
		} else {
			// acceptor's local send = download, local recv = upload
			up += s.GoodputDownBps
			down += s.GoodputUpBps
		}
	}
	m.legMu.RUnlock()
	return m.flipStep(up, down)
}

// flipStep applies one hysteresis/cooldown step to the flip state given the
// current ABSOLUTE upload/download goodput. Split from the sampling so it is unit
// testable with synthetic rates. Single-goroutine (the flip loop); hysteresis
// counters need no lock.
func (m *routeMux) flipStep(up, down float64) (flipped, changed bool) {
	m.legMu.RLock()
	directional := m.directional
	cur := m.flipped
	pin := m.flipPin
	m.legMu.RUnlock()
	if !directional {
		return cur, false
	}
	// MANUAL pin: the controller is dormant. Enforce the pinned mapping every
	// tick (idempotent — setFlipped only reports a change when the state moves,
	// e.g. right after the pin landed or if something else touched flipped) and
	// zero the hysteresis/cooldown counters so releasing the pin hands the
	// controller a clean slate instead of a half-accumulated flip decision.
	if pin != routing.DirectionAuto {
		m.flipUpHits, m.flipDownHits, m.flipCooldown = 0, 0, 0
		want := pin == routing.DirectionPinFlipped
		return want, m.setFlipped(want)
	}
	if m.flipCooldown > 0 {
		m.flipCooldown--
		return cur, false
	}

	switch {
	case up > float64(m.knBytes(routersettings.UnidirFlipMinGoodput)) && up >= m.knRatio(routersettings.UnidirFlipRatio)*down:
		m.flipUpHits++
		m.flipDownHits = 0
	case down > float64(m.knBytes(routersettings.UnidirFlipMinGoodput)) && down >= m.knRatio(routersettings.UnidirFlipRatio)*up:
		m.flipDownHits++
		m.flipUpHits = 0
	default:
		m.flipUpHits, m.flipDownHits = 0, 0
	}

	if !cur && m.flipUpHits >= m.knInt(routersettings.UnidirFlipHysteresis) && m.setFlipped(true) {
		m.flipCooldown = m.knInt(routersettings.UnidirFlipCooldownTicks)
		m.flipUpHits = 0
		return true, true
	}
	if cur && m.flipDownHits >= m.knInt(routersettings.UnidirFlipHysteresis) && m.setFlipped(false) {
		m.flipCooldown = m.knInt(routersettings.UnidirFlipCooldownTicks)
		m.flipDownHits = 0
		return false, true
	}
	return cur, false
}

// Forward fan-out under load (step 2c). CONFINEMENT is what keeps an
// interactive session's requests on the lowest-latency leg, and #5035 measured
// what happens without it: an upload sprayed across a 44 ms and a 166 ms leg
// runs at x0.24 of one leg alone, because the peer's no-skip reorder frontier
// waits out the skew. But confinement also caps the upload at ONE leg's rate,
// which is why every upload cell of the live campaign lost (legs-2: x0.69 at
// 10 MB, x0.88 at 50 MB; compose 2x2 50 MB up x0.43).
//
// The flip controller was supposed to cover this case and cannot: it moves
// which CLASS of leg each direction prefers, while selectTransportRaw sends the
// forward direction through selectConfinedForward by this end's ROLE. A flip
// therefore moves the upload from one single leg to another single leg, and in
// a group with no direct leg — the legs-2 and compose-2x2 shapes — both classes
// are "multihop", so it does not even do that.
//
// So the widening is driven by LOAD instead, and it is a WIDENING rather than a
// move: the leg the direction was confined to stays in the set and stays the
// scheduler's first choice. Three gates keep it away from the traffic
// confinement exists for:
//
//   - LOAD: the confined leg must keep filling its send window for
//     unidir.fanout_engage. A request that fits in one window never gets there,
//     so an interactive session stays on its single lowest-latency leg.
//   - SKEW: only siblings within unidir.fanout_max_skew of the confined leg's
//     measured latency join the band. This is the lesson of the rejected #5050 —
//     a 44 ms leg's frames must not be striped onto a 166 ms one — and it is why
//     a group whose siblings are all far slower simply stays confined.
//   - RELEASE: unidir.fanout_release with nothing written puts the direction
//     back on its one leg, so the fan-out cannot outlive the upload.
//
// Within the band the frame goes wherever the existing ECF scheduler says, which
// is the same machinery the download direction has always used; the band is the
// only thing the forward direction adds to it.
const (
	forwardFanoutEngageDefault  = 300 * time.Millisecond
	forwardFanoutReleaseDefault = 2 * time.Second
	forwardFanoutMaxSkewDefault = 2.0
)

// forwardFanout is the forward direction's load meter and fan-out latch. All
// fields are atomics: the meter is fed from the writer (waitSendWindow, with
// the route group's mu DROPPED) and read from the send path (with it held).
type forwardFanout struct {
	on atomic.Bool
	// satSince is when the current load episode began, 0 when there is none.
	// lastSat is the most recent qualifying observation, which is what the
	// release interval is measured from.
	satSince atomic.Int64
	lastSat  atomic.Int64
}

// forwardFanoutStep applies one observation of the forward writer's demand to
// the latch. qualifies means "there is a band to widen into AND the direction
// is loaded" — the band half matters on its own, because a group with no
// comparable sibling must never accumulate toward a fan-out it cannot use.
// Split from the sampling so the hysteresis is unit testable with a synthetic
// clock (nowNano is a real UnixNano; 0 is the "no episode" sentinel).
func (m *routeMux) forwardFanoutStep(nowNano int64, qualifies bool, engage, release time.Duration) (on, changed bool) {
	f := &m.fwdFan
	if qualifies {
		if f.satSince.Load() == 0 {
			f.satSince.Store(nowNano)
		}
		f.lastSat.Store(nowNano)
		if nowNano-f.satSince.Load() >= int64(engage) && f.on.CompareAndSwap(false, true) {
			return true, true
		}
		return f.on.Load(), false
	}
	// A rate-limited leg is not full from one microsecond to the next — it
	// fills, drains an ack's worth and fills again, so the writer's view of it
	// alternates many times a second. Requiring an UNBROKEN stretch of full
	// windows would therefore never engage on a real upload (measured: 25
	// window parks across a 4.6 s 16 MB transfer, none of them adjacent). The
	// episode is what has to be unbroken: it survives any gap shorter than the
	// release interval, and only a leg that has had room for that whole
	// interval ends it.
	last := f.lastSat.Load()
	if last == 0 || nowNano-last < int64(release) {
		return f.on.Load(), false
	}
	f.satSince.Store(0)
	if f.on.CompareAndSwap(true, false) {
		return false, true
	}
	return false, false
}

// forwardFanoutActive reports whether the forward direction is currently fanned
// out, expiring the latch in place when the release interval has passed with no
// qualifying observation. Expiring here rather than only on the writer's path
// matters because a group that stops uploading stops calling waitSendWindow
// altogether — the latch has to drop on the clock, not on the next write.
func (m *routeMux) forwardFanoutActive() bool {
	if !m.fwdFan.on.Load() {
		return false
	}
	on, changed := m.forwardFanoutStep(time.Now().UnixNano(), false,
		m.knDur(routersettings.UnidirFanoutEngage), m.knDur(routersettings.UnidirFanoutRelease))
	if changed {
		m.noteFanoutChange(false, "the forward direction has written nothing for the whole release interval — the upload is back on its single leg")
	}
	return on
}

// noteFanoutChange fires the fan-out callback (SetForwardFanoutFn), so a change
// of latch is a named mux event rather than only a rate that moved.
func (m *routeMux) noteFanoutChange(on bool, reason string) {
	if m.onForwardFanout != nil {
		m.onForwardFanout(on, m.confinedForwardIdx(), reason)
	}
}

// noteForwardLoad feeds the writer's view of the confined leg's send window to
// the fan-out latch. Called from waitSendWindow, which runs on the writer with
// the route group's mu DROPPED and has just refreshed the in-flight estimates.
func (m *routeMux) noteForwardLoad(tps []*transport.ManagedTransport) {
	if !m.forwardSender() || m.tpSelector == nil {
		return
	}
	idx := m.confinedForwardIdx()
	// ENGAGE reads pressure: the one leg the direction is confined to is at its
	// send window. HOLD reads demand: the writer is still pushing frames at all.
	// They cannot be the same question, because the pressure is what the fan-out
	// RELIEVES — once the siblings are carrying, the confined leg stops being
	// full and a pressure-held latch releases itself in the middle of the upload
	// and re-engages 300 ms later (measured on a 48 MB emulated upload: one
	// release, one re-engage, x1.31 instead of x1.6). A latch held by demand is
	// stable, and it still cannot outlive the upload: this is the writer's own
	// path, so when the application stops writing the observations stop with it
	// and forwardFanoutActive expires the latch on the clock.
	qualifies := idx >= 0 && len(m.forwardFanoutLegs(tps, idx)) > 0 &&
		(m.fwdFan.on.Load() || m.tpSelector.Saturated(idx))
	on, changed := m.forwardFanoutStep(time.Now().UnixNano(), qualifies,
		m.knDur(routersettings.UnidirFanoutEngage), m.knDur(routersettings.UnidirFanoutRelease))
	if !changed {
		return
	}
	if on {
		m.noteFanoutChange(true, fmt.Sprintf(
			"leg %d kept filling its send window for %v — the upload is now striding %d legs within %.1fx of its latency",
			idx, m.knDur(routersettings.UnidirFanoutEngage), len(m.forwardFanoutLegs(tps, idx)), m.knRatio(routersettings.UnidirFanoutMaxSkew)))
		return
	}
	m.noteFanoutChange(false, "the forward direction has written nothing for the whole release interval — the upload is back on its single leg")
}

// forwardFanoutLegs is the BAND the forward direction strides while fanned out:
// the leg it is confined to, plus every live, ready, class-agnostic sibling
// whose latency is within unidir.fanout_max_skew of it, measured on the SAME
// basis confinedForwardLeg used (forwardLatencies: end-to-end when every
// candidate has a pong, else the first-hop RTT). nil when the group has no
// comparable sibling — an unmeasured group included, because the fan-out never
// guesses which legs are close enough to stride together.
//
// Two deliberate choices. The band is NOT filtered by leg class: the whole
// point of the load trigger is that one leg is not enough, and in a group that
// has a direct leg the extra capacity is on the multihop legs. And the band is
// narrow (2.0x by default), which is the lesson of the rejected #5050 — a
// 44 ms leg's traffic striped onto a 166 ms one measured x0.24, because the
// peer's no-skip reorder frontier waits out the skew. A direct leg far faster
// than every sibling therefore has an EMPTY band and stays confined.
func (m *routeMux) forwardFanoutLegs(tps []*transport.ManagedTransport, cur int) []int {
	if cur < 0 || cur >= len(tps) {
		return nil
	}
	cand := make([]int, 0, len(tps))
	cand = append(cand, cur)
	for idx := range tps {
		if idx == cur {
			continue
		}
		tp := tps[idx]
		if tp == nil || tp.IsClosed() || !m.legReadyAt(idx) {
			continue
		}
		cand = append(cand, idx)
	}
	if len(cand) < 2 {
		return nil
	}
	lat, measured := m.forwardLatencies(tps, cand)
	if !measured || lat[0] <= 0 {
		return nil
	}
	ceil := lat[0] * m.knRatio(routersettings.UnidirFanoutMaxSkew)
	band := []int{cur}
	for i := 1; i < len(cand); i++ {
		if lat[i] > 0 && lat[i] <= ceil {
			band = append(band, cand[i])
		}
	}
	if len(band) < 2 {
		return nil
	}
	return band
}

// pickFanoutLeg returns the band leg a forward frame should ride now, or -1
// when every leg in the band is at its send window (then the writer waits,
// exactly as a confined direction does — it never spills outside the band).
//
// Round-robin over the band, NOT "the confined leg until it is full, then the
// rest". Strict overflow was measured first and does not aggregate: the writer
// is one goroutine, so keeping the primary leg pinned at its window makes every
// frame wait on that leg's pacing and the siblings pick up only what leaks past
// it (16 MB over a 120 ms and a 150 ms leg: 63 %/37 %, x1.28 of one leg alone).
// Striding the band evenly is what the download direction has always done, and
// it is what the aggregation number comes from.
func (m *routeMux) pickFanoutLeg(tps []*transport.ManagedTransport, payload []byte, cur int) int {
	band := m.forwardFanoutLegs(tps, cur)
	if len(band) == 0 {
		return -1
	}
	inBand := func(idx int) bool {
		for _, b := range band {
			if b == idx {
				return true
			}
		}
		return false
	}
	// Ask the SCHEDULER first, bounded to the band. ECF (the default mode)
	// places a frame on the leg that will deliver it soonest and charges the
	// leg's in-flight for it; a plain round-robin does neither, and on two legs
	// of equal rate and unequal RTT it splits by bandwidth-delay product instead
	// of by rate — the slower leg draws the larger share and becomes the
	// bottleneck (measured 37 %/62 % at x1.34 against 48 %/52 % at x1.6). This is
	// the same selector the download direction has always used; the band is the
	// only thing the forward direction adds.
	if idx := m.tpSelector.SelectForPayload(payload); idx >= 0 && idx < len(tps) && inBand(idx) &&
		!m.legProbeExhausted(idx) && !m.tpSelector.Saturated(idx) {
		return idx
	}
	start := int(atomic.AddUint32(&m.tpIndex, 1) - 1)
	for i := 0; i < len(band); i++ {
		idx := band[((start%len(band))+i+len(band))%len(band)]
		if !m.tpSelector.Saturated(idx) {
			return idx
		}
	}
	return -1
}

// forwardFanoutRoom reports whether a fanned-out forward direction has anywhere
// to put a frame. It is the send-window question for an upload striding a band,
// and answering it from the band rather than from every ready leg is what stops
// the writer being released against a leg too skewed to be used.
//
// A pure query: it neither advances the round-robin cursor nor charges the
// scheduler, because the parked writer asks it on every poll and the frame it
// is waiting to send has not been placed yet.
func (m *routeMux) forwardFanoutRoom(tps []*transport.ManagedTransport, cur int) bool {
	for _, idx := range m.forwardFanoutLegs(tps, cur) {
		if !m.tpSelector.Saturated(idx) {
			return true
		}
	}
	return false
}

// selectByDirection picks a leg matching this end's send direction. Under
// unidirectional assignment DIRECTION governs which CLASS of leg carries the
// send (direct vs multihop); WITHIN that class the initiator-mirrored active set
// (LegState / CapLegState) governs WHICH legs — so the exit's download fan-out
// stays bounded to the few legs the initiator parked active instead of spraying
// every warm-standby reverse leg.
//
// Four tiers, tried in order (each bounded to the wanted CLASS so the wrong
// direction — the direct leg on a download — is NEVER selected here):
//
//	Tier 1 (steady state): active (non-standby), ready, class-matching legs. The
//	   exit sends the download on the same small mirrored-active set the initiator
//	   selected, so the reorder frontier is not over-subscribed. The heavy
//	   direction spreads across THOSE legs via the weighted selector / round-robin.
//	Tier 2 (active-not-ready): an ACTIVE class leg whose readiness gate has not
//	   fired. Under CapUniDir the initiator sends its light direction ONLY on the
//	   direct leg, so the exit's active reverse leg may never receive inbound bulk
//	   and so is never markLegReady'd — yet its rules were installed on both ends
//	   when the leg joined the group, so it IS sendable. Preferring it here (over
//	   any ready STANDBY leg) is what confines the download to the one
//	   mirrored-active reverse leg instead of spraying the warm-standby reserve —
//	   the fix for the "active leg not preferred / download on standby legs" bug.
//	Tier 3 (warm-reserve failover): no active class leg at all (the initiator
//	   parked every leg of this direction, e.g. mid-rotation) — fall back to a
//	   ready STANDBY class leg. The #4319 bounding: still the wanted class, still
//	   the warm reserve; steady state never reaches here.
//	Tier 4 (last resort before the wrong direction): ANY class leg, ignoring both
//	   standby and readiness. A reverse leg that exists but is neither active nor
//	   "ready" is still the RIGHT CLASS, so sending on it preserves the #4311
//	   confinement guarantee (never the wrong-direction direct leg) when the group
//	   is momentarily between active/ready states.
//
// Returns ok=false only when there is genuinely NO leg of the wanted class at
// all — then selectTransport falls through and may use the direct leg, because
// there is no reverse leg to confine the download to.
func (m *routeMux) selectByDirection(tps []*transport.ManagedTransport, fwd []routing.Rule, wantDirect bool, dst, src cipher.PubKey) (*transport.ManagedTransport, routing.Rule, int, bool) {
	n := len(tps)
	if n == 0 || len(fwd) == 0 {
		return nil, nil, -1, false
	}
	// Base gate: a live, ruled leg whose CLASS (direct vs multihop) matches this
	// end's send direction. The tiers below layer readiness/standby on top.
	classOK := func(idx int) bool {
		tp := tps[idx]
		return idx < len(fwd) && tp != nil && !tp.IsClosed() &&
			legIsDirect(tp, dst, src) == wantDirect
	}
	start := int(atomic.AddUint32(&m.tpIndex, 1) - 1)
	scan := func(match func(idx int) bool) (*transport.ManagedTransport, routing.Rule, int, bool) {
		for i := 0; i < n; i++ {
			idx := ((start % n) + i) % n
			if idx < 0 {
				idx += n
			}
			if match(idx) {
				return tps[idx], fwd[idx], idx, true
			}
		}
		return nil, nil, -1, false
	}

	// Tier 1: active, ready, class-matching (legReadyAt already excludes standby).
	// Prefer the scheduler's pick when it qualifies, so the heavy direction spreads
	// across the active legs per the mux weights/ECF.
	//
	// The spread is bounded by the probe budget: the schedule this tier reads is
	// an UNWEIGHTED round-robin over the live legs for every predictive mode
	// (transportSelector.Rebuild), so without the gate a leg whose delay basis is
	// a multiple of its sibling's keeps taking every other frame of the download
	// — which is the 2026-09-18 collapse (8.3-37.4 s for 10 MB, each trial's
	// duration the bytes placed on the 60 KB/s leg divided by its rate). A leg
	// ruled probe-only still gets its probe per window, so it keeps being
	// measured and the ruling lifts by itself when it recovers.
	activeReady := func(idx int) bool { return classOK(idx) && m.legReadyAt(idx) }
	withBudget := func(idx int) bool { return activeReady(idx) && !m.legProbeExhausted(idx) }
	if m.tpSelector != nil && m.tpSelector.Len() > 0 {
		if idx := m.tpSelector.Select(); idx >= 0 && idx < n && withBudget(idx) {
			return tps[idx], fwd[idx], idx, true
		}
	}
	if tp, rule, idx, ok := scan(withBudget); ok {
		return tp, rule, idx, true
	}
	// Every active leg is out of probe budget (or the group has only outclassed
	// legs left): the download still has to go somewhere, so the budget is
	// advisory from here down and the original tiers decide.
	if tp, rule, idx, ok := scan(activeReady); ok {
		return tp, rule, idx, true
	}
	// Tier 2: active class leg, ignoring the readiness gate — the mirrored-active
	// reverse leg the exit must send bulk on even though it never received inbound.
	if tp, rule, idx, ok := scan(func(idx int) bool { return classOK(idx) && !m.isLegStandby(idx) }); ok {
		return tp, rule, idx, true
	}
	// Tier 3: ready standby class leg (warm-reserve failover; ignore standby flag).
	if tp, rule, idx, ok := scan(func(idx int) bool { return classOK(idx) && m.legSelectableIgnoringStandby(idx) }); ok {
		return tp, rule, idx, true
	}
	// Tier 4: any class leg at all (ignore both standby and readiness) — never the
	// wrong-direction direct leg while a reverse leg exists.
	if tp, rule, idx, ok := scan(classOK); ok {
		return tp, rule, idx, true
	}
	return nil, nil, -1, false
}
