// Package router pkg/router/unidir.go c2-net-routing
package router

import (
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
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
	unidirFlipInterval = 1 * time.Second // flip-controller tick cadence
	flipRatio          = 2.0             // flip when the heavy direction ≥ this × the light one
	flipHysteresis     = 3               // consecutive qualifying ticks before flipping
	flipCooldownTicks  = 3               // ticks to hold after a flip before another
	flipMinGoodput     = 8192.0          // bytes/sec floor — ignore near-idle noise
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
	m.legMu.RUnlock()
	if !directional {
		return m.flipped, false
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
	m.legMu.RUnlock()
	if !directional {
		return cur, false
	}
	if m.flipCooldown > 0 {
		m.flipCooldown--
		return cur, false
	}

	switch {
	case up > flipMinGoodput && up >= flipRatio*down:
		m.flipUpHits++
		m.flipDownHits = 0
	case down > flipMinGoodput && down >= flipRatio*up:
		m.flipDownHits++
		m.flipUpHits = 0
	default:
		m.flipUpHits, m.flipDownHits = 0, 0
	}

	if !cur && m.flipUpHits >= flipHysteresis && m.setFlipped(true) {
		m.flipCooldown = flipCooldownTicks
		m.flipUpHits = 0
		return true, true
	}
	if cur && m.flipDownHits >= flipHysteresis && m.setFlipped(false) {
		m.flipCooldown = flipCooldownTicks
		m.flipDownHits = 0
		return false, true
	}
	return cur, false
}

// selectByDirection picks a leg matching this end's send direction. Under
// unidirectional assignment DIRECTION governs which CLASS of leg carries the
// send (direct vs multihop); WITHIN that class the initiator-mirrored active set
// (LegState / CapLegState) governs WHICH legs — so the exit's download fan-out
// stays bounded to the few legs the initiator parked active instead of spraying
// every warm-standby reverse leg.
//
// Two tiers:
//
//	Tier 1 (normal): active (non-standby), ready, direction-matching legs. This
//	   honors the mirrored active set — the exit sends the download on the same
//	   small set the initiator selected, so the reorder frontier is not
//	   over-subscribed and retransmits do not amplify. The heavy direction still
//	   spreads across THOSE legs via the weighted selector / round-robin.
//	Tier 2 (degenerate): if NO active direction-matching leg exists — the
//	   initiator parked every leg of this direction as standby, e.g. mid-rotation
//	   — fall back to any alive, ready standby leg of the wanted direction. This
//	   keeps confinement (the #4311 guarantee: never fall through to the
//	   standby-aware path, which could pick the wrong-direction direct leg) while
//	   still bounding to the wanted class. It is the warm-reserve failover, not
//	   the steady state.
//
// Returns ok=false only when NO direction-matching leg is selectable at all, so
// selectTransport falls through to the standard path rather than dropping the send.
func (m *routeMux) selectByDirection(tps []*transport.ManagedTransport, fwd []routing.Rule, wantDirect bool, dst, src cipher.PubKey) (*transport.ManagedTransport, routing.Rule, int, bool) {
	n := len(tps)
	if n == 0 || len(fwd) == 0 {
		return nil, nil, -1, false
	}
	// Tier 1: active, ready, direction-matching (standby-aware — bounds fan-out).
	matchActive := func(idx int) bool {
		tp := tps[idx]
		return idx < len(fwd) && tp != nil && !tp.IsClosed() &&
			m.legReadyAt(idx) && legIsDirect(tp, dst, src) == wantDirect
	}
	// Prefer the scheduler's pick when it matches AND is active, so the heavy
	// direction spreads across the active legs per the mux weights/ECF.
	if m.tpSelector != nil && m.tpSelector.Len() > 0 {
		if idx := m.tpSelector.Select(); idx >= 0 && idx < n && matchActive(idx) {
			return tps[idx], fwd[idx], idx, true
		}
	}
	start := int(atomic.AddUint32(&m.tpIndex, 1) - 1)
	for i := 0; i < n; i++ {
		idx := ((start % n) + i) % n
		if idx < 0 {
			idx += n
		}
		if matchActive(idx) {
			return tps[idx], fwd[idx], idx, true
		}
	}
	// Tier 2: no ACTIVE direction-matching leg — warm-reserve failover on the
	// wanted class only (confinement preserved). Rare; logged by the caller.
	matchAny := func(idx int) bool {
		tp := tps[idx]
		return idx < len(fwd) && tp != nil && !tp.IsClosed() &&
			m.legSelectableIgnoringStandby(idx) && legIsDirect(tp, dst, src) == wantDirect
	}
	for i := 0; i < n; i++ {
		idx := ((start % n) + i) % n
		if idx < 0 {
			idx += n
		}
		if matchAny(idx) {
			return tps[idx], fwd[idx], idx, true
		}
	}
	return nil, nil, -1, false
}
