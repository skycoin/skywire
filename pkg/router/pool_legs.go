//go:build !tinygo || (js && wasm)

// Package router pkg/router/pool_legs.go c2-net-routing
//
// POOL-SOURCED MUX LEGS — grow an active tunnel on the routes its own STANDBY
// tunnels already proved.
//
// A multi-tunnel proxy (skysocks-client's standby pool, skyenv.
// SkysocksClientStandbyPool) holds up to 32 tunnels to one exit. Each one is a
// whole route group with a SINGLE leg (the app dials MuxRoutes=1), already
// ranked by the promoter and already carrying a live first-hop transport. When
// the ACTIVE tunnel wants a second packet-level mux leg, today it discovers one
// from scratch: a route-finder round trip plus the disjointness BFS, per leg,
// per group — while a dozen ready, measured, disjoint paths to that same exit
// are sitting in the pool.
//
// This file makes the pool the FIRST source of aux-leg plans. It does not — and
// cannot — hand a standby tunnel's RULES to the active group: a chain's consume
// rule at each end binds it to one route descriptor, so rules are never shared
// between groups (docs/design/shared-warm-route-pool.md). What is shared is the
// PLAN: the forward+reverse hop sequence, which is group-independent. Seeding
// those plans into the visor-level warmRoutePool means addOneAuxLeg — and the
// grow operation below — dial their own rule chain onto a known-good route with
// NO route-finder call at all.
//
// The governing rule from the same design doc's last section: a pool entry is a
// (transport, route_id) logical route, and legs MAY share a transport with a
// standby tunnel; what must never be double-used is the ACTIVE set's binding
// bottleneck. So a plan whose first hop the target group already holds is
// skipped here, before it can reach the cache — two legs on one first hop are
// one link's capacity wearing two route IDs.
//
// Ranking is by the standby tunnel's MEASURED end-to-end route latency (the
// number a routing policy judges a leg by — MuxLeg.RouteLatencyMS /
// legEndToEndLatencyMs), with the first hop's observed throughput prior
// (transport.Entry.ThroughputBps via ManagedTransport.GetThroughputBps, the
// same signal the dial ranker reads through throughputFor) as the secondary
// key. A pool tunnel has been pinged and used; that is strictly better
// information than the route-finder's topology-only estimate.
//
// Settlement: the app's own pool settlement marks (poolSettledAt / poolSettledN,
// pkg/skysocks/client.go) never cross into the visor — the router sees tunnels,
// not the app's pool state machine. So there is nothing to wait for: the seed
// is taken LAZILY on each grow, off whatever standby tunnels exist at that
// instant. An empty or unsettled pool seeds nothing and every path below falls
// through to today's route-finder dial, unchanged.
package router

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// poolPlanSeedCap bounds how many standby siblings' routes are offered to the
// warm-route pool in one seed (pool.plan_seed_cap). The pool is ranked
// best-first, a group grows by a handful of legs at most, and the bucket it
// feeds is itself capped (warmPlanBucketCap) — so pushing all 32 tunnels of a
// full standby pool through it every tick would only churn the bucket. 16
// leaves ample headroom above any mux width the adaptive engine asks for.
func poolPlanSeedCap() int { return routersettings.PoolPlanSeedCap.Int() }

