// Package router pkg/router/transport_close.go c2-net-routing
//
// What happens to a route group when a transport underneath it goes away.
//
// A leg is a (transport, forward rule, consume rule) triple, and nothing used
// to tell a leg that its transport had closed. The transport manager has fired
// a close hook since the VStream muxes needed one (transport.Manager.
// OnTransportClosed), but the router never registered: it found out by polling
// tp.IsClosed() from the keep-alive and leg-liveness services, and both of
// those step AROUND a closed transport rather than rule on it.
// legLivenessServiceFn skips a closed leg, so that leg is never probed, never
// missed and never declared dead; pruneDeadTransports refuses to drop the last
// leg. A single-leg group whose transport the visor itself removed therefore
// kept all of its rules and stayed registered, and the session on top only
// learned of the death when one of its own writes happened to land on the dead
// transport and error.
//
// On a download that write can be a long time coming: the bytes stop, so the
// app consumes nothing more, so it emits no window update, so nothing is
// written, so nothing errors. Three runs of near-identical trees on the
// standby-pool bench measured the same cut at 0.5 s, 5.3 s and 14.4 s to first
// byte — the spread was entirely "when does this side next write?", not
// anything about the failover, which is same-tick once the session errors.
//
// A transport the visor removed is not a silent peer. The liveness probe is
// for peers that went quiet; this is a link we KNOW is gone, so it is ruled on
// at the close rather than at a timeout.
package router

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/transport"
)

// anyLiveTransport reports whether any of tps can still carry a packet. A
// group with none of them cannot receive a close reply, so it must not wait
// for one.
func anyLiveTransport(tps []*transport.ManagedTransport) bool {
	for _, tp := range tps {
		if tp != nil && !tp.IsClosed() {
			return true
		}
	}
	return false
}

// handleTransportClosed rules on tpID having closed under this group and
// reports how many legs it accounted for.
//
// A leg that still has siblings is pruned, exactly as the liveness service
// prunes a black-holing one. When the closed transport carried EVERY leg the
// group is closed instead: it cannot carry another byte, and a group that
// lingers is a session above it that lingers with it.
func (rg *RouteGroup) handleTransportClosed(tpID uuid.UUID) int {
	if tpID == uuid.Nil || rg.isClosed() {
		return 0
	}

	rg.mu.Lock()
	legs := len(rg.tps)
	hit := 0
	for _, tp := range rg.tps {
		if tpEntryID(tp) == tpID {
			hit++
		}
	}
	rg.mu.Unlock()

	if hit == 0 {
		return 0
	}

	if hit < legs {
		return rg.pruneDeadLegs([]uuid.UUID{tpID},
			fmt.Sprintf("transport %v closed under the leg", tpID), "transport-closed")
	}

	rg.setCloseReason(fmt.Sprintf("transport %v closed under the group's only leg", tpID))
	// Close off the hook's goroutine: close hooks run on the transport
	// manager's close path and must not block, and rg.Close broadcasts close
	// packets before it returns.
	go func() { rg.Close() }() //nolint:errcheck,gosec
	return hit
}

// closeLegsOnTransport is the router's transport-close hook, registered with
// the transport manager in New.
//
// It rules OFF the caller's goroutine. A close hook fires wherever the
// transport closed, and one of those places is inside a write:
// ManagedTransport.WritePacket closes the transport when the write errors, and
// RouteGroup.sendKeepAlive writes with rg.mu held. Ruling inline would take
// rg.mu on the goroutine that already holds it. The manager ignores the count,
// so this returns 0 and ruleOnTransportClosed carries the number for tests.
func (r *router) closeLegsOnTransport(tpID uuid.UUID) int {
	go r.ruleOnTransportClosed(tpID)
	return 0
}

// ruleOnTransportClosed hands tpID to every route group this router holds and
// returns the number of legs that rode it.
func (r *router) ruleOnTransportClosed(tpID uuid.UUID) int {
	r.mx.Lock()
	groups := make([]*RouteGroup, 0, len(r.rgsNs)+len(r.rgsRaw))
	for _, nrg := range r.rgsNs {
		if nrg != nil && nrg.rg != nil {
			groups = append(groups, nrg.rg)
		}
	}
	for _, rg := range r.rgsRaw {
		if rg != nil {
			groups = append(groups, rg)
		}
	}
	r.mx.Unlock()

	// Outside r.mx: handleTransportClosed takes rg.mu and can start a close,
	// and the close path calls back into the router to drop the group.
	legs := 0
	for _, rg := range groups {
		legs += rg.handleTransportClosed(tpID)
	}
	if legs > 0 {
		r.logger.Infof("transport %v closed: ruled on %d route-group leg(s) at the close", tpID, legs)
	}
	return legs
}
