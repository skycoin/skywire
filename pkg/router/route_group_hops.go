// Package router pkg/router/route_group_hops.go c2-net-routing
package router

import (
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// RouteHops returns the list of visor public keys that form the route path.
// The first element is the first hop from the source, and the last element
// is the destination visor.
func (rg *RouteGroup) RouteHops() []cipher.PubKey {
	rg.mu.Lock()
	defer rg.mu.Unlock()

	hops := make([]cipher.PubKey, 0, len(rg.legs)+1)
	for _, lg := range rg.legs {
		tp := lg.tp
		if tp != nil {
			hops = append(hops, tp.Remote())
		}
	}
	// Add destination from the route descriptor
	hops = append(hops, rg.desc.DstPK())
	return hops
}

// SetForwardHops sets the complete forward route hops.
// This should be called after route setup to store the full route path.
func (rg *RouteGroup) SetForwardHops(hops []routing.Hop) {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	rg.forwardHops = hops
	// Also record under the leg's first-hop transport ID so the per-leg mux
	// view can show the primary leg's whole path (aux legs are recorded by
	// AddMuxRouteByHops → recordLegHops).
	if len(hops) > 0 {
		rg.legForwardHops[hops[0].TpID] = hops
	}
}

// recordLegRoute stores a mux leg's full forward route keyed by its first-hop
// transport ID, AND the far end's first-hop transport for that leg, taken from
// the reverse path the leg was dialed with. Called by every aux-mux-leg commit path and by
// the primary dial, so remoteLegTransportIDs mirrors the peer's live leg
// transports. A nil/empty rev records the forward route only (best-effort: an
// unrecorded leg simply contributes no exclusion).
func (rg *RouteGroup) recordLegRoute(fwd, rev []routing.Hop) {
	if len(fwd) == 0 {
		return
	}
	rg.mu.Lock()
	rg.legForwardHops[fwd[0].TpID] = fwd
	if len(rev) > 0 {
		rg.legRemoteTp[fwd[0].TpID] = rev[0].TpID
	}
	rg.mu.Unlock()
}

// remoteLegTransportIDs returns the far end's first-hop transport ID for every
// LIVE leg of this group — the peer's own rg.legs set, as far as this side
// recorded it. A planned aux leg whose Reverse[0].TpID is in this set is
// guaranteed to be refused by the peer's appendRouteToGroup/appendRouteAsymmetric
// duplicate-transport guard, so the planners drop it BEFORE the setup-node dial.
//
// Derived from the live legs on every call (not from an accumulating set), so a
// leg that has been pruned locally immediately stops excluding its remote
// transport and a replacement leg over that same far-end link can be built
// again. Callers must NOT hold rg.mu.
func (rg *RouteGroup) remoteLegTransportIDs() []uuid.UUID {
	if rg == nil {
		return nil
	}
	rg.mu.Lock()
	defer rg.mu.Unlock()
	if len(rg.legRemoteTp) == 0 {
		return nil
	}
	out := make([]uuid.UUID, 0, len(rg.legs))
	for _, lg := range rg.legs {
		tp := lg.tp
		if tp == nil {
			continue
		}
		if remote, ok := rg.legRemoteTp[tp.Entry.ID]; ok {
			out = append(out, remote)
		}
	}
	return out
}

// legHopsFor returns a copy of the full forward route for the leg on
// transport tpID (nil if not recorded). Callers must NOT hold rg.mu.
func (rg *RouteGroup) legHopsFor(tpID uuid.UUID) []routing.Hop {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	h, ok := rg.legForwardHops[tpID]
	if !ok {
		return nil
	}
	return append([]routing.Hop(nil), h...)
}

// RouteHopDetails returns detailed information about each hop in the route,
// including transport IDs and types.
func (rg *RouteGroup) RouteHopDetails() []RouteHopInfo {
	rg.mu.Lock()
	defer rg.mu.Unlock()

	// Use stored forward hops if available (preferred - has complete route)
	if len(rg.forwardHops) > 0 {
		hops := make([]RouteHopInfo, len(rg.forwardHops))
		for i, hop := range rg.forwardHops {
			// Derive transport type from the transport ID
			// The ID is deterministically generated from (keyA, keyB, type)
			tpType := transport.TypeFromTransportID(hop.TpID, hop.From, hop.To)
			hops[i] = RouteHopInfo{
				TpID:   hop.TpID.String(),
				From:   hop.From.String(),
				To:     hop.To.String(),
				TpType: string(tpType),
			}
		}
		return hops
	}

	// Fallback: reconstruct from local transports (may be incomplete for multi-hop)
	srcPK := rg.desc.SrcPK()
	hops := make([]RouteHopInfo, 0, len(rg.legs))
	for i, lg := range rg.legs {
		tp := lg.tp
		if tp == nil {
			continue
		}
		var fromPK cipher.PubKey
		if i == 0 {
			fromPK = srcPK
		} else if i > 0 && rg.legs[i-1].tp != nil {
			fromPK = rg.legs[i-1].tp.Remote()
		}
		hops = append(hops, RouteHopInfo{
			TpID:   tp.Entry.ID.String(),
			From:   fromPK.String(),
			To:     tp.Remote().String(),
			TpType: string(tp.Type()),
		})
	}
	return hops
}

// appendLegLocked adds one leg and returns its index. reverse is nil on a
// forward-only leg.
func (rg *RouteGroup) appendLegLocked(forward, reverse routing.Rule, tp *transport.ManagedTransport) int {
	rg.legs = append(rg.legs, groupLeg{tp: tp, fwd: forward, rvs: reverse})
	return len(rg.legs) - 1
}

// legByConsumeRuleLocked returns the leg whose reverse rule is routeID, or -1.
// A reverse-only rule belongs to no leg. Caller holds rg.mu.
func (rg *RouteGroup) legByConsumeRuleLocked(routeID routing.RouteID) int {
	for i := range rg.legs {
		if rv := rg.legs[i].rvs; rv != nil && rv.KeyRouteID() == routeID {
			return i
		}
	}
	return -1
}

// appendRules adds a forward/reverse rule pair and its transport as a new leg.
// reason names what asked for the leg ("route setup", "mux append", …); it is
// what the leg_added mux event carries.
func (rg *RouteGroup) appendRules(forward, reverse routing.Rule, tp *transport.ManagedTransport, reason string) {
	rg.mu.Lock()

	newIdx := rg.appendLegLocked(forward, reverse, tp)
	rg.resetLegLiveness(tpEntryID(tp))
	legs := len(rg.legs)

	// Rebuild transport weights when transports change
	if rg.mux != nil && len(rg.legs) > 1 {
		rg.mux.rebuildWeights(rg.legs)
	}
	// Keep per-leg counters parallel to legs.
	if rg.mux != nil {
		rg.mux.growLegs(len(rg.legs))
	}
	rg.noteLegEvent(MuxEventLegAdded, reason, MuxByLocal, newIdx, legs, tp, rg.legHopsLocked(tpEntryID(tp)))
	// Initial single-leg rg's get their first leg via appendRules
	// too, but the policy doesn't need on_leg_change to fire for
	// that — the BeforeDial/SelectRoute knobs already handled it.
	// Only fire on subsequent appends (mux growth).
	hookSet := rg.legChangeHook != nil && newIdx > 0
	rg.mu.Unlock()

	// A second leg is what the leg-gated loops were parked waiting for.
	rg.signalServiceWake()

	if hookSet {
		rg.fireLegChange("added", newIdx)
	}
}

// appendForwardLeg adds a forward leg (rule + transport) WITHOUT a
// matching reverse rule. Used by asymmetric mux setups where the
// ForwardMuxRoutes count exceeds ReverseMuxRoutes — e.g. a workload
// with bulk upstream but tiny downstream. The reverse-direction
// rule from the same setup-node call is discarded by the caller
// (router.appendRouteAsymmetric) so it doesn't leak in the routing
// table.
//
// Mirrors appendRules but holds a nil in the leg's rvs field. Mux weight
// + per-leg counter bookkeeping treats
// this as a new forward leg.
func (rg *RouteGroup) appendForwardLeg(forward routing.Rule, tp *transport.ManagedTransport, reason string) {
	rg.mu.Lock()

	newIdx := rg.appendLegLocked(forward, nil, tp)
	rg.resetLegLiveness(tpEntryID(tp))
	legs := len(rg.legs)

	if rg.mux != nil && len(rg.legs) > 1 {
		rg.mux.rebuildWeights(rg.legs)
	}
	if rg.mux != nil {
		rg.mux.growLegs(len(rg.legs))
	}
	rg.noteLegEvent(MuxEventLegAdded, reason, MuxByLocal, newIdx, legs, tp, rg.legHopsLocked(tpEntryID(tp)))
	hookSet := rg.legChangeHook != nil
	rg.mu.Unlock()

	// A second leg is what the leg-gated loops were parked waiting for.
	rg.signalServiceWake()

	if hookSet {
		rg.fireLegChange("added", newIdx)
	}
}

// appendReverseLeg adds a reverse rule WITHOUT a paired forward
// rule + transport. Used by asymmetric mux setups where
// ReverseMuxRoutes exceeds ForwardMuxRoutes — the operator's
// canonical bandwidth-asymmetric case (1 forward + N reverse for
// download-heavy workloads).
//
// The rule is kept in rvsOnly, apart from the legs, so it shifts no leg; the read
// path is by route-id lookup (not slice-indexed) so this works without further
// data-plane changes. Per-leg counter slots (mux.legs) are NOT
// extended here — they are parallel to legs and the reverse-only
// leg doesn't add a forward transport. Per-direction recv-byte
// attribution for reverse-only legs is a separate refinement.
func (rg *RouteGroup) appendReverseLeg(reverse routing.Rule) {
	rg.mu.Lock()
	defer rg.mu.Unlock()

	rg.rvsOnly = append(rg.rvsOnly, reverse)
}

// RouteHopInfo contains detailed information about a single hop in a route.
type RouteHopInfo struct {
	TpID   string `json:"tp_id"`   // Transport ID
	From   string `json:"from"`    // Source public key (full, never truncated)
	To     string `json:"to"`      // Destination public key (full, never truncated)
	TpType string `json:"tp_type"` // Transport type (stcpr, sudph, dmsg)
	// LatencyMS is this hop's transport RTT in ms, when known. The first
	// hop is owned locally (tp.GetLatency). For a single-intermediate leg
	// the far hop is derived (route RTT − first-hop RTT). Deeper hops are
	// 0 here until sourced from TPD / the setup node's per-transport data.
	LatencyMS float64 `json:"latency_ms,omitempty"`
}
