// Package router pkg/router/router_pending_legs.go c2-net-routing
package router

import (
	"context"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// Responder-side mux-leg setup-race mitigation (#80).
//
// A route group is registered under rgsNs only AFTER its 10s-bounded setup
// handshake completes; while it is initializing it lives under rgsRaw and its
// rg.mux is not yet set (the responder creates the mux lazily when the primary
// forward handshake lands). An additional (aux) mux leg for the SAME descriptor
// arrives independently via AddEdgeRules -> IntroduceRules. If that aux leg
// lands in the initialization window it can neither be appended (mux may be nil)
// nor treated as a new group (it shares the primary's descriptor) — the old code
// pushed it to r.accept, where AcceptRoutes' second saveRouteGroupRules either
// deleted the leg's freshly-installed rules or blocked the whole handshake-await
// timeout, collapsing the group back toward a single leg.
//
// Instead of dropping or misrouting such a leg, we buffer it here and drain it
// through appendRouteToGroup the moment the primary registers into rgsNs. This
// mirrors the transport-frame parking in router_pending.go, one level up: that
// parks a packet whose route group has not registered; this parks a whole leg
// whose route group has not registered.
const (
	// pendingLegTTL must outlast the responder's full handshake-await window
	// (handshakeAwaitTimeout, router.go) so a leg buffered at the very start of
	// the initialization window is still deliverable when the primary registers.
	// A group that never registers (its handshake failed) has its raw entry
	// deleted; the buffered leg is then reclaimed by the sweep at this TTL.
	pendingLegTTL = handshakeAwaitTimeout + 2*time.Second
	// Memory bounds: aux legs for descriptors that never register must not grow
	// the buffer without limit. A single group tops out well under this per-desc
	// cap (mux legs are capped far lower); the cap only guards against churn.
	maxPendingLegsPerDesc = 32
	maxPendingLegsTotal   = 2048
)

type pendingLeg struct {
	rules    routing.EdgeRules
	deadline time.Time
}

// pendingLegs buffers aux mux legs whose route group is still initializing
// (present in rgsRaw, not yet rgsNs). It is safe for concurrent use.
type pendingLegs struct {
	mu     sync.Mutex
	byDesc map[routing.RouteDescriptor][]pendingLeg
	total  int
}

func newPendingLegs() *pendingLegs {
	return &pendingLegs{byDesc: make(map[routing.RouteDescriptor][]pendingLeg)}
}

// park stores an aux leg's rules for desc until the route group registers
// (take) or the TTL expires (sweep). Returns false when the buffer is full, in
// which case the caller reports the append failure to the setup node.
func (p *pendingLegs) park(desc routing.RouteDescriptor, rules routing.EdgeRules, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.total >= maxPendingLegsTotal || len(p.byDesc[desc]) >= maxPendingLegsPerDesc {
		return false
	}
	p.byDesc[desc] = append(p.byDesc[desc], pendingLeg{rules: rules, deadline: now.Add(pendingLegTTL)})
	p.total++
	return true
}

// take removes and returns all legs parked for desc. Called when the route
// group for desc registers into rgsNs.
func (p *pendingLegs) take(desc routing.RouteDescriptor) []pendingLeg {
	p.mu.Lock()
	defer p.mu.Unlock()
	legs := p.byDesc[desc]
	if len(legs) == 0 {
		return nil
	}
	delete(p.byDesc, desc)
	p.total -= len(legs)
	return legs
}

// sweep drops legs past their deadline and returns the count dropped.
func (p *pendingLegs) sweep(now time.Time) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	dropped := 0
	for desc, legs := range p.byDesc {
		kept := legs[:0]
		for _, pl := range legs {
			if now.Before(pl.deadline) {
				kept = append(kept, pl)
			} else {
				dropped++
			}
		}
		if len(kept) == 0 {
			delete(p.byDesc, desc)
		} else {
			p.byDesc[desc] = kept
		}
	}
	p.total -= dropped
	return dropped
}

// runSweep periodically drops expired buffered legs until ctx is done. Buffered
// legs are normally taken within the setup window (on route-group registration);
// the sweep only reclaims legs for descriptors that never register.
func (p *pendingLegs) runSweep(ctx context.Context, log *logging.Logger) {
	t := time.NewTicker(pendingSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if n := p.sweep(now); n > 0 && log != nil {
				log.WithField("dropped", n).Debug("Dropped buffered aux mux legs whose route group never registered")
			}
		}
	}
}
