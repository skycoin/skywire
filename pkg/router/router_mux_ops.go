// router_mux_ops.go contains mux route operations on the router.
package router

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/routing"
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

// AddMuxRouteByTransport adds a new mux route to an existing route group,
// using the specified transport for the first hop. The route is established
// via the embedded setup node.
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

	// Build a route that uses this specific transport
	opts := &DialOptions{
		MinForwardRts: 1,
		MaxForwardRts: 1,
		MinConsumeRts: 1,
		MaxConsumeRts: 1,
		Retries:       1,
		ExcludeDMSG:   true,
	}

	// Verify the transport exists
	tp := r.tm.Transport(tpID)
	if tp == nil {
		return fmt.Errorf("transport %s not found", tpID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fwd, rev, err := r.fetchBestRoutes(ctx, lPK, rPK, opts)
	if err != nil {
		return fmt.Errorf("failed to find route via transport: %w", err)
	}

	forwardDesc := routing.NewRouteDescriptor(lPK, rPK, desc.DstPort(), desc.SrcPort())
	req := routing.BidirectionalRoute{
		Desc:      forwardDesc,
		KeepAlive: DefaultRouteKeepAlive,
		Forward:   fwd,
		Reverse:   rev,
	}

	rules, _, err := r.conf.RouteGroupDialer.Dial(ctx, r.logger, r.dmsgC, r.conf.SetupNodes, req)
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