// poolPlanAdmitted reports whether one standby sibling's route may become a
// pool leg plan at all — the CANDIDATE filter (pool.max_hops, pool.tp_types,
// pool.exclude_pks), applied before a plan is ranked or seeded. All three
// default to "admit everything", so an unset visor's selection is unchanged.
// tp is the first hop's transport (its declared type is what pool.tp_types
// filters on); fwd is the whole hop path (every hop's From/To is what
// pool.exclude_pks filters on).
func poolPlanAdmitted(log *logging.Logger, port routing.Port, fwd []routing.Hop, tp *transport.ManagedTransport) bool {
	if max := routersettings.PoolMaxHops.Int(); max > 0 && len(fwd) > max {
		if log != nil {
			log.Debugf("pool plan from standby group :%d skipped: %d hops over pool.max_hops=%d", port, len(fwd), max)
		}
		return false
	}
	if types := routersettings.PoolTpTypes.Strings(); len(types) > 0 && tp != nil {
		want := string(tp.Entry.Type)
		ok := false
		for _, t := range types {
			if t == want {
				ok = true
				break
			}
		}
		if !ok {
			if log != nil {
				log.Debugf("pool plan from standby group :%d skipped: first hop type %q not in pool.tp_types", port, want)
			}
			return false
		}
	}
	if excl := routersettings.PoolExcludePKs.Strings(); len(excl) > 0 {
		bad := make(map[string]struct{}, len(excl))
		for _, pk := range excl {
			bad[pk] = struct{}{}
		}
		for _, h := range fwd {
			if _, hit := bad[h.From.Hex()]; hit {
				if log != nil {
					log.Debugf("pool plan from standby group :%d skipped: hop touches an excluded pk", port)
				}
				return false
			}
			if _, hit := bad[h.To.Hex()]; hit {
				if log != nil {
					log.Debugf("pool plan from standby group :%d skipped: hop touches an excluded pk", port)
				}
				return false
			}
		}
	}
	return true
}

// tunnelRoleStandby is the label a multi-tunnel app puts on a tunnel it is
// holding ready rather than sending on (DialOptions.TunnelRole, re-stamped live
// by NoteTunnelEvent). Only these are borrowed: an ACTIVE sibling's route is
// carrying its own streams, and a tunnel with no role at all is not part of a
// pool.
const tunnelRoleStandby = "standby"

// poolLegPlan is one standby tunnel's route, offered as an aux-leg plan for an
// active tunnel of the same app to the same exit.
type poolLegPlan struct {
	fwd, rev []routing.Hop
	firstTp  uuid.UUID
	// port is the standby group's own port — desc.DstPort(), which is what
	// `proxy mux info` prints and what `--tunnel` names.
	port routing.Port
	// latencyMS is the tunnel's measured end-to-end route latency (the leg
	// liveness EWMA, else the first-hop transport RTT). 0 = never measured.
	latencyMS float64
	// throughputBps is the first hop's observed capacity prior.
	throughputBps float64
	// dupOf names the sibling tunnel that already holds this exact hop path,
	// set only when pool.allow_duplicate_route let the plan through. 0 means
	// the plan's route is distinct, which is the default and the only case the
	// ranking offers at all.
	dupOf routing.Port
}

// source is the plan's provenance, in the words the dial-decision event carries.
func (p poolLegPlan) source() string {
	s := fmt.Sprintf("pool plan from standby group :%d", p.port)
	if p.dupOf != 0 {
		s += fmt.Sprintf(" (duplicate of the route :%d already holds; allowed by pool.allow_duplicate_route)", p.dupOf)
	}
	return s
}

// hopPathSig identifies a WHOLE route by its transport-ID sequence, which is
// what "the same route" means for the distinct-route rule: two plans with the
// same signature traverse the same physical links in the same order, so a
// second route ID over them buys no capacity and costs the exit a second chain.
func hopPathSig(hops []routing.Hop) string {
	if len(hops) == 0 {
		return ""
	}
	ids := make([]string, 0, len(hops))
	for _, h := range hops {
		ids = append(ids, h.TpID.String())
	}
	return strings.Join(ids, ">")
}

// rankPoolPlans orders plans best-first: measured end-to-end route latency
// ascending, then the first hop's throughput prior descending.
//
// A plan with NO latency sample sorts after every plan that has one — 0 here
// means "never measured", not "instant", and the whole point of preferring the
// pool is that its members have been measured. Between two unmeasured plans (or
// two equally fast ones) the capacity prior decides, which is the tiebreak that
// keeps a fast-but-thin first hop from outranking the link that can actually
// carry the stripe.
func rankPoolPlans(plans []poolLegPlan) {
	sort.SliceStable(plans, func(i, j int) bool { return lessPoolPlan(plans[i], plans[j]) })
}

