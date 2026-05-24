// router_mux_ops.go contains mux route operations on the router.
package router

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/routing"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// appendRouteAsymmetric adds an aux mux leg, selectively appending
// the forward and/or reverse direction. addFwd=true && addRev=true is
// equivalent to appendRouteToGroup. When either flag is false, the
// corresponding rule is deleted from the routing table immediately
// (the setup-node always creates both sides; this is the cleanup for
// the unused side so it doesn't accumulate stale entries).
//
// Used by establishMuxRoutes when ForwardMuxRoutes != ReverseMuxRoutes
// to construct asymmetric mux topologies (e.g. 1 forward + N reverse
// for download-heavy workloads).
func (r *router) appendRouteAsymmetric(nrg *NoiseRouteGroup, rules routing.EdgeRules, addFwd, addRev bool) error {
	if !addFwd && !addRev {
		// Neither direction wanted — delete both rules and skip.
		r.rt.DelRules([]routing.RouteID{rules.Forward.KeyRouteID(), rules.Reverse.KeyRouteID()})
		return nil
	}

	if err := r.SaveRoutingRules(rules.Forward, rules.Reverse); err != nil {
		return fmt.Errorf("SaveRoutingRules: %w", err)
	}

	// Forward leg requires the transport (writes) + DMSG safety check.
	if addFwd {
		nextTpID := rules.Forward.NextTransportID()
		tp := r.tm.Transport(nextTpID)
		if tp == nil {
			r.rt.DelRules([]routing.RouteID{rules.Forward.KeyRouteID(), rules.Reverse.KeyRouteID()})
			return fmt.Errorf("transport %s not found for additional mux route", nextTpID)
		}
		if tp.Entry.Type == tptypes.DMSG {
			r.rt.DelRules([]routing.RouteID{rules.Forward.KeyRouteID(), rules.Reverse.KeyRouteID()})
			return errors.New("refusing to append DMSG transport to mux route group")
		}
		nrg.rg.mu.Lock()
		for _, existing := range nrg.rg.tps {
			if existing != nil && existing.Entry.Type == tptypes.DMSG {
				nrg.rg.mu.Unlock()
				r.rt.DelRules([]routing.RouteID{rules.Forward.KeyRouteID(), rules.Reverse.KeyRouteID()})
				return errors.New("refusing to mux: route group already contains a DMSG transport")
			}
		}
		nrg.rg.mu.Unlock()
		nrg.rg.appendForwardLeg(rules.Forward, tp)

		// Send handshake on the new forward transport.
		rg := nrg.rg
		rg.mu.Lock()
		lastIdx := len(rg.tps) - 1
		lastTp := rg.tps[lastIdx]
		lastRule := rg.fwd[lastIdx]
		rg.mu.Unlock()
		packet := routing.MakeHandshakePacket(lastRule.NextRouteID(), rg.encrypt, routing.CapMux|routing.CapSACK)
		if err := rg.writePacket(context.Background(), lastTp, packet, lastRule.KeyRouteID()); err != nil {
			r.logger.WithError(err).Warn("Failed to send handshake on additional mux transport")
		}
	} else {
		// Forward not wanted — drop the forward rule from routing table.
		r.rt.DelRules([]routing.RouteID{rules.Forward.KeyRouteID()})
	}

	if addRev {
		nrg.rg.appendReverseLeg(rules.Reverse)
	} else {
		// Reverse not wanted — drop the reverse rule from routing table.
		r.rt.DelRules([]routing.RouteID{rules.Reverse.KeyRouteID()})
	}

	r.logger.Debugf("Appended asymmetric mux route (fwd=%v rev=%v) to RouteGroup %s", addFwd, addRev, &rules.Desc)
	return nil
}

// appendRouteToGroup adds an additional transport/rule pair to an existing
// mux-enabled NoiseRouteGroup. Used for establishing additional parallel routes.
func (r *router) appendRouteToGroup(nrg *NoiseRouteGroup, rules routing.EdgeRules) error {
	if err := r.SaveRoutingRules(rules.Forward, rules.Reverse); err != nil {
		return fmt.Errorf("SaveRoutingRules: %w", err)
	}

	nextTpID := rules.Forward.NextTransportID()
	tp := r.tm.Transport(nextTpID)
	if tp == nil {
		return fmt.Errorf("transport %s not found for additional mux route", nextTpID)
	}

	// Reject any mux append that would introduce a DMSG transport into the group.
	// A dmsg server is an opaque intermediary; multiplexing routes that share or
	// overlap dmsg servers can loop traffic with no way to detect it.
	if tp.Entry.Type == tptypes.DMSG {
		return errors.New("refusing to append DMSG transport to mux route group")
	}
	nrg.rg.mu.Lock()
	for _, existing := range nrg.rg.tps {
		if existing != nil && existing.Entry.Type == tptypes.DMSG {
			nrg.rg.mu.Unlock()
			return errors.New("refusing to mux: route group already contains a DMSG transport")
		}
	}
	nrg.rg.mu.Unlock()

	nrg.rg.appendRules(rules.Forward, rules.Reverse, tp)

	// Send handshake on the new transport to inform the remote side
	rg := nrg.rg
	rg.mu.Lock()
	lastIdx := len(rg.tps) - 1
	lastTp := rg.tps[lastIdx]
	lastRule := rg.fwd[lastIdx]
	rg.mu.Unlock()

	packet := routing.MakeHandshakePacket(lastRule.NextRouteID(), rg.encrypt, routing.CapMux|routing.CapSACK)
	if err := rg.writePacket(context.Background(), lastTp, packet, lastRule.KeyRouteID()); err != nil {
		r.logger.WithError(err).Warn("Failed to send handshake on additional mux transport")
	}

	r.logger.Debugf("Appended mux route via transport %s to RouteGroup %s", nextTpID, &rules.Desc)
	return nil
}

