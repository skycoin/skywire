//go:build !tinygo || (js && wasm)

// Package router pkg/router/router_dial_select.go c2-net-routing
package router

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/rfclient"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

func (r *router) fetchBestRoutes(ctx context.Context, log *logging.Logger, src, dst cipher.PubKey, opts *DialOptions, baseMinHops uint16) (fwd, rev []routing.Hop, err error) {
	if log == nil {
		log = r.logger
	}
	if opts == nil {
		opts = DefaultDialOptions() // nolint
	}

	// Check if force local routes is enabled
	forceLocal := r.forceLocalRoutes.Load()

	if forceLocal {
		log.Info("Calculating route locally (--local-route enabled)")
		opts.note("force-local")
		calcStart := time.Now()
		localFwd, localRev, localErr := r.calculateLocalRoutes(ctx, log, src, dst, opts)
		calcTime := time.Since(calcStart)
		r.lastRouteCalcTime.Store(int64(calcTime))
		if localErr == nil {
			log.Infof("Local route calculated in %v: Forward=%v, Reverse=%v", calcTime, localFwd, localRev)
		}
		return localFwd, localRev, localErr
	}

	// RSN-oracle 2-hop fast path (opt-in, default OFF). For a single-
	// intermediate route the source computes the route locally from its own
	// transports intersected with the destination's own transports (fetched
	// authoritatively from the destination via an RSN-signed query) — no TPD /
	// route-finder round-trip. Only attempted when the path is enabled AND an
	// oracle is wired AND the min-hops constraint is 2-hop-satisfiable (a 2-hop
	// route is exactly one intermediate; a min_hops>=3 request cannot be served
	// this way and falls through). On any miss (no oracle, no shared
	// intermediate, delivery error) it falls through to the existing behavior —
	// zero change when disabled.
	// A direct dial (--direct: EnsureDirectTransport, or UseExistingTpOnly) means
	// "use the 1-hop direct transport, don't route around it". The RSN-oracle is
	// a SEPARATE 2-hop path from the route-finder that --direct already bypasses,
	// so without this guard an enabled oracle still overrode a --direct control
	// forward with a 2-hop route (observed: a same-LAN :4443 forward routed
	// US->AU->US at 2.3s instead of over its 3ms direct transport). Skip the
	// oracle for a direct dial.
	directDial := opts != nil && (opts.EnsureDirectTransport || opts.UseExistingTpOnly)
	if r.conf != nil && (r.conf.EnableRSNOracleRoutes || opts.UseRSNOracle2Hop) && src != dst && !directDial {
		hi := baseMinHops
		if e := opts.EffectiveMinHops(true); uint16(e) > hi { //nolint:gosec
			hi = uint16(e) //nolint:gosec
		}
		if e := opts.EffectiveMinHops(false); uint16(e) > hi { //nolint:gosec
			hi = uint16(e) //nolint:gosec
		}
		if hi <= 2 {
			if oFwd, oRev, oErr := r.oracle2HopRoutes(ctx, log, src, dst, opts); oErr == nil {
				// The oracle knows nothing of a diversify dial's first-hop
				// exclusions, and it returns before the finder filter below: it
				// handed every extra tunnel the same direct first hop its sibling
				// held (the dial_decision trail showed the exclusion seeded and
				// nothing after it). Keep its answer only when it leaves over a
				// free transport; otherwise fall through to the filtered paths.
				if opts.DiversifyTransports && r.firstHopExcluded(oFwd, opts) {
					opts.note("oracle: path leaves over an excluded first hop; falling through")
				} else {
					if opts.DiversifyTransports {
						opts.note("oracle: path over a free first hop")
					}
					return oFwd, oRev, nil
				}
			} else if !errors.Is(oErr, errRSNOracleInert) {
				log.WithError(oErr).Debug("RSN-oracle 2-hop path missed; falling through to route finder")
			}
		}
	}

	// A --direct dial over an EXISTING direct transport does not need the route
	// finder: the route it would return is the transport this visor is already
	// holding. Three comments (here at the baseMinHops downgrade, on
	// EnsureDirectTransport in router.go, and at the app-server flag site) have
	// long claimed UseExistingTpOnly "bypasses the route-finder", but nothing
	// implemented it — useExistingOnly only skipped the transport-CREATION hooks,
	// so every --direct dial still paid an RF round-trip to be told about its own
	// transport (#4552).
	//
	// Guarded on baseMinHops == 1, which the caller sets only when a transport to
	// dst is already known AND nothing (per-dial or visor-global min_hops) asked
	// for more than one hop — so an operator running min_hops=3 is never silently
	// handed a 1-hop route here.
	//
	// If no live transport is found we fall through to the route finder rather
	// than failing: --direct means "prefer the direct leg", and EnsureDirectTransport
	// may still be creating one.
	if directDial && baseMinHops == 1 {
		if hop, ok := r.directHop(src, dst); ok {
			fwd := []routing.Hop{hop}
			log.WithField("transport", hop.TpID).
				Debug("--direct: 1-hop route over the existing transport; skipping the route finder")
			opts.note("direct-hop %s", hop.TpID.String()[:8])
			return fwd, reverseHops(fwd), nil
		}
	}

	retries := opts.Retries

	log.Debugf("Requesting new routes from %s to %s", src, dst)

	timer := time.NewTimer(retryDuration)
	defer timer.Stop()

	forward := [2]cipher.PubKey{src, dst}
	backward := [2]cipher.PubKey{dst, src}

fetchRoutesAgain:
	// Check context before making network calls
	if err := ctx.Err(); err != nil {
		return nil, nil, fmt.Errorf("context canceled before route fetch: %w", err)
	}

	// Per-call MinHops override takes precedence over visor-global
	// Config.MinHops. Set by callers like mux-bw that want to test
	// a multi-hop path without bumping the visor's static config.
	//
	// Per-direction overrides (ForwardMinHops / ReverseMinHops) win
	// over the symmetric MinHops when set. When the two directions
	// differ we split into two route-finder queries — the rfclient
	// applies one MinHops to all PathEdges in a single call, so
	// asymmetric constraints can't be expressed in one round-trip.
	fwdEff := opts.EffectiveMinHops(true)
	revEff := opts.EffectiveMinHops(false)
	fwdMinHops := baseMinHops
	revMinHops := baseMinHops
	if fwdEff > 0 {
		fwdMinHops = uint16(fwdEff) //nolint:gosec // bounded by caller; CLI flag values are small
	}
	if revEff > 0 {
		revMinHops = uint16(revEff) //nolint:gosec // bounded by caller; CLI flag values are small
	}

	// Route count to request per edge: the route-finder defaults to 3
	// routes, so a mux degree above that would be silently capped and
	// starve the mux splitter of legs. Ask for the mux degree (+ small
	// headroom for disjoint-pick overlap) so --forward-mux/--reverse-mux
	// above 3 actually get enough disjoint routes. Zero mux → 0 →
	// finder uses its default.
	fwdNum := findRouteNumFor(opts.EffectiveMuxRoutes(true), opts)
	revNum := findRouteNumFor(opts.EffectiveMuxRoutes(false), opts)

	// A destination this visor already holds a graph for (a hypervisor's
	// attached visors) is routed locally first; the route finder is asked
	// only when that yields nothing.
	if r.conf.PreferLocalRouteTo != nil && r.conf.PreferLocalRouteTo(dst) {
		localFwd, localRev, localErr := r.calculateLocalRoutes(ctx, log, src, dst, opts)
		if localErr == nil {
			r.routeSource.localAttached.Add(1)
			log.Infof("Local route from attached graph: Forward=%v, Reverse=%v", localFwd, localRev)
			return localFwd, localRev, nil
		}
		log.WithError(localErr).Debug("Attached-graph local route failed; asking the route finder")
	}

	r.routeSource.rfQueries.Add(1)
	var paths map[routing.PathEdges][][]routing.Hop
	if fwdMinHops == revMinHops {
		// Common path: single query covers both directions. One count
		// applies to all PathEdges in the call, so request the larger
		// of the two directions' mux degrees.
		num := fwdNum
		if revNum > num {
			num = revNum
		}
		paths, err = r.conf.RouteFinder.FindRoutes(ctx, []routing.PathEdges{forward, backward},
			&rfclient.RouteOptions{MinHops: fwdMinHops, MaxHops: r.conf.MaxHops, NumRoutes: num})
	} else {
		// Asymmetric: two queries with different MinHops per direction.
		// Merge their results into a single map keyed by PathEdges.
		var fwdPaths, revPaths map[routing.PathEdges][][]routing.Hop
		fwdPaths, err = r.conf.RouteFinder.FindRoutes(ctx, []routing.PathEdges{forward},
			&rfclient.RouteOptions{MinHops: fwdMinHops, MaxHops: r.conf.MaxHops, NumRoutes: fwdNum})
		if err == nil {
			revPaths, err = r.conf.RouteFinder.FindRoutes(ctx, []routing.PathEdges{backward},
				&rfclient.RouteOptions{MinHops: revMinHops, MaxHops: r.conf.MaxHops, NumRoutes: revNum})
		}
		if err == nil {
			paths = make(map[routing.PathEdges][][]routing.Hop, 2)
			for k, v := range fwdPaths {
				paths[k] = v
			}
			for k, v := range revPaths {
				paths[k] = v
			}
		}
	}

	if err == nil {
		opts.note("finder: %d forward candidate(s), first hops [%s]", len(paths[forward]), firstHopsOf(paths[forward]))
	}
	if err == rfclient.ErrTransportNotFound {
		// Try local route calculation - may find a local transport that's not yet in TPD
		log.Info("Route finder returned transport not found, attempting local route calculation...")
		opts.note("finder: transport not found; local calc")
		localFwd, localRev, localErr := r.calculateLocalRoutes(ctx, log, src, dst, opts)
		if localErr == nil {
			r.routeSource.localFallback.Add(1)
			log.Infof("Local route calculation succeeded: Forward=%v, Reverse=%v", localFwd, localRev)
			return localFwd, localRev, nil
		}
		log.WithError(localErr).Debug("Local route calculation also failed")
		return nil, nil, err
	}
	// simple retries condition
	if retries == 0 {
		// Try local route calculation as fallback before giving up
		log.Info("Route finder exhausted retries, attempting local route calculation...")
		localFwd, localRev, localErr := r.calculateLocalRoutes(ctx, log, src, dst, opts)
		if localErr == nil {
			r.routeSource.localFallback.Add(1)
			log.Infof("Local route calculation succeeded: Forward=%v, Reverse=%v", localFwd, localRev)
			return localFwd, localRev, nil
		}
		log.WithError(localErr).Warn("Local route calculation also failed")
		err := noRouteErr(fwdMinHops, revMinHops, dst)
		log.Error(err.Error())
		return nil, nil, err
	}
	if retries > 0 {
		retries--
	}

	if err != nil {
		// If the dial context was canceled, the finder error is just fallout from
		// that cancellation (e.g. a faster path — a freshly-created direct
		// transport — won the DialRoutes race and abandoned this query). Return
		// immediately instead of misreporting a "route finder timed out" and
		// burning a redundant local calc.
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		select {
		case <-timer.C:
			// Try local route calculation as fallback
			log.Info("Route finder timed out, attempting local route calculation...")
			localFwd, localRev, localErr := r.calculateLocalRoutes(ctx, log, src, dst, opts)
			if localErr == nil {
				r.routeSource.localFallback.Add(1)
				log.Infof("Local route calculation succeeded: Forward=%v, Reverse=%v", localFwd, localRev)
				return localFwd, localRev, nil
			}
			log.WithError(localErr).Warn("Local route calculation also failed")
			return nil, nil, err
		default:
			time.Sleep(retryInterval)
			goto fetchRoutesAgain
		}
	}

	log.Debugf("Found routes Forward: %s. Reverse %s", paths[forward], paths[backward])

	// DisjointMux post-filter + latency ranking: iterate the route-
	// finder's response and return the path whose intermediates don't
	// overlap with opts.ExcludeIntermediatePKs AND whose total per-hop
	// avg latency is lowest among acceptable candidates. The route-
	// finder service doesn't sort by latency at query time today; the
	// visor has the same TPD latency data available in-memory (own
	// transports via tm.GetLatencyStats) + via TPD entries (already
	// aggregated by pkg/deployment/tpd/cxoaggregator from the
	// CXO telemetry feed). Building the lookup here puts the ranking
	// in the dial hot path with no extra round-trip cost — local-
	// transport latency is a constant-time map read; non-local hops
	// fall back to GetTransport's cached entry. Pass-through when
	// the route-finder returned a single candidate (lookup is built
	// but never iterated).
	// Single TPD walk: builds both the latency rank lookup and the
	// type lookup the DMSG-multihop filter needs. Before merging,
	// each was a separate GetTransportByID per ID — doubling the
	// TPD query rate per dial and tripping the service's
	// 30-req/min rate limit on heavier workloads.
	latencyFor, typeFor, throughputFor := r.buildHopLookups(ctx, paths[forward], paths[backward])

	// Drop any multihop candidate that contains a DMSG hop. A DMSG
	// transport relays through a dmsg server (possibly chained via
	// server-to-server forwarding) that neither endpoint can
	// observe — using one in a multihop route means traffic could
	// transit the same dmsg server multiple times with no way to
	// detect it. Single-hop DMSG paths survive (direct DMSG dials
	// are fine). If every candidate in either direction was DMSG-
	// multihop, the disjoint-path pick below will fail and we'll
	// fall through to calculateLocalRoutes (which applies the same
	// filter at BFS construction time).
	if filtered := rejectDMSGMultihop(paths[forward], typeFor); len(filtered) != len(paths[forward]) {
		log.Debugf("rejected %d DMSG-multihop forward candidate(s)", len(paths[forward])-len(filtered))
		paths[forward] = filtered
	}
	if filtered := rejectDMSGMultihop(paths[backward], typeFor); len(filtered) != len(paths[backward]) {
		log.Debugf("rejected %d DMSG-multihop reverse candidate(s)", len(paths[backward])-len(filtered))
		paths[backward] = filtered
	}

	// Operator-configured hard exclusion (routing.route_exclude_transport_types):
	// drop any candidate route that traverses an excluded transport type, so it
	// never enters the mux leg pool. Unlike transportTypeCostMs (deprioritize), this
	// removes the route entirely — the fix for flaky types (e.g. webrtc) that wedge
	// the reorder frontier. Opt-in and empty by default.
	if len(r.conf.ExcludeTransportTypes) > 0 {
		if filtered := rejectExcludedTypes(paths[forward], typeFor, r.conf.ExcludeTransportTypes); len(filtered) != len(paths[forward]) {
			log.Debugf("excluded %d forward candidate(s) by transport type %v", len(paths[forward])-len(filtered), r.conf.ExcludeTransportTypes)
			paths[forward] = filtered
		}
		if filtered := rejectExcludedTypes(paths[backward], typeFor, r.conf.ExcludeTransportTypes); len(filtered) != len(paths[backward]) {
			log.Debugf("excluded %d reverse candidate(s) by transport type %v", len(paths[backward])-len(filtered), r.conf.ExcludeTransportTypes)
			paths[backward] = filtered
		}
	}

	// Multi-tunnel diversify (opts.DiversifyTransports): steer this extra
	// tunnel's FIRST HOP off the transports its sibling tunnels to the same
	// exit already occupy (opts.ExcludeTransportIDs, seeded by
	// siblingRouteGroupExclusions). The route-finder ranks by latency and does
	// NOT honor ExcludeTransportIDs, so without this every tunnel is handed the
	// same preferred (usually the sole direct) first hop and they contend on one
	// link instead of aggregating — the disjointness the RFC promises collapses.
	// A direct (0-intermediate) sibling contributes NO ExcludeIntermediatePKs, so
	// the intermediate-based pickDisjointPath below cannot break the tie on its
	// own; the first-hop transport-ID is the only distinguishing signal.
	//
	// Preference, not a hard cut: restrict the forward candidates to those whose
	// first-hop transport is not already claimed; if the finder offers none, try
	// the local BFS (which can build a disjoint multi-hop path, and skips the
	// excluded direct transport) before conceding. Only when nothing disjoint
	// exists anywhere do we keep a shared first hop — a shared tunnel still beats
	// no tunnel, just with limited aggregation, which we log.
	if opts.DiversifyTransports && len(opts.ExcludeTransportIDs) > 0 {
		if disjoint := r.freeFirstHops(paths[forward], opts); len(disjoint) > 0 {
			if len(disjoint) != len(paths[forward]) {
				log.Debugf("diversify: %d/%d forward candidate(s) leave over a disjoint first-hop transport; preferring those",
					len(disjoint), len(paths[forward]))
			}
			opts.note("finder: %d/%d candidate(s) leave over a free first hop", len(disjoint), len(paths[forward]))
			// Order the free first hops by measured latency before the pick
			// below: an unused first hop is not automatically a GOOD one (live
			// 2026-09-16 this took a 470ms intermediate while a 39ms one was
			// free). rankByPathLatency is an ordering, not a filter, and the
			// downstream rank is stable, so it decides every tie the whole-path
			// score cannot.
			paths[forward] = r.rankFreeFirstHops(ctx, opts, disjoint, latencyFor)
		} else {
			localFwd, localRev, localErr := r.calculateLocalRoutes(ctx, log, src, dst, opts)
			if localErr == nil && len(localFwd) > 0 && !r.firstHopExcluded(localFwd, opts) {
				opts.note("finder: none of %d disjoint; local-calc path over %s", len(paths[forward]), localFwd[0].TpID.String()[:8])
				log.Debug("diversify: no disjoint first-hop from route finder; using local-calc disjoint path")
				return localFwd, localRev, nil
			}
			// A caller GROWING a pool of sibling tunnels asked to be told when
			// the topology is out of disjoint paths rather than handed a shared
			// one (RequireDisjointFirstHop). This is that moment: the ranked
			// candidate list is exhausted and the local calc had nothing free
			// either. Report it as the settled answer it is, so the caller
			// stops instead of re-dialing.
			if opts.RequireDisjointFirstHop {
				opts.note("finder: none of %d disjoint and no local-calc path; disjoint required, settling", len(paths[forward]))
				log.Debugf("diversify: no disjoint first hop to %s is free and one was required; the pool has settled", dst)
				return nil, nil, noDisjointFirstHopErr(dst, len(paths[forward]))
			}
			opts.note("finder: none of %d disjoint and no local-calc path; sharing a first hop", len(paths[forward]))
			log.Warnf("diversify: no disjoint first-hop transport to %s is free; extra tunnel shares an existing first hop (aggregation limited)", dst)
		}
	}

	// Keep an aux mux leg's REVERSE off the far-end transports the destination's
	// route group already has a leg on. The setup node installs the reverse
	// route's first-hop ForwardRule on the destination, whose appendRouteToGroup
	// refuses a duplicate transport — so a reverse candidate leaving over one of
	// these is a route setup that is certain to fail. The forward and reverse
	// directions are ranked independently below and a direct reverse carries no
	// intermediates, so ExcludeIntermediatePKs cannot express this; only the
	// far-end transport ID can.
	//
	// Preference, not a hard cut (same discipline as the forward diversify
	// above): if no reverse candidate leaves over a free transport we keep the
	// full set and let the caller's pre-dial gate decide, so no dial is ever
	// starved of a route by this filter.
	if len(opts.ExcludeRemoteTransportIDs) > 0 {
		if disjoint := filterDisjointFirstHop(paths[backward], opts.ExcludeRemoteTransportIDs); len(disjoint) > 0 {
			if len(disjoint) != len(paths[backward]) {
				log.Debugf("mux: %d/%d reverse candidate(s) leave the destination over a free transport; preferring those",
					len(disjoint), len(paths[backward]))
			}
			paths[backward] = disjoint
		} else {
			log.Debugf("mux: every reverse candidate leaves %s over a transport its route group already uses; the leg will be skipped before the setup-node dial", dst)
		}
	}

	// Routing-policy candidate selection. When the configured
	// DialHook also implements RouteSelectingHook, hand it the
	// forward-direction candidates so the operator's script can
	// filter / pick by geo / latency / transport-kind. The hook's
	// Chosen index overrides the disjoint-path pick for the
	// forward direction; the reverse direction still goes through
	// the existing latency-ranked pick. Failure-safe — any error
	// from the hook falls through to pickDisjointPath.
	if sh, ok := r.conf.DialHook.(RouteSelectingHook); ok && sh != nil && len(paths[forward]) > 0 {
		toHookCandidates := func(hopsList [][]routing.Hop, isForward bool) []CandidateInfo {
			out := make([]CandidateInfo, 0, len(hopsList))
			endpoint := src
			if !isForward {
				endpoint = dst
			}
			otherEnd := dst
			if !isForward {
				otherEnd = src
			}
			for _, hops := range hopsList {
				intermediates := intermediatesOfHops(hops, endpoint, otherEnd)
				hopHex := make([]string, 0, len(intermediates))
				for _, pk := range intermediates {
					hopHex = append(hopHex, pk.Hex())
				}
				latSum := pathLatencyScore(hops, latencyFor, typeFor, throughputFor)
				out = append(out, CandidateInfo{
					Hops:         hopHex,
					EstLatencyMs: int(latSum),
				})
			}
			return out
		}
		fwdHookCandidates := toHookCandidates(paths[forward], true)
		revHookCandidates := toHookCandidates(paths[backward], false)
		info := DialInfo{
			AppName: opts.AppName,
			PeerPK:  dst,
		}
		if sel, herr := sh.SelectRoute(ctx, info, fwdHookCandidates, revHookCandidates); herr != nil {
			log.WithError(herr).Debug("Routing policy SelectRoute errored; falling back to disjoint-path pick.")
		} else if sel.Drop {
			log.WithField("policy_decision", "drop").Info("Routing policy dropped dial at SelectRoute.")
			return nil, nil, ErrDialPolicyDropped
		} else {
			if sel.Distribution.Mode != DistributionUnset {
				opts.Distribution = sel.Distribution
			}
			// Pick forward and reverse independently — a policy
			// can override one direction and leave the other to
			// the router's default pick.
			haveFwd := sel.Chosen >= 0 && sel.Chosen < len(paths[forward])
			haveRev := sel.ReverseChosen >= 0 && sel.ReverseChosen < len(paths[backward])
			if haveFwd || haveRev {
				excludeSet := make(map[cipher.PubKey]struct{}, len(opts.ExcludeIntermediatePKs))
				for _, pk := range opts.ExcludeIntermediatePKs {
					excludeSet[pk] = struct{}{}
				}
				var chosenFwd, chosenRev []routing.Hop
				if haveFwd {
					chosenFwd = paths[forward][sel.Chosen]
				} else if best, ok := pickBestDirection(paths[forward], excludeSet, latencyFor, typeFor, throughputFor); ok {
					chosenFwd = best
				}
				if haveRev {
					chosenRev = paths[backward][sel.ReverseChosen]
				} else if best, ok := pickBestDirection(paths[backward], excludeSet, latencyFor, typeFor, throughputFor); ok {
					chosenRev = best
				}
				if chosenFwd != nil && chosenRev != nil {
					log.
						WithField("policy_chosen", sel.Chosen).
						WithField("policy_reverse_chosen", sel.ReverseChosen).
						Debug("Routing policy selected route(s).")
					return chosenFwd, chosenRev, nil
				}
				log.Debug("Routing policy made a partial pick but no acceptable counterpart; falling back to disjoint pick.")
			}
		}
	}

	fwdPath, revPath, ok := pickDisjointPath(paths[forward], paths[backward], opts.ExcludeIntermediatePKs, latencyFor, typeFor, throughputFor)
	if !ok {
		log.Debugf("No route-finder path avoids the %d excluded intermediates; trying local route calc fallback", len(opts.ExcludeIntermediatePKs))
		localFwd, localRev, localErr := r.calculateLocalRoutes(ctx, log, src, dst, opts)
		if localErr == nil {
			return localFwd, localRev, nil
		}
		return nil, nil, ErrNoRouteFound
	}
	return fwdPath, revPath, nil
}