// lessPoolPlan is that ordering as a two-plan comparison, so the pool ARBITER
// (pool_arbiter.go) ranks whole standby tunnels by exactly the same keys this
// file ranks their plans by.
func lessPoolPlan(a, b poolLegPlan) bool {
	la, lb := a.latencyMS, b.latencyMS
	switch {
	case la > 0 && lb <= 0:
		return true
	case la <= 0 && lb > 0:
		return false
	case la != lb:
		return la < lb
	}
	return a.throughputBps > b.throughputBps
}

// throughputPrior is the capacity signal for a first-hop transport, in the
// same order of preference buildHopLookups uses to build the dial ranker's
// throughputFor: this visor's own passively-observed peak
// (ManagedTransport.GetThroughputBps) when it has one, else the value the
// transport discovery entry carries (transport.Entry.ThroughputBps, which an
// active probe elsewhere may have filled in). 0 means no signal at all.
func throughputPrior(tp *transport.ManagedTransport) float64 {
	if tp == nil {
		return 0
	}
	if bps := tp.GetThroughputBps(); bps > 0 {
		return bps
	}
	return tp.Entry.ThroughputBps
}

// standbyPoolPlans enumerates the STANDBY sibling tunnels of desc's app that
// terminate at the same exit, and returns each one's route as a candidate
// aux-leg plan for desc's group, ranked best-first.
//
// rgsNs is keyed by the RECEIVE-side descriptor (Src = the exit, Dst = this
// visor), so siblings to the same exit service match on SrcPK()+SrcPort() — the
// same match siblingRouteGroupExclusions uses at dial time — while the differing
// DstPort() is each tunnel's own port. The app name must match too: another
// app's tunnels to the same exit are not this app's pool.
//
// Skipped, in order: the target group itself; a sibling that is not labeled
// standby; one whose route was never recorded (an accepted or legacy group);
// one shorter than the caller's min-hops floor (a 1-hop direct plan must never
// land in a min_hops>=2 bucket); and — the binding-bottleneck rule — one whose
// first hop the target group ALREADY holds.
//
// Locking: the matching groups are snapshotted under r.mx and then read through
// their own rg locks, so r.mx is never held while a route-group lock is taken.
func (r *router) standbyPoolPlans(desc routing.RouteDescriptor, minHops uint16) []poolLegPlan {
	log := r.scopedLog(desc.SrcPort())
	r.mx.Lock()
	target := r.rgsNs[desc]
	var siblings []*NoiseRouteGroup
	for d, nrg := range r.rgsNs {
		if nrg == nil || nrg.rg == nil || d == desc {
			continue
		}
		if d.SrcPK() == desc.SrcPK() && d.SrcPort() == desc.SrcPort() {
			siblings = append(siblings, nrg)
		}
	}
	r.mx.Unlock()
	if target == nil || target.rg == nil || len(siblings) == 0 {
		return nil
	}

	app := target.rg.AppName()
	held := make(map[uuid.UUID]struct{})
	target.rg.mu.Lock()
	for _, tp := range target.rg.tps {
		if tp != nil {
			held[tp.Entry.ID] = struct{}{}
		}
	}
	target.rg.mu.Unlock()

	plans := make([]poolLegPlan, 0, len(siblings))
	for _, nrg := range siblings {
		rg := nrg.rg
		if rg.AppName() != app || rg.TunnelRole() != tunnelRoleStandby {
			continue
		}
		// A standby tunnel is a single-leg group: tps[0] IS its route.
		rg.mu.Lock()
		var tp *transport.ManagedTransport
		if len(rg.tps) > 0 {
			tp = rg.tps[0]
		}
		rg.mu.Unlock()
		if tp == nil {
			continue
		}
		fwd := rg.legHopsFor(tp.Entry.ID)
		if len(fwd) == 0 {
			continue
		}
		if minHops > 0 && len(fwd) < int(minHops) {
			continue
		}
		if _, dup := held[fwd[0].TpID]; dup {
			continue
		}
		if !poolPlanAdmitted(log, rg.desc.DstPort(), fwd, tp) {
			continue
		}
		plans = append(plans, poolLegPlan{
			fwd:           fwd,
			rev:           reverseHops(fwd),
			firstTp:       fwd[0].TpID,
			port:          rg.desc.DstPort(),
			latencyMS:     rg.legBandLatencyMs(tp),
			throughputBps: throughputPrior(tp),
		})
	}
	rankPoolPlans(plans)
	return distinctRoutePlans(plans, target.rg.knBool(routersettings.PoolAllowDuplicateRoute))
}