// AddMuxRouteByHops adds a leg over the caller-supplied route to an
// existing mux'd route group. The hop lists have the same shape as
// 'cli route calc' emits (forward = lPK → ... → rPK, reverse = rPK
// → ... → lPK). The router does not consult the route finder; the
// caller is responsible for path selection.
//
// Sanity gates:
//   - fwd and rev must be non-empty and end at the right peer.
//   - The forward path must start with a transport this visor owns
//     (since this visor is the entry point).
//   - The first transport must not already be a leg in the rg —
//     two legs sharing the same first hop bottleneck on that link
//     and don't give the diversity mux exists for.
//
// Deeper duplication checks (full path equality across intermediate
// hops) are out of scope: the local visor only sees its own first
// hop after Dial completes, so checking past hop 0 is best-effort.
func (r *router) AddMuxRouteByHops(desc routing.RouteDescriptor, fwd, rev []routing.Hop) error {
	r.mx.Lock()
	nrg, ok := r.rgsNs[desc]
	r.mx.Unlock()
	if !ok {
		return fmt.Errorf("no active route group for %s", desc.String())
	}

	if nrg.rg.mux == nil {
		return errors.New("route group does not have mux enabled")
	}

	lPK := desc.DstPK()
	rPK := desc.SrcPK()
	log := r.scopedLog(desc.SrcPort())

	if len(fwd) == 0 || len(rev) == 0 {
		return errors.New("empty forward or reverse path")
	}
	if fwd[0].From != lPK {
		return fmt.Errorf("forward path must start at this visor (%s), got %s", lPK, fwd[0].From)
	}
	if fwd[len(fwd)-1].To != rPK {
		return fmt.Errorf("forward path must end at peer (%s), got %s", rPK, fwd[len(fwd)-1].To)
	}

	tpID := fwd[0].TpID
	tp := r.tm.Transport(tpID)
	if tp == nil {
		return fmt.Errorf("first-hop transport %s not found locally", tpID)
	}

	// Reject if the new leg starts on a transport that's already a
	// leg in the rg. The finder used to silently re-pick the rg's
	// existing first hop, defeating mux; refusing here surfaces the
	// duplication at the API boundary.
	nrg.rg.mu.Lock()
	for i, existing := range nrg.rg.tps {
		if existing != nil && existing.Entry.ID == tpID {
			nrg.rg.mu.Unlock()
			return fmt.Errorf("transport %s is already leg %d in this route group", tpID, i)
		}
	}
	nrg.rg.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	keepAlive := DefaultRouteKeepAlive
	forwardDesc := routing.NewRouteDescriptor(lPK, rPK, desc.DstPort(), desc.SrcPort())
	req := routing.BidirectionalRoute{
		Desc:      forwardDesc,
		KeepAlive: keepAlive,
		Forward:   fwd,
		Reverse:   rev,
	}

	rules, _, err := r.conf.RouteGroupDialer.Dial(ctx, log, r.dmsgC, r.conf.SetupNodes, req)
	if err != nil {
		return fmt.Errorf("route setup failed: %w", err)
	}

	if err := r.appendRouteToGroup(nrg, rules); err != nil {
		return fmt.Errorf("append route failed: %w", err)
	}

	r.logger.Infof("Added mux route via %d-hop path (first tp=%s) to route group %s", len(fwd), tpID, desc.String())
	return nil
}

// RemoveMuxRouteByTransport removes a specific transport's route from a mux group.
func (r *router) RemoveMuxRouteByTransport(desc routing.RouteDescriptor, tpID uuid.UUID) error {
	r.mx.Lock()
	nrg, ok := r.rgsNs[desc]
	r.mx.Unlock()
	if !ok {
		return fmt.Errorf("no active route group for %s", desc.String())
	}

	rg := nrg.rg
	rg.mu.Lock()
	defer rg.mu.Unlock()

	if len(rg.tps) <= 1 {
		return errors.New("cannot remove the last route from a route group")
	}

	// Find and remove the transport
	idx := -1
	for i, tp := range rg.tps {
		if tp != nil && tp.Entry.ID == tpID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("transport %s not found in route group", tpID)
	}

	// Collect rule IDs to delete
	var deadRuleIDs []routing.RouteID
	if idx < len(rg.fwd) {
		deadRuleIDs = append(deadRuleIDs, rg.fwd[idx].KeyRouteID())
	}
	if idx < len(rg.rvs) {
		deadRuleIDs = append(deadRuleIDs, rg.rvs[idx].KeyRouteID())
	}

	// Remove from slices
	rg.tps = append(rg.tps[:idx], rg.tps[idx+1:]...)
	rg.fwd = append(rg.fwd[:idx], rg.fwd[idx+1:]...)
	if idx < len(rg.rvs) {
		rg.rvs = append(rg.rvs[:idx], rg.rvs[idx+1:]...)
	}

	// Clean up rules
	if len(deadRuleIDs) > 0 {
		rg.rt.DelRules(deadRuleIDs)
	}

	// Rebuild selector
	if rg.mux != nil {
		rg.mux.rebuildWeights(rg.tps)
	}

	r.logger.Infof("Removed mux route via transport %s from route group %s", tpID, desc.String())
	return nil
}