// pickDisjointPath returns the forward/reverse path pair whose
// intermediate hops do not contain any PK in exclude AND whose
// per-direction latency is lowest. Forward and reverse are RANKED
// INDEPENDENTLY — the route-finder returns each direction as its own
// candidate slice (PathEdges{forward, backward} at the FindRoutes call
// site) and the data plane stores fwd / rvs as separate rule chains
// (route_group.fwd / route_group.rvs), so there's no requirement that
// the two paths share the same intermediates.
//
// Pre-#2782 this function paired by index (forward[i], reverse[i]),
// which threw away the independent-direction signal: if forward had
// a great candidate at index 2 but the best reverse was at index 0,
// pairing-by-index would force forward[0] + reverse[0] (or whatever
// matching pair scored lowest combined). Unpairing closes that gap.
//
// latencyFor is a per-TpID latency lookup (avg-ms; 0 = unknown). Hops
// with unknown latency are treated as a high cost so paths with
// measured signal outrank paths with no signal at all. Pass nil to
// disable latency ranking (legacy first-acceptable behavior).
//
// When exclude is empty AND latencyFor is nil: returns paths[0] (the
// pre-existing hot-path single-route behavior — back-compat for tests
// and any caller that doesn't opt in to ranking).
//
// ok=false means every candidate in at least one direction touched
// the exclude set — caller should fall back to local route calc or
// surface ErrNoRouteFound.
//
// Rationale for asymmetric ranking (operator framing 2026-05-23):
// most real workloads are bandwidth-asymmetric (HTTP GET = tiny
// upstream + bulk downstream). Allowing forward and reverse to use
// different intermediates means each direction can pick its best
// available wire, not the best PAIR — which is strictly weaker when
// the per-direction optimums sit at different indices.
func pickDisjointPath(forward, reverse [][]routing.Hop, exclude []cipher.PubKey, latencyFor func(uuid.UUID) float64, typeFor func(uuid.UUID) string, throughputFor func(uuid.UUID) float64) ([]routing.Hop, []routing.Hop, bool) {
	if len(forward) == 0 || len(reverse) == 0 {
		return nil, nil, false
	}
	if len(exclude) == 0 && latencyFor == nil && typeFor == nil && throughputFor == nil {
		// Hot path: no constraint, no ranker. Preserve the original
		// first-element behavior so back-compat single-route callers
		// are unaffected (tests that don't pass a latencyFor).
		return forward[0], reverse[0], true
	}
	excludeSet := make(map[cipher.PubKey]struct{}, len(exclude))
	for _, pk := range exclude {
		excludeSet[pk] = struct{}{}
	}
	fwdPath, fwdOK := pickBestDirection(forward, excludeSet, latencyFor, typeFor, throughputFor)
	revPath, revOK := pickBestDirection(reverse, excludeSet, latencyFor, typeFor, throughputFor)
	if !fwdOK || !revOK {
		return nil, nil, false
	}
	return fwdPath, revPath, true
}