// distinctRoutePlans is the DISTINCT-ROUTE rule, applied after the ranking so
// the tunnel kept for a given route is the best-ranked one holding it.
//
// A hop path a sibling tunnel already offers is not a second path, it is the
// same one wearing another route ID: the rig run of 2026-09-22 ended with
// pooled tunnels :49205 and :49235 both riding transport a94550f5 and the exit
// carried both, for no aggregation at all. The repeat is dropped — unless
// pool.allow_duplicate_route is on, in which case it is offered with the
// tunnel it duplicates named, so the dial-decision event says so out loud.
//
// A plan whose route the TARGET group holds never gets this far: it shares the
// target's first hop, which standbyPoolPlans has already skipped.
func distinctRoutePlans(plans []poolLegPlan, allowDup bool) []poolLegPlan {
	seen := make(map[string]routing.Port, len(plans))
	out := plans[:0]
	for _, p := range plans {
		sig := hopPathSig(p.fwd)
		keeper, dup := seen[sig]
		if !dup {
			seen[sig] = p.port
			out = append(out, p)
			continue
		}
		if !allowDup {
			continue
		}
		p.dupOf = keeper
		out = append(out, p)
	}
	return out
}

// seedPoolPlans puts the ranked standby-sibling plans for desc's group into the
// visor's warm-route pool, so the very next aux-leg dial for that group (or any
// other group to the same exit at the same min-hops class) is served a known
// route instead of paying for a route-finder round trip. Returns how many
// plans were seeded; 0 means the caller's path is byte-for-byte today's.
//
// Idempotent: the pool dedupes by first-hop transport and refreshes a repeat in
// place, so seeding on every grow costs a map write per plan and never grows
// the bucket past what the topology offers.
func (r *router) seedPoolPlans(desc routing.RouteDescriptor, minHops uint16) int {
	plans := r.standbyPoolPlans(desc, minHops)
	if seedCap := poolPlanSeedCap(); len(plans) > seedCap {
		plans = plans[:seedCap]
	}
	for i := range plans {
		r.warmRoutes.putSourced(desc.SrcPK(), minHops, plans[i].fwd, plans[i].rev, plans[i].source())
	}
	return len(plans)
}

// GrowMuxFromPool implements Router. It grows the tunnel route group this visor
// dialed from localPort by up to legs additional mux legs, taking the plans
// from the app's STANDBY tunnels to the same exit and falling back to
// GrowMuxRoute's route-finder path for whatever the pool could not cover.
// minHops floors the hop count of a planned leg (0 = the visor's configured
// floor). Returns the number of legs actually added.
//
// This is the one entry point for "grow group G by k legs from the pool": the
// CLI (`proxy mux grow`) reaches it through the visor RPC and the app reaches it
// through the ingress gateway, so an in-place re-home of a standby chain can
// use it as its no-capability fallback without duplicating any of this.
func (r *router) GrowMuxFromPool(localPort routing.Port, legs, minHops int) (int, error) {
	if legs <= 0 {
		legs = 1
	}
	desc, nrg, ok := r.tunnelGroupByLocalPort(localPort)
	if !ok {
		return 0, fmt.Errorf("no active route group dialed from port %d", localPort)
	}
	if nrg.rg.mux == nil {
		return 0, errors.New("route group does not have mux enabled")
	}

	keyMinHops := r.conf.MinHops
	if minHops > 0 {
		keyMinHops = uint16(minHops) //nolint:gosec // G115: a hop floor is a small positive int
	}
	log := r.scopedLog(desc.SrcPort())
	seeded := r.seedPoolPlans(desc, keyMinHops)

	lPK := desc.DstPK() // this visor (the tunnel's entry point)
	rPK := desc.SrcPK() // the exit

	added := 0
	for added < legs {
		excludeIDs, excludeRemoteIDs, excludePKs := r.groupExcludes(nrg, lPK, rPK)
		fwd, rev, source, hit := r.warmRoutes.bestPlanSourced(rPK, keyMinHops, excludeIDs, excludeRemoteIDs, excludePKs)
		if !hit {
			break
		}
		if err := r.AddMuxRouteByHops(desc, fwd, rev); err != nil {
			// The plan was stale (a transport died, the exit refused it). Drop
			// the whole bucket rather than re-serving it on the next turn of
			// this loop, and let the route-finder fallback below finish the job.
			log.Debugf("GrowMuxFromPool: %s failed to attach: %v", planSource(source), err)
			r.warmRoutes.invalidate(rPK)
			break
		}
		added++
		nrg.rg.noteMuxEvent(MuxEvent{
			Event: MuxEventDialDecision, By: MuxByLocal, LegIndex: -1, Legs: nrg.rg.legCount(),
			TpID:   fwd[0].TpID,
			Reason: planSource(source) + "; grown without a route-finder call",
		})
	}

	if added < legs {
		// Whatever the pool could not cover is grown exactly as before: the
		// route-finder, the local-calc fallback, and the same disjointness
		// gates. A visor with no standby pool at all takes only this path.
		current := nrg.rg.legCount()
		n, err := r.GrowMuxRoute(desc, current+(legs-added), minHops)
		added += n
		if err != nil && added == 0 {
			return 0, err
		}
	}

	log.Infof("GrowMuxFromPool: added %d of %d requested leg(s) to the tunnel on port %d (%d standby plan(s) seeded)",
		added, legs, localPort, seeded)
	return added, nil
}

