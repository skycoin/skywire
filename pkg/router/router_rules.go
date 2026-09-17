// Package router pkg/router/router_rules.go c2-net-routing
// router_rules.go contains rule and route group management logic.
package router

import (
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/routing"
)

// sortMuxInfos orders a mux-info slice deterministically by route descriptor
// (dst PK, src PK, dst port, src port). The router holds route groups in a map,
// so RouteGroupMuxInfo* would otherwise return them in random iteration order —
// which reshuffled the `cli visor state` / status.skysocks stream tree and the
// CLI mux-info rows on every refresh. A stable key keeps each tunnel in the same
// slot as the pool churns around it.
func sortMuxInfos(out []MuxInfo) {
	sort.SliceStable(out, func(i, j int) bool {
		di, dj := &out[i].Desc, &out[j].Desc
		if a, b := di.DstPK().String(), dj.DstPK().String(); a != b {
			return a < b
		}
		if a, b := di.SrcPK().String(), dj.SrcPK().String(); a != b {
			return a < b
		}
		if a, b := di.DstPort(), dj.DstPort(); a != b {
			return a < b
		}
		return di.SrcPort() < dj.SrcPort()
	})
}

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
	sortMuxInfos(out)
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
	sortMuxInfos(out)
	return out
}

// NoteTunnelEvent implements Router. Finds the established route group this
// visor dialed from localPort and records the app's tunnel event on it,
// stamped with the group's first-hop transport and that leg's forward path.
//
// The local port is the descriptor's SOURCE port on the dialing side. A group
// keyed by the mirrored descriptor (the accept side) has it as the DESTINATION
// port instead, so both are matched — an exit never calls this, but a route
// group is keyed by whichever descriptor its end created and the lookup should
// not depend on that.
//
// role, when non-empty, also re-stamps the group's TunnelRole, so
// `visor state --select mux_route_groups` shows what a tunnel is NOW rather
// than what it was labeled at dial time. A promote that changed the answer
// and did not say so would be worse than no field at all.
func (r *router) NoteTunnelEvent(localPort routing.Port, event, reason, role string) bool {
	if localPort == 0 || event == "" {
		return false
	}
	r.mx.Lock()
	var rg *RouteGroup
	for desc, nrg := range r.rgsNs {
		if nrg == nil || nrg.rg == nil {
			continue
		}
		if desc.SrcPort() == localPort || desc.DstPort() == localPort {
			rg = nrg.rg
			break
		}
	}
	r.mx.Unlock()
	if rg == nil {
		return false
	}
	rg.SetTunnelRole(role)
	rg.noteTunnelEvent(event, reason)
	return true
}

// appRouteGroupCloseTimeout bounds the wait for an app's route groups to
// finish their close handshake. The de-registration itself is immediate (the
// map entries are dropped before any close runs), so this only bounds how long
// the caller waits for the close packets the peer needs to reap its mirror
// group; a leg over a dead transport must not hold the RPC open.
const appRouteGroupCloseTimeout = 5 * time.Second