// pickBestDirection returns the lowest-latency-scoring candidate from
// a single direction's path slice that doesn't touch the exclude set.
// When latencyFor is nil, returns the first acceptable candidate (the
// legacy first-after-exclude behavior — preserved for callers that
// don't opt into ranking, e.g. the existing tests).
func pickBestDirection(paths [][]routing.Hop, excludeSet map[cipher.PubKey]struct{}, latencyFor func(uuid.UUID) float64, typeFor func(uuid.UUID) string, throughputFor func(uuid.UUID) float64) ([]routing.Hop, bool) {
	bestIdx := -1
	bestScore := 0.0
	for i, p := range paths {
		if pathTouchesIntermediate(p, excludeSet) {
			continue
		}
		if latencyFor == nil && typeFor == nil && throughputFor == nil {
			return p, true
		}
		score := pathLatencyScore(p, latencyFor, typeFor, throughputFor)
		if bestIdx < 0 || score < bestScore {
			bestIdx = i
			bestScore = score
		}
	}
	if bestIdx < 0 {
		return nil, false
	}
	return paths[bestIdx], true
}

// firstHopCarrierClass classes a candidate by the carrier of its first hop —
// the transport this visor owns and chose. Later hops are not classed: their
// carriers are not this visor's decision, and the evidence is about the link
// it dials. An unrecognized or underivable type is classed with the others,
// which is where an unknown carrier belongs.
func firstHopCarrierClass(path []routing.Hop) int {
	if len(path) == 0 {
		return carrierClassOther
	}
	h := path[0]
	switch transport.TypeFromTransportID(h.TpID, h.From, h.To) {
	case tptypes.STCPR, tptypes.QUIC, tptypes.STCP:
		return carrierClassDirect
	case tptypes.SUDPH:
		return carrierClassHole
	case tptypes.WEBRTC:
		return carrierClassP2P
	default:
		return carrierClassOther
	}
}

