// router_rules.go contains rule and route group management logic.
package router

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/skycoin/dmsg/pkg/noise"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// Saves `rules` to the routing table.
func (r *router) SaveRoutingRules(rules ...routing.Rule) error {
	for _, rule := range rules {
		if err := r.rt.SaveRule(rule); err != nil {
			r.logger.WithError(err).Error("Error saving rule to routing table")
			return fmt.Errorf("routing table: %w", err)
		}

		r.logger.Debugf("Save new Routing Rule with ID %d %s", rule.KeyRouteID(), rule)
	}

	return nil
}

func (r *router) ReserveKeys(n int) ([]routing.RouteID, error) {
	ids, err := r.rt.ReserveKeys(n)
	if err != nil {
		r.logger.WithError(err).Error("Error reserving IDs")
	}

	return ids, err
}

func (r *router) popNoiseRouteGroup(desc routing.RouteDescriptor) (*NoiseRouteGroup, bool) {
	r.mx.Lock()
	defer r.mx.Unlock()

	nrg, ok := r.rgsNs[desc]
	if !ok {
		return nil, false
	}

	delete(r.rgsNs, desc)

	return nrg, true
}

func (r *router) noiseRouteGroup(desc routing.RouteDescriptor) (*NoiseRouteGroup, bool) {
	r.mx.Lock()
	defer r.mx.Unlock()

	nrg, ok := r.rgsNs[desc]

	return nrg, ok
}

func (r *router) initializingRouteGroup(desc routing.RouteDescriptor) (*RouteGroup, bool) {
	r.mx.Lock()
	defer r.mx.Unlock()

	rg, ok := r.rgsRaw[desc]

	return rg, ok
}

func (r *router) popRawRouteGroup(desc routing.RouteDescriptor) (*RouteGroup, bool) {
	r.mx.Lock()
	defer r.mx.Unlock()

	rg, ok := r.rgsRaw[desc]
	if !ok {
		return nil, false
	}

	delete(r.rgsRaw, desc)

	return rg, true
}

func (r *router) removeNoiseRouteGroup(desc routing.RouteDescriptor) {
	r.mx.Lock()
	defer r.mx.Unlock()

	delete(r.rgsNs, desc)
}

func (r *router) IntroduceRules(rules routing.EdgeRules) error {
	// Save rules immediately to avoid race with incoming transport packets
	if err := r.SaveRoutingRules(rules.Forward, rules.Reverse); err != nil {
		return fmt.Errorf("SaveRoutingRules: %w", err)
	}

	// Check if we already have an active mux-enabled route group for this descriptor.
	// If so, append the additional route instead of creating a new connection.
	r.mx.Lock()
	if nrg, ok := r.rgsNs[rules.Desc]; ok && nrg != nil && nrg.rg.mux != nil {
		r.mx.Unlock()

		nextTpID := rules.Forward.NextTransportID()
		tp := r.tm.Transport(nextTpID)
		if tp == nil {
			return fmt.Errorf("transport %s not found for additional mux route", nextTpID)
		}
		nrg.rg.appendRules(rules.Forward, rules.Reverse, tp)
		r.logger.Debugf("Appended additional mux route to existing RouteGroup for %s", &rules.Desc)
		return nil
	}
	r.mx.Unlock()

	// Handle ping/latency probe routes (port 46) directly without going through
	// the accept channel. These are ephemeral routes from other visors measuring
	// transport latency — they don't need to be delivered to any application.
	// Processing them in-line prevents them from blocking application routes
	// (proxy, VPN) in the accept queue, where each route blocks for up to the
	// handshake timeout (30s).
	if rules.Desc.DstPort() == routing.Port(skyenv.LatencyProbePort) || rules.Desc.SrcPort() == routing.Port(skyenv.LatencyProbePort) {
		go func() {
			// Short timeout for ping routes — they're ephemeral and if the
			// handshake doesn't complete quickly, the ping will fail anyway.
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			nsConf := noise.Config{
				LocalPK:   r.conf.PubKey,
				LocalSK:   r.conf.SecKey,
				RemotePK:  rules.Desc.SrcPK(),
				Initiator: false,
			}
			nrg, err := r.saveRouteGroupRules(ctx, rules, nsConf)
			if err != nil {
				r.rt.DelRules([]routing.RouteID{rules.Forward.KeyRouteID(), rules.Reverse.KeyRouteID()})
				return
			}
			nrg.rg.startOffServiceLoops()
		}()
		return nil
	}

	select {
	case <-r.done:
		return io.ErrClosedPipe
	default:
		r.mx.Lock()
		defer r.mx.Unlock()

		select {
		case r.accept <- rules:
			return nil
		case <-r.done:
			return io.ErrClosedPipe
		}
	}
}

// RoutesCount returns count of the routes stored within the routing table.
func (r *router) RoutesCount() int {
	return r.rt.Count()
}

// Rules gets all the rules stored within the routing table.
func (r *router) Rules() []routing.Rule {
	return r.rt.AllRules()
}

// Rule fetches rule by the route `id`.
func (r *router) Rule(id routing.RouteID) (routing.Rule, error) {
	return r.rt.Rule(id)
}

// SaveRule stores the `rule` within the routing table.
func (r *router) SaveRule(rule routing.Rule) error {
	return r.rt.SaveRule(rule)
}

// DelRules removes rules associated with `ids` from the routing table.
func (r *router) DelRules(ids []routing.RouteID) {
	rules := make([]routing.Rule, 0, len(ids))
	for _, id := range ids {
		rule, err := r.rt.Rule(id)
		if err != nil {
			r.logger.WithError(err).Errorf("Failed to get rule with ID %d on rule removal", id)
			continue
		}

		rules = append(rules, rule)
	}

	r.rt.DelRules(ids)

	for _, rule := range rules {
		r.removeRouteGroupOfRule(rule)
	}
}

// RemoveRouteDescriptor removes route group rule.
func (r *router) RemoveRouteDescriptor(desc routing.RouteDescriptor) {
	rules := r.rt.AllRules()
	for _, rule := range rules {
		if rule.Type() != routing.RuleReverse {
			continue
		}

		rd := rule.RouteDescriptor()
		if rd.DstPK() == desc.DstPK() && rd.DstPort() == desc.DstPort() && rd.SrcPort() == desc.SrcPort() {
			r.rt.DelRules([]routing.RouteID{rule.KeyRouteID()})
			return
		}
	}
}