// planSource renders a warm-pool plan's provenance for a log line or an event
// reason, naming the route-finder when the plan carries no label.
func planSource(source string) string {
	if source == "" {
		return "warm-pool plan (route-finder discovered)"
	}
	return source
}

// tunnelGroupByLocalPort finds the established route group this visor dialed
// from localPort. rgsNs is keyed by the receive-side descriptor, so the dialing
// side's own port is DstPort(); SrcPort() is matched only as the fallback the
// accept side would need (and is the shared app port for a multi-tunnel client,
// so it can never name one tunnel on its own — dst_port is tried first, exactly
// as selectRouteDesc does).
func (r *router) tunnelGroupByLocalPort(localPort routing.Port) (routing.RouteDescriptor, *NoiseRouteGroup, bool) {
	r.mx.Lock()
	defer r.mx.Unlock()
	for desc, nrg := range r.rgsNs {
		if nrg == nil || nrg.rg == nil {
			continue
		}
		if desc.DstPort() == localPort {
			return desc, nrg, true
		}
	}
	for desc, nrg := range r.rgsNs {
		if nrg == nil || nrg.rg == nil {
			continue
		}
		if desc.SrcPort() == localPort {
			return desc, nrg, true
		}
	}
	return routing.RouteDescriptor{}, nil, false
}

// groupExcludes is the per-group exclude set an aux-leg plan must avoid: the
// first-hop transports the group's live legs occupy, the far-end transports its
// peer holds for those legs, and the intermediate visors already in use (plus
// the same-LAN peers that are never valid mux intermediates). The same three
// sets addOneAuxLeg builds, gathered here so the pool path and the dial path
// judge a plan identically.
func (r *router) groupExcludes(nrg *NoiseRouteGroup, lPK, rPK cipher.PubKey) (excludeIDs, excludeRemoteIDs []uuid.UUID, excludePKs []cipher.PubKey) {
	excludePKs = intermediatesOfRouteGroup(nrg, lPK, rPK)
	excludePKs = appendUniquePKs(excludePKs, r.sameLANExcludedPKs())
	nrg.rg.mu.Lock()
	excludeIDs = make([]uuid.UUID, 0, len(nrg.rg.tps))
	for _, tp := range nrg.rg.tps {
		if tp != nil {
			excludeIDs = append(excludeIDs, tp.Entry.ID)
		}
	}
	nrg.rg.mu.Unlock()
	excludeRemoteIDs = nrg.rg.remoteLegTransportIDs()
	return excludeIDs, excludeRemoteIDs, excludePKs
}