// rankByPathLatency orders a diversify dial's admissible candidates (those whose
// first hop is not already claimed by a sibling tunnel and is not a same-LAN
// neighbor, i.e. the output of freeFirstHops) by their first hop's CARRIER
// CLASS first (see carrierClassDirect — a resolved TCP/QUIC link beats a
// hole-punched UDP one however good its ping) and then, within a class, by
// their MEASURED END-TO-END latency — the sum of every hop's latency — lowest
// first. A candidate whose FIRST hop is unmeasured sorts LAST: an unknown link is
// not evidence of a good one. Ties break on
// fewer hops, then on the input order (the sort is stable), so the route-finder's
// own preference survives wherever the latency signal cannot separate candidates.
//
// The score is the WHOLE path, not just the first hop. A first hop this visor
// can measure says nothing about where the route goes afterwards: ranking on it
// alone picked a LAN neighbor whose own hop to the exit was unknown — 1ms to
// the first hop, and 50 MB down measured 3.85 MB/s, 10 MB down 2.46, worse than
// the 470ms intermediate the ranking was introduced to avoid (2026-09-18).
//
// A PARTIALLY measured path is still ranked, by the sum of the hops it does
// know (at least its first hop) plus unknownHopPenaltyMs per unknown hop — it
// is not dropped to the bottom. Sorting partial knowledge with the unmeasured
// threw away the only intermediate worth having: on 2026-09-17
// (bench/2026-09-16/a6a42506f-smoke)
// a 33ms first hop whose path sample was stale — another client on this visor
// held the route — ranked below a 131+1ms candidate and lost the dial, having
// measured 8.23 MB/s against the winner's 3.03 minutes earlier. The penalty is
// bounded so a fast tp with an unknown remainder can beat a slow known path but
// never a comparable known one.
//
// Above all of it sits the operator's prefer list (route settings dial
// --dial-prefer-pks): a candidate whose path touches a named peer is taken
// first, and the ordering below then decides among those. The list is empty by
// default, which makes the comparison fall straight through — an untouched
// visor ranks exactly as it did.
//
// This is an ORDERING, not a filter: no candidate is ever dropped, so a dial is
// never starved of a route by it. The downstream rankers sort stably, so this
// order decides every tie they cannot.
func rankByPathLatency(cands [][]routing.Hop, latencyFor func(uuid.UUID) float64) [][]routing.Hop {
	if len(cands) < 2 {
		return cands
	}
	type scored struct {
		path    []routing.Hop
		prefer  bool
		class   int
		ms      float64
		unknown bool
	}
	acc := make([]scored, 0, len(cands))
	for _, p := range cands {
		s := scored{path: p, class: firstHopCarrierClass(p), unknown: true, prefer: pathPrefersPK(p)}
		if ms, ok := pathLatencyTotalMs(p, latencyFor); ok {
			s.ms, s.unknown = ms, false
		} else if ms, ok := pathLatencyPartialMs(p, latencyFor); ok {
			s.ms, s.unknown = ms, false
		}
		acc = append(acc, s)
	}
	sort.SliceStable(acc, func(i, j int) bool {
		a, b := acc[i], acc[j]
		// The operator's prefer list outranks everything the measurement can
		// say. `route settings dial --dial-prefer-pks <pk,…>` is the answer to
		// "auto-diversify cannot prefer the routes that won a set": a route
		// through a named peer is taken first, and the rest of this ordering
		// then decides among them. Empty list (the default) makes every
		// candidate unpreferred, so the comparison falls straight through.
		if a.prefer != b.prefer {
			return a.prefer
		}
		if a.class != b.class {
			return a.class < b.class
		}
		if a.unknown != b.unknown {
			return !a.unknown
		}
		if !a.unknown && a.ms != b.ms {
			return a.ms < b.ms
		}
		return len(a.path) < len(b.path)
	})
	out := make([][]routing.Hop, 0, len(acc))
	for _, s := range acc {
		out = append(out, s.path)
	}
	return out
}

