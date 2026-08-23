// Package router pkg/router/router_rules.go c2-net-routing
// router_rules.go contains rule and route group management logic.
package router

import (
	"fmt"
	"io"
	"time"

	"github.com/skycoin/skywire/pkg/routing"
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

// RouteGroupHops returns the stored forward hops for the route group keyed
// by desc. The lookup tries the given descriptor first, then its inversion,
// since dialer and acceptor sides key the map by mirrored descriptors.
func (r *router) RouteGroupHops(desc routing.RouteDescriptor) []RouteHopInfo {
	if nrg, ok := r.noiseRouteGroup(desc); ok && nrg != nil {
		return nrg.RouteHopDetails()
	}
	inv := desc.Invert()
	if nrg, ok := r.noiseRouteGroup(inv); ok && nrg != nil {
		return nrg.RouteHopDetails()
	}
	return nil
}

// RouteGroupMuxInfo implements Router. Tries desc and its inversion
// in case the rg is keyed by the mirrored descriptor.
func (r *router) RouteGroupMuxInfo(desc routing.RouteDescriptor) (MuxInfo, bool) {
	if nrg, ok := r.noiseRouteGroup(desc); ok && nrg != nil && nrg.rg != nil {
		return nrg.rg.MuxStats(), true
	}
	inv := desc.Invert()
	if nrg, ok := r.noiseRouteGroup(inv); ok && nrg != nil && nrg.rg != nil {
		return nrg.rg.MuxStats(), true
	}
	return MuxInfo{}, false
}

// RouteGroupMuxInfoForApp implements Router. Walks every active rg
// and returns mux snapshots for the ones tagged with appName at
// dial time (saveRouteGroupRules calls SetAppName from opts.AppName).
// Empty slice when no rg's are tagged for the app — typically because
// nothing is currently dialed via that app, or the rg's are still
// initializing (the rgsRaw map; we only walk rgsNs since those are
// the established sessions).
func (r *router) RouteGroupMuxInfoForApp(appName string) []MuxInfo {
	if appName == "" {
		return nil
	}
	r.mx.Lock()
	rgs := make([]*NoiseRouteGroup, 0, len(r.rgsNs))
	for _, nrg := range r.rgsNs {
		if nrg != nil && nrg.rg != nil {
			rgs = append(rgs, nrg)
		}
	}
	r.mx.Unlock()

	out := make([]MuxInfo, 0, len(rgs))
	for _, nrg := range rgs {
		if nrg.rg.AppName() != appName {
			continue
		}
		out = append(out, nrg.rg.MuxStats())
	}
	return out
}

// RouteGroupMuxInfoAll implements Router. Returns a mux snapshot for
// every established (noise) route group, with no app-name filter — the
// whole-runtime view `cli visor state` surfaces. Same MuxStats() the
// per-app query and 'mux plot' read; only the filter differs.
func (r *router) RouteGroupMuxInfoAll() []MuxInfo {
	r.mx.Lock()
	rgs := make([]*NoiseRouteGroup, 0, len(r.rgsNs))
	for _, nrg := range r.rgsNs {
		if nrg != nil && nrg.rg != nil {
			rgs = append(rgs, nrg)
		}
	}
	r.mx.Unlock()

	out := make([]MuxInfo, 0, len(rgs))
	for _, nrg := range rgs {
		out = append(out, nrg.rg.MuxStats())
	}
	return out
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

// datagramRouteGroup returns the faithful-UDP route group registered for desc,
// if any. Mirrors noiseRouteGroup; the datagram groups live in their own map so
// a DatagramPacket is never confused with a reliable RouteGroup keyed by the
// same descriptor. #2607 stage-4 dispatch.
func (r *router) datagramRouteGroup(desc routing.RouteDescriptor) (*DatagramRouteGroup, bool) {
	r.mx.Lock()
	defer r.mx.Unlock()

	dg, ok := r.rgsDatagrams[desc]

	return dg, ok
}

// setDatagramRouteGroup registers (or replaces) the datagram route group for
// desc. A pre-existing group for the same descriptor is closed first so a
// re-dial doesn't leak the old pump. Called by the route-setup integration once
// it constructs a DatagramRouteGroup.
func (r *router) setDatagramRouteGroup(desc routing.RouteDescriptor, dg *DatagramRouteGroup) {
	r.mx.Lock()
	old, ok := r.rgsDatagrams[desc]
	r.rgsDatagrams[desc] = dg
	r.mx.Unlock()

	if ok && old != nil && old != dg {
		if err := old.Close(); err != nil {
			r.logger.WithError(err).Debugf("Failed to close replaced datagram route group %s", desc.String())
		}
	}
}

// removeDatagramRouteGroup de-registers the datagram route group for desc. The
// caller owns closing the group; this only drops the dispatch mapping.
func (r *router) removeDatagramRouteGroup(desc routing.RouteDescriptor) {
	r.mx.Lock()
	defer r.mx.Unlock()

	delete(r.rgsDatagrams, desc)
}

func (r *router) IntroduceRules(rules routing.EdgeRules) error {
	// Save rules immediately to avoid race with incoming transport packets
	if err := r.SaveRoutingRules(rules.Forward, rules.Reverse); err != nil {
		return fmt.Errorf("SaveRoutingRules: %w", err)
	}

	// Check if we already have a route group for this descriptor. If so, these
	// rules are an ADDITIONAL (aux) mux leg for it, not a new connection — never
	// push them to r.accept (that path builds a duplicate group; on the responder
	// it deletes the leg's rules or blocks the whole handshake-await, collapsing
	// the mux back toward one leg, #80).
	//
	// Two cases:
	//   - The group is already live (rgsNs) with mux enabled → append now.
	//   - The group is still initializing (rgsRaw): its rg.mux is not set yet (the
	//     responder creates it lazily when the primary forward handshake lands),
	//     so we cannot append yet. Buffer the leg and drain it through the same
	//     guarded append the moment the group registers (saveRouteGroupRules).
	r.mx.Lock()
	if nrg, ok := r.rgsNs[rules.Desc]; ok && nrg != nil && nrg.rg.mux != nil {
		r.mx.Unlock()
		// Route through appendRouteToGroup so the DMSG-refusal and
		// duplicate-transport-ID guards apply (the old inline append skipped
		// them), and the peer is handshaked on the new leg.
		return r.appendRouteToGroup(nrg, rules)
	}
	if _, initializing := r.rgsRaw[rules.Desc]; initializing {
		// Park under r.mx so the group cannot transition rgsRaw -> rgsNs (and
		// drain) between our check and our park, which would strand this leg.
		parked := r.pendingLegs.park(rules.Desc, rules, time.Now())
		r.mx.Unlock()
		if !parked {
			return fmt.Errorf("pending-leg buffer full for initializing route group %s", &rules.Desc)
		}
		r.logger.Debugf("Buffered aux mux leg for initializing route group %s", &rules.Desc)
		return nil
	}
	r.mx.Unlock()

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