// CloseRouteGroupsForApp implements Router. Closes and de-registers every
// route group tagged with appName — the app's own tunnels AND the sibling
// groups a multi-tunnel dial (--tunnels N, dial_decision(diversify …)) put
// alongside them, since every one of them carries the same app tag.
//
// Nothing else does this. A route group is reaped either when the app's
// net.Conn is closed (Proc teardown -> rpcGW.cm.CloseAll, pkg/app/appserver/
// proc.go:392) or when the rules GC collects an expired consume rule
// (router_gc.go:105). Neither fires for a group the app never took delivery
// of — a dial that completed after the proc was torn down, or a leg-removal
// survivor — so the group keeps its keep-alive loop running, keeps refreshing
// its own rules (so the GC never collects them), and keeps appearing in
// `proxy mux info` and the `--route reconcile` count for an app that is
// stopped. Closing by app tag reaps those without waiting on rule expiry.
//
// Entries are removed from the registry under the lock BEFORE any close runs,
// so a query right after this returns is already empty. Each close then runs
// concurrently (close waits on the peer's close packets, which is what reaps
// the mirror group on the far end) under one bounded wait.
func (r *router) CloseRouteGroupsForApp(appName string) int {
	if appName == "" {
		return 0
	}

	type victim struct {
		desc routing.RouteDescriptor
		nrg  *NoiseRouteGroup
		rg   *RouteGroup
	}

	r.mx.Lock()
	victims := make([]victim, 0, len(r.rgsNs))
	for desc, nrg := range r.rgsNs {
		if nrg == nil || nrg.rg == nil || nrg.rg.AppName() != appName {
			continue
		}
		delete(r.rgsNs, desc)
		victims = append(victims, victim{desc: desc, nrg: nrg, rg: nrg.rg})
	}
	// Groups still finishing their noise handshake are the app's too; a dial
	// that is mid-flight when the app stops would otherwise land in rgsNs
	// moments later with nobody left to close it.
	for desc, rg := range r.rgsRaw {
		if rg == nil || rg.AppName() != appName {
			continue
		}
		delete(r.rgsRaw, desc)
		victims = append(victims, victim{desc: desc, rg: rg})
	}
	r.mx.Unlock()

	if len(victims) == 0 {
		return 0
	}

	done := make(chan struct{})
	var wg sync.WaitGroup
	for _, v := range victims {
		// The reason rides the group_closed mux event RouteGroup.close emits.
		v.rg.setCloseReason("app stopped: " + appName)
		// The faithful-UDP sibling's lifetime is coupled to the reliable
		// route (#2607), same as the rules-GC reap path.
		r.closeDatagramSibling(v.desc)
		wg.Add(1)
		go func() {
			defer wg.Done()
			var err error
			if v.nrg != nil {
				err = v.nrg.Close()
			} else {
				err = v.rg.Close()
			}
			if err != nil {
				r.logger.WithError(err).
					WithField("app_name", appName).
					WithField("rt_desc", v.desc.String()).
					Debug("Failed to close the stopped app's route group.")
			}
		}()
	}
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(appRouteGroupCloseTimeout):
		r.logger.WithField("app_name", appName).
			WithField("groups", len(victims)).
			Warn("Timed out waiting for the stopped app's route groups to close; they are de-registered regardless.")
	}

	r.logger.WithField("app_name", appName).
		WithField("groups", len(victims)).
		Debug("Closed the stopped app's route groups.")

	return len(victims)
}

// SetMuxDirectionForApp implements Router. Applies the operator's manual
// direction pin to every active rg tagged with appName (same tag walk as
// RouteGroupMuxInfoForApp). Each pinned rg coordinates the pin with its peer
// over the wire (RouteGroup.SetDirectionPin); non-directional rg's are counted
// as errors so a proxy session without CapUniDir reports why nothing happened.
func (r *router) SetMuxDirectionForApp(appName string, mode byte) (int, error) {
	if appName == "" {
		return 0, fmt.Errorf("app name required")
	}
	r.mx.Lock()
	rgs := make([]*NoiseRouteGroup, 0, len(r.rgsNs))
	for _, nrg := range r.rgsNs {
		if nrg != nil && nrg.rg != nil && nrg.rg.AppName() == appName {
			rgs = append(rgs, nrg)
		}
	}
	r.mx.Unlock()
	if len(rgs) == 0 {
		return 0, fmt.Errorf("no active route group for app %q", appName)
	}

	applied := 0
	var lastErr error
	for _, nrg := range rgs {
		if err := nrg.rg.SetDirectionPin(mode); err != nil {
			lastErr = err
			continue
		}
		applied++
	}
	if applied == 0 {
		return 0, fmt.Errorf("direction pin applied to none of app %q's %d route group(s): %w", appName, len(rgs), lastErr)
	}
	return applied, nil
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

	// Hand the new group off to AcceptRoutes. This send MUST NOT hold r.mx: the
	// accept buffer can fill (a burst of route setups, or the app-accept loop
	// mid-handshake), and the only consumer — AcceptRoutes → saveRouteGroupRules
	// — takes r.mx to drain each item. Holding r.mx across a blocking send
	// therefore deadlocks the whole router: the sender waits for the consumer to
	// free a slot while the consumer waits for r.mx, and every other
	// IntroduceRules, ActiveRouteStatuses and the rules-GC pile up behind the
	// held lock (observed live: one sender parked in chansend, 1080 waiters, the
	// visor wedged at ~10k goroutines with route setup failing fleet-wide).
	// Duplicate sends for the same descriptor are already tolerated downstream —
	// saveRouteGroupRules detects an initializing group and adopts the rules as
	// an aux mux leg — so the lock guarded nothing here.
	select {
	case r.accept <- rules:
		return nil
	case <-r.done:
		return io.ErrClosedPipe
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