// firstHopLatencyMs returns the measured latency of a path's FIRST hop in
// milliseconds, or 0 when it has no measurement at all. Sources in order: the
// dial's own TpID→latency lookup (buildHopLookups: this visor's ping/pong
// average for a transport it holds, else TPD's latency_ms for the entry), then
// the per-hop Latency the route-finder attached to the candidate. Zero means
// unmeasured everywhere — ManagedTransport.GetLatency and TransportEntry.Latency
// are both "0 = never sampled", never a real reading.
func firstHopLatencyMs(path []routing.Hop, latencyFor func(uuid.UUID) float64) float64 {
	if len(path) == 0 {
		return 0
	}
	return hopLatencyMs(path[0], latencyFor)
}

// hopLatencyMs returns ONE hop's latency in milliseconds, 0 when unmeasured.
// Sources in order: the dial's TpID→latency lookup (buildHopLookups — this
// visor's ping/pong average for a transport it holds, else the TPD entry's
// latency_ms for the link, which is where an INTERMEDIATE→exit hop's number
// comes from since this visor never touches that link), then the per-hop
// Latency the route-finder attached to the candidate (routing.Hop.Latency, its
// own per-edge TPD reading). Zero means unmeasured in every source —
// ManagedTransport.GetLatency and transport.Entry.Latency are both "0 = never
// sampled", never a real reading.
func hopLatencyMs(h routing.Hop, latencyFor func(uuid.UUID) float64) float64 {
	if latencyFor != nil {
		if ms := latencyFor(h.TpID); ms > 0 {
			return ms
		}
	}
	if h.Latency > 0 {
		return h.Latency
	}
	return 0
}

