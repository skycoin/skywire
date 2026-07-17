// Package router pkg/router/router_gc.go c2-net-routing
// router_gc.go contains garbage collection for routing rules.
package router

import (
	"time"

	"github.com/skycoin/skywire/pkg/routing"
)

func (r *router) rulesGCLoop() {
	ticker := time.NewTicker(r.conf.RulesGCInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.done:
			return
		case <-ticker.C:
			r.rulesGC()
		}
	}
}

func (r *router) rulesGC() {
	log := r.logger.WithField("func", "router.rulesGC")

	removedRules := r.rt.CollectGarbage()
	if len(removedRules) > 0 {
		log.WithField("rules_count", len(removedRules)).
			Debug("Removed rules.")
	}

	for _, rule := range removedRules {
		r.removeRouteGroupOfRule(rule)
	}

	r.sweepClosedRouteGroups()
}

// sweepClosedRouteGroups removes route-group map entries whose underlying
// RouteGroup has already closed itself.
//
// Most teardowns are reaped via removeRouteGroupOfRule (the rules expire,
// CollectGarbage returns them, the rg is popped). But a route group that
// closes ITSELF — the keep-alive failure path (maxConsecutiveWriteFailures →
// rg.Close), a self-heal/rotation-induced close, or a close whose rules it
// already deleted in RouteGroup.close — leaves an orphaned entry in rgsNs/
// rgsRaw: the rg deleted its own rules from the routing table, so
// CollectGarbage never returns them and removeRouteGroupOfRule is never
// invoked for that descriptor. Under sustained setup/teardown/failure churn
// these orphaned entries accumulate without bound (each is also walked by
// SetMuxMode / ActiveRouteStatuses / RouteGroupMuxInfoForApp, so the leak
// also slows those scans). This additive sweep drops them; it changes no
// routing semantics — only already-closed groups are removed.
func (r *router) sweepClosedRouteGroups() {
	r.mx.Lock()
	defer r.mx.Unlock()
	for desc, nrg := range r.rgsNs {
		if nrg == nil || nrg.isClosed() {
			delete(r.rgsNs, desc)
			// Couple the faithful-UDP sibling's lifetime to the reliable
			// route (#2607): when the reliable group is reaped, reap its
			// datagram sibling too. dg.Close is independent of r.mx.
			if dg := r.rgsDatagrams[desc]; dg != nil {
				delete(r.rgsDatagrams, desc)
				dg.Close() //nolint:errcheck,gosec // idempotent, always returns nil
			}
		}
	}
	for desc, rg := range r.rgsRaw {
		if rg == nil || rg.isClosed() {
			delete(r.rgsRaw, desc)
		}
	}
	// Defensive: drop any datagram sibling that closed itself
	// (e.g. consecutive write failures tripped its IsAlive threshold).
	for desc, dg := range r.rgsDatagrams {
		if dg == nil || dg.isClosed() {
			delete(r.rgsDatagrams, desc)
		}
	}
}

func (r *router) removeRouteGroupOfRule(rule routing.Rule) {
	log := r.logger.
		WithField("func", "router.removeRouteGroupOfRule").
		WithField("rule_type", rule.Type().String()).
		WithField("rule_keyRtID", rule.KeyRouteID())

	// we need to process only consume rules, cause we don't
	// really care about the other ones, other rules removal
	// doesn't affect our work here
	if rule.Type() != routing.RuleReverse {
		log.Debug("Nothing to be done")
		return
	}

	rDesc := rule.RouteDescriptor()
	log.WithField("rt_desc", rDesc.String()).
		Debug("Closing route group associated with rule...")

	// Reap the faithful-UDP sibling (if any) with the reliable route (#2607);
	// fires on every return path below. No-op when no sibling exists.
	defer r.closeDatagramSibling(rDesc)

	// First try noise-wrapped route groups (fully initialized)
	nrg, ok := r.popNoiseRouteGroup(rDesc)
	if ok {
		if nrg.isClosed() {
			log.Debug("Noise route group already closed. Nothing to be done.")
			return
		}
		// Close in a goroutine with a timeout to prevent the GC from deadlocking
		// if the route group's close path blocks on a dead transport.
		done := make(chan struct{})
		go func() {
			defer close(done)
			if err := nrg.Close(); err != nil {
				log.WithError(err).Error("Failed to close noise route group.")
			}
		}()
		select {
		case <-done:
			log.Debug("Noise route group closed.")
		case <-time.After(10 * time.Second):
			log.Error("Timed out closing noise route group, abandoning.")
		}
		return
	}

	// Also check raw route groups (still being initialized, e.g., interrupted during handshake)
	rg, ok := r.popRawRouteGroup(rDesc)
	if ok {
		if rg.isClosed() {
			log.Debug("Raw route group already closed. Nothing to be done.")
			return
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			if err := rg.Close(); err != nil {
				log.WithError(err).Error("Failed to close raw route group.")
			}
		}()
		select {
		case <-done:
			log.Debug("Raw route group closed.")
		case <-time.After(10 * time.Second):
			log.Error("Timed out closing raw route group, abandoning.")
		}
		return
	}

	log.Debug("No route group associated with rule. Nothing to be done.")
}
