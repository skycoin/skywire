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