// pathLatencyTotalMs sums EVERY hop's latency. ok is false when the path is
// empty or any hop is unmeasured — a route whose second hop is unknown carries
// no usable prediction of what it will deliver, however fast its first hop is,
// so it must not outrank one that is known end to end.
func pathLatencyTotalMs(path []routing.Hop, latencyFor func(uuid.UUID) float64) (float64, bool) {
	if len(path) == 0 {
		return 0, false
	}
	var total float64
	for _, h := range path {
		ms := hopLatencyMs(h, latencyFor)
		if ms <= 0 {
			return 0, false
		}
		total += ms
	}
	return total, true
}

// unknownHopPenaltyMs is what one unmeasured hop costs a partially-measured
// path in pathLatencyPartialMs. It sits above a typical intra-continent hop and
// below a bad one (the campaign fleet spans 39ms to 470ms), so a fast tp into an
// unknown remainder outranks a known-slow path but never a known-comparable one.
// Live-settable via DialUnknownHopPenaltyMs (settings_dial.go); this is its
// default.
const unknownHopPenaltyMs = dialUnknownHopPenaltyDefaultMs

// pathLatencyPartialMs scores a path whose latency is known for some hops but
// not all: the sum of the hops that ARE measured plus unknownHopPenaltyMs for
// each that is not. ok is false when the path is empty or its FIRST hop is
// unmeasured — that is the "unmeasured" candidate the on-demand probe exists
// for, and it keeps sorting last. This is the fallback for a route whose
// end-to-end sample is missing or stale (another client on this visor holding
// it, say), which pathLatencyTotalMs alone reported as unrankable.
func pathLatencyPartialMs(path []routing.Hop, latencyFor func(uuid.UUID) float64) (float64, bool) {
	if firstHopLatencyMs(path, latencyFor) <= 0 {
		return 0, false
	}
	penalty := DialUnknownHopPenaltyMs()
	var total float64
	for _, h := range path {
		if ms := hopLatencyMs(h, latencyFor); ms > 0 {
			total += ms
			continue
		}
		total += penalty
	}
	return total, true
}

// pathLatencyTrail renders each candidate as its first-hop transport (8 chars,
// as the rest of the decision trail names transports) and its PER-HOP latencies,
// so `mux info`'s dial_decision shows exactly what the ranking knew:
//
//	"f1012467=134+10ms, fdab37dd=144+10ms, 0be8b6f0=1ms+unknown, 50cdb857=unmeasured"
//
// A hop whose number came from the on-demand probe is marked. "+unknown" is the
// case the whole-path rule exists for: a fast first hop into an unknown
// remainder, which sorts with the unmeasured rather than at the top.
func pathLatencyTrail(cands [][]routing.Hop, latencyFor func(uuid.UUID) float64, probed map[uuid.UUID]float64) string {
	out := make([]string, 0, len(cands))
	for _, p := range cands {
		if len(p) == 0 {
			out = append(out, "-")
			continue
		}
		name := p[0].TpID.String()[:8]
		// Name the carrier when it is derivable, since it is now the ranking's
		// FIRST key and a reading of the trail that cannot see it would look
		// like a latency ordering gone wrong.
		if t := transport.TypeFromTransportID(p[0].TpID, p[0].From, p[0].To); t != "" {
			name += "/" + string(t)
		}
		if firstHopLatencyMs(p, latencyFor) <= 0 {
			out = append(out, name+"=unmeasured")
			continue
		}
		parts := make([]string, 0, len(p))
		complete := true
		for _, h := range p {
			ms := hopLatencyMs(h, latencyFor)
			if ms <= 0 {
				parts = append(parts, "unknown")
				complete = false
				continue
			}
			s := fmt.Sprintf("%.0f", ms)
			if _, ok := probed[h.TpID]; ok {
				s += "p"
			}
			parts = append(parts, s)
		}
		if complete {
			// Every hop known: "134+10ms" reads as one end-to-end number.
			out = append(out, name+"="+strings.Join(parts, "+")+"ms")
			continue
		}
		// Mixed: spell the unit on each known hop so "1ms+unknown" cannot be
		// misread as a total, and say what the candidate was actually ranked on,
		// since it is no longer sorted with the unmeasured.
		for i, s := range parts {
			if s != "unknown" {
				parts[i] = s + "ms"
			}
		}
		entry := name + "=" + strings.Join(parts, "+")
		if ms, ok := pathLatencyPartialMs(p, latencyFor); ok {
			entry += fmt.Sprintf(" (path latency unknown, ranked by tp latency %.0f ms)", ms)
		}
		out = append(out, entry)
	}
	return strings.Join(out, ", ")
}

