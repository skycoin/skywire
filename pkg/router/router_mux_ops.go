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

// AddMuxRouteByTransport adds a leg over the named transport to an
// existing mux'd route group. Pre-release semantics:
//
//   - tpID must not already be a leg in the rg (rejects the obvious
//     duplicate; the route finder otherwise loves to re-pick the same
//     first hop).
//   - tpID must go directly to the peer (tp.Remote() == rPK). The
//     1-hop route is built from the user's pick; we don't ask the
//     finder to extend a pinned first hop into a multi-hop path.
//
// Multi-hop (transport-to-intermediate) is deferred — callers who
// need it should compute the path off-router via 'route calc' and
// pass it through a future hops-list form. "Auto pick a disjoint
// leg" is also deferred until the route finder honors
// ExcludeTransportIDs in the multi-hop branch.
func (r *router) AddMuxRouteByTransport(desc routing.RouteDescriptor, tpID uuid.UUID) error {
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

	// Reject if this transport is already a leg in the rg. The
	// finder used to silently re-pick the rg's primary first-hop;
	// surfacing the duplication explicitly stops that mistake at
	// the API boundary instead of letting it land as a "leg added"
	// that does nothing for path diversity.
	nrg.rg.mu.Lock()
	for i, existing := range nrg.rg.tps {
		if existing != nil && existing.Entry.ID == tpID {
			nrg.rg.mu.Unlock()
			return fmt.Errorf("transport %s is already leg %d in this route group", tpID, i)
		}
	}
	nrg.rg.mu.Unlock()

	tp := r.tm.Transport(tpID)
	if tp == nil {
		return fmt.Errorf("transport %s not found", tpID)
	}

	// Direct-to-peer only for now; multi-hop is deferred.
	tpRemote := tp.Remote()
	if tpRemote != rPK {
		return fmt.Errorf("transport %s goes to %s, not direct to peer %s; multi-hop add-mux is deferred", tpID, tpRemote, rPK)
	}

	fwd := []routing.Hop{{TpID: tpID, From: lPK, To: rPK}}
	rev := []routing.Hop{{TpID: tpID, From: rPK, To: lPK}}

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

	r.logger.Infof("Added mux route via transport %s to route group %s", tpID, desc.String())
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