// firstHopProbeTimeout bounds the on-demand first-hop latency probe. One round
// trip to an intermediate is tens to hundreds of milliseconds (39ms to 470ms
// across the campaign fleet), and every candidate is probed CONCURRENTLY, so
// this is a ceiling on the whole probe, not a per-candidate cost. A dial is
// never delayed longer than this, and anything still unmeasured when it expires
// simply sorts last.
const firstHopProbeTimeout = 2 * time.Second

// latencyProber is the transport-level active probe the diversify ranking uses:
// one ping, wait for the pong, bounded by ctx. *transport.ManagedTransport
// implements it; the interface exists so the ranking can be tested without a
// live link.
type latencyProber interface {
	ProbeLatency(ctx context.Context) (float64, error)
}

// probeFirstHopLatencies measures the candidates' first hops that have no
// sample, all at once, bounded by timeout. known reports an already-known
// latency for a TpID (0 = none); probe yields the prober for a TpID, or nil when
// this visor does not hold that transport (an intermediate-to-exit hop, say).
// Returns only the values the probe actually obtained, keyed by TpID.
//
// Why a dial probes at all: the periodic transport ping samples a transport only
// once it is READY, so a first hop this visor holds but has never sent traffic
// over reports nothing — which is the state every candidate intermediate was in
// when a multi-tunnel dial had to choose one (2026-09-17: "50cdb857=unmeasured,
// f1012467=unmeasured, …", and the tie fell to the finder's arbitrary order,
// landing on the 470ms hop again). One round trip per candidate, in parallel,
// turns that into a real ranking.
func probeFirstHopLatencies(
	ctx context.Context,
	cands [][]routing.Hop,
	known func(uuid.UUID) float64,
	probe func(uuid.UUID) latencyProber,
	timeout time.Duration,
) map[uuid.UUID]float64 {
	if len(cands) == 0 || probe == nil {
		return nil
	}
	todo := make(map[uuid.UUID]latencyProber)
	for _, p := range cands {
		if len(p) == 0 {
			continue
		}
		id := p[0].TpID
		if _, seen := todo[id]; seen {
			continue
		}
		if firstHopLatencyMs(p, known) > 0 {
			continue // already measured; no round trip needed
		}
		if pr := probe(id); pr != nil {
			todo[id] = pr
		}
	}
	if len(todo) == 0 {
		return nil
	}

	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var mu sync.Mutex
	out := make(map[uuid.UUID]float64, len(todo))
	var wg sync.WaitGroup
	for id, pr := range todo {
		wg.Add(1)
		go func(id uuid.UUID, pr latencyProber) {
			defer wg.Done()
			ms, err := pr.ProbeLatency(pctx)
			if err != nil || ms <= 0 {
				return // still unmeasured — it will sort last
			}
			mu.Lock()
			out[id] = ms
			mu.Unlock()
		}(id, pr)
	}
	wg.Wait()
	if len(out) == 0 {
		return nil
	}
	return out
}

// rankFreeFirstHops is THE candidate ordering for a diversify dial, shared by
// every selection that can win one (the route-finder path, the K-candidate race,
// the RSN oracle and the hook-race direct route) so they cannot disagree. It
//
//  1. drops same-LAN first hops (they share our uplink — see filterLANFirstHops)
//     and names them on the trail;
//  2. resolves per-hop latencies, including the INTERMEDIATE→exit hop, which only
//     the TPD-backed lookup knows (buildHopLookups); callers that already built
//     one pass it in, the others get one here;
//  3. probes the first hops that still carry no measurement (bounded, parallel);
//  4. ranks by END-TO-END path latency and records the ranking — marking probed
//     hops — on the dial's decision trail.
func (r *router) rankFreeFirstHops(ctx context.Context, opts *DialOptions, cands [][]routing.Hop, latencyFor func(uuid.UUID) float64) [][]routing.Hop {
	cands, lanDropped := r.filterLANFirstHops(cands)
	if len(lanDropped) > 0 {
		opts.note("excluding %d same-LAN first hop(s) (share our uplink): %s",
			len(lanDropped), firstHopsOf(lanDropped))
	}
	if len(cands) == 0 {
		return cands
	}
	if latencyFor == nil {
		// The oracle and the hook-race build no lookup of their own, and a
		// candidate's SECOND hop (intermediate→exit) is a link this visor never
		// touches, so without this it is unmeasurable and every 2-hop candidate
		// would sort as unknown.
		latencyFor, _, _ = r.buildHopLookups(ctx, cands, nil)
	}
	probed := probeFirstHopLatencies(ctx, cands, latencyFor, r.firstHopProber, firstHopProbeTimeout)
	return notePathRanking(opts, cands, mergeProbedLatency(latencyFor, probed), probed)
}

// firstHopProber returns the active prober for a transport this visor holds, or
// nil for one it does not (so a hop deeper in the path is never probed).
func (r *router) firstHopProber(id uuid.UUID) latencyProber {
	if r.tm == nil {
		return nil
	}
	tp := r.tm.Transport(id)
	if tp == nil {
		return nil
	}
	return tp
}

// mergeProbedLatency layers a probe's fresh readings over the dial's existing
// TpID→latency lookup.
func mergeProbedLatency(latencyFor func(uuid.UUID) float64, probed map[uuid.UUID]float64) func(uuid.UUID) float64 {
	if len(probed) == 0 {
		return latencyFor
	}
	return func(id uuid.UUID) float64 {
		if ms, ok := probed[id]; ok && ms > 0 {
			return ms
		}
		if latencyFor == nil {
			return 0
		}
		return latencyFor(id)
	}
}

// notePathRanking ranks cands by end-to-end path latency and records the ranking on the dial's decision
// trail. probed names the values a live probe supplied, so the trail says which
// numbers were measured on the spot.
func notePathRanking(opts *DialOptions, cands [][]routing.Hop, latencyFor func(uuid.UUID) float64, probed map[uuid.UUID]float64) [][]routing.Hop {
	ranked := rankByPathLatency(cands, latencyFor)
	if len(ranked) == 0 {
		return ranked
	}
	opts.note("ranked by carrier class then path latency: %s; chose %s",
		pathLatencyTrail(ranked, latencyFor, probed), firstHopsOf(ranked[:1]))
	return ranked
}

// transportTypeCostMs is a per-hop, latency-equivalent penalty that biases
// route ranking toward high-THROUGHPUT transports. RTT alone can't do this: a
// webrtc DTLS/SCTP datachannel has LOW ping/pong RTT but POOR throughput, so
// without this term the ranker scores a webrtc hop as cheap as an stcpr one,
// and disjoint mux legs pile onto the few webrtc-only intermediates (webrtc is
// ~5% of network transports yet dominated the mux legs). The magnitudes are
// ordered by the transport's throughput class — TCP (stcpr) and QUIC (squicr)
// fast; UDP hole-punch (sudph) ok; SCTP-over-DTLS (webrtc) and relayed (dmsg)
// slow — expressed in ms so they compose additively with the RTT score. It is a
// bias, not a filter: a genuinely fast webrtc hop can still win on total score,
// but all else equal a fast transport is preferred.
func transportTypeCostMs(tpType string) float64 {
	switch tptypes.Type(tpType) {
	case tptypes.STCPR, tptypes.QUIC, tptypes.QUICLegacy, tptypes.QUICLegacy2:
		return 0
	case tptypes.SUDPH:
		return 25
	case tptypes.STCP:
		return 50
	case tptypes.WT, tptypes.WTLegacy, tptypes.WS, tptypes.WSLegacy:
		return 150
	case tptypes.WEBRTC:
		return 300
	case tptypes.DMSG:
		return 400
	default:
		return 100 // unknown type — mild penalty, below webrtc
	}
}

// transportCostMs is the per-hop throughput penalty used by route ranking. When
// a MEASURED throughput estimate exists (throughputBps > 0, from the passive
// observer / packet-pair probe) it is used DIRECTLY — a link measured fast
// scores 0 regardless of type, a link measured slow is penalized regardless of
// type — so ranking is by evidence, and a genuinely-fast webrtc link is kept
// while a congested stcpr one is avoided. transportTypeCostMs is only the PRIOR,
// used until a real measurement exists. Bytes/sec thresholds ≈ 40/10/2/0.4 Mbps.
// Both terms are scaled by the live DialTypePriorScale / DialThroughputPriorScale
// knobs (default 1.0 — the magnitudes below), so an operator can weaken the
// prior toward pure RTT ranking (0 removes it) or sharpen it, on a running
// visor. See settings_dial.go.
func transportCostMs(tpType string, throughputBps float64) float64 {
	if throughputBps <= 0 {
		return transportTypeCostMs(tpType) * DialTypePriorScale() // no measurement yet — type prior
	}
	var band float64
	switch {
	case throughputBps >= 5_000_000:
		band = 0
	case throughputBps >= 1_250_000:
		band = 25
	case throughputBps >= 250_000:
		band = 100
	case throughputBps >= 50_000:
		band = 250
	default:
		band = 400 // measured slow — worse than the webrtc prior
	}
	return band * DialThroughputPriorScale()
}

// pathLatencyScore returns the sum of per-hop avg-latency-ms across a
// path, treating unknown latencies (latencyFor returning 0) as a high
// cost so paths with measured latency outrank paths with no signal at
// all. The penalty (unknownLatencyCostMs) is set above the practical
// upper bound of healthy single-hop latency — currently 1000ms — so a
// single unknown hop already loses to a known 500ms hop, but a path
// composed entirely of unknown hops is still preferred over no path.
//
// typeFor adds the per-hop transport-type throughput penalty (see
// transportTypeCostMs) so fast transports outrank slow ones at equal RTT. It
// is nil-safe: a nil typeFor scores on RTT alone (back-compat for callers /
// tests that don't thread transport type).
func pathLatencyScore(path []routing.Hop, latencyFor func(uuid.UUID) float64, typeFor func(uuid.UUID) string, throughputFor func(uuid.UUID) float64) float64 {
	unknownLatencyCostMs := DialUnknownLatencyCostMs()
	var total float64
	for _, h := range path {
		var ms float64
		if latencyFor != nil {
			ms = latencyFor(h.TpID)
		}
		if ms <= 0 {
			total += unknownLatencyCostMs
		} else {
			total += ms
		}
		// Throughput penalty: MEASURED capacity when available, else the
		// transport-type prior. Only applied when typeFor is threaded (the real
		// dial path); nil typeFor scores on RTT alone (back-compat tests).
		if typeFor != nil {
			var tp float64
			if throughputFor != nil {
				tp = throughputFor(h.TpID)
			}
			total += transportCostMs(typeFor(h.TpID), tp)
		}
	}
	return total
}

// findRouteNum returns the per-edge route count to request from the
// route-finder for a dial with the given effective mux degree. Zero
// (mux off) returns 0, letting the finder apply its own default. A
// positive mux degree returns mux+headroom, floored at the historical
// default so we never request fewer candidates than before. The
// finder clamps the value to its own ceiling.
//
// Both terms are live knobs (DialRouteCandidates / DialMuxRouteHeadroom, see
// settings_dial.go); the constants above are their defaults.
func findRouteNum(mux int) uint16 {
	if mux <= 0 {
		return 0
	}
	n := mux + DialMuxRouteHeadroom()
	if floor := DialRouteCandidates(); n < floor {
		n = floor
	}
	if n > math.MaxUint16 {
		n = math.MaxUint16
	}
	return uint16(n) //nolint:gosec // clamped to MaxUint16 above
}

// findRouteNumFor is findRouteNum with the DIAL in view, because a diversify
// dial needs a different number than a mux dial does.
//
// findRouteNum sizes the request off the mux degree: how many legs this one
// route group wants. A standby-pool fill asks for mux 1 and therefore for
// dial.candidates = 3 routes — and it asks that same question thirty-two times
// in a row while excluding what it already holds. The finder answers
// rank-ordered, so those three are the same three every time: the pool re-sees
// its own lowest-latency route, every candidate is refused as a held first hop,
// and the fill settles far below a topology that has hundreds of distinct
// intermediates to offer (measured 2026-09-18: 1,332 transports over 662
// distinct peers, a pool of 32 tunnels over 4 transports).
//
// So a dial that is explicitly diversifying — the pool fill sets both
// DiversifyTransports and RequireDisjointFirstHop — asks for a WINDOW instead
// of a top-N: dial.diversify_candidates routes, defaulting to 20, which is
// also the route finder's own per-request ceiling today. The mux-degree
// calculation still applies and still wins when it asks for more — including
// mux 0, where the window replaces the "let the finder pick its own default"
// sentinel, because a dial that is diversifying needs alternatives whether or
// not it is also muxing.
func findRouteNumFor(mux int, opts *DialOptions) uint16 {
	n := findRouteNum(mux)
	if opts == nil || !opts.DiversifyTransports || !opts.RequireDisjointFirstHop {
		return n
	}
	want := DialDiversifyCandidates()
	if want > math.MaxUint16 {
		want = math.MaxUint16
	}
	if uint16(want) > n { //nolint:gosec // clamped to MaxUint16 above
		return uint16(want) //nolint:gosec // clamped to MaxUint16 above
	}
	return n
}
