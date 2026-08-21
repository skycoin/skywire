//go:build !tinygo || (js && wasm)

// Package router pkg/router/parallel_route_setup.go c2-net-routing
//
// parallel_route_setup.go implements PARALLEL candidate route-group setup:
// the steady-connection fix. Instead of the old one-candidate-at-a-time
// retry loop — where every dead / non-forwarding candidate burned the full
// handshakeAwaitTimeout (~10s) before the next was tried, so a run of bad
// candidates could burn the whole ~90s dial ceiling and wedge the app in
// "starting" — DialRoutes now sets up the top-K candidate routes CONCURRENTLY
// and the FIRST to complete its reciprocal handshake WINS and becomes the
// working route. Losing / failed candidates are canceled, torn down, and
// their intermediate hops marked suspect for a short TTL so the NEXT dial
// front-loads known-good hops.
//
// Data-model constraint (why the two phases below): incoming handshake / data
// packets are demuxed to a route group by RouteDescriptor (see
// dispatchToRouteGroup), and every candidate of one dial shares the SAME
// descriptor (same src/dst/ports). The router therefore holds at most ONE raw
// route group per descriptor (r.rgsRaw[desc]), so two candidates cannot run
// their reciprocal handshakes concurrently — the second would collide on the
// descriptor. The race is consequently two-phase:
//
//	phase 1 (CONCURRENT): dial/reserve route IDs across every candidate's hops.
//	    This is where an unreachable intermediate fails id_reservation and loses
//	    the race cheaply, instead of blocking the whole dial. This is the
//	    dominant live failure mode (dead/non-forwarding intermediate).
//	phase 2 (SERIAL, fastest-reservation-first): complete the reciprocal
//	    handshake on reserved candidates; the FIRST to complete wins and is
//	    returned immediately (it is the never-dropped working base — additional
//	    mux legs grow on top later and must never unseat it). Because the
//	    candidates are already reserved, walking to the next one on a handshake
//	    failure costs no re-fetch and no re-reservation — unlike the old loop.
package router

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/rfclient"
	"github.com/skycoin/skywire/pkg/routing"
)

// suspectHopPenaltyMs is the per-suspect-intermediate cost added to a
// candidate path's latency score when ranking, so a path through a hop that
// just lost / failed a setup race sorts BELOW any clean path (2x the
// unknown-latency cost). Soft, not a hard exclude: if every candidate touches
// a suspect, the least-bad one is still tried — the goal is to converge to
// SOME working route, never to wedge.
const suspectHopPenaltyMs = 2000.0

// suspectCount returns how many INTERMEDIATE hops of a forward path are
// currently suspect. Endpoints (src/dst) are never counted.
func (c *suspectHopCache) suspectCount(path []routing.Hop, src, dst cipher.PubKey, now time.Time) int {
	if c == nil || len(path) == 0 {
		return 0
	}
	n := 0
	for _, pk := range intermediatePKsOfPath(path, src, dst) {
		if c.isSuspect(pk, now) {
			n++
		}
	}
	return n
}

// effectiveParallelK resolves the number of candidates to race for a dial:
// the per-call opts override if > 0, else the visor config, clamped to
// [1, maxParallelRouteSetup]. 1 means strictly-sequential setup.
func (r *router) effectiveParallelK(opts *DialOptions) int {
	k := 0
	if opts != nil && opts.ParallelRouteSetup > 0 {
		k = opts.ParallelRouteSetup
	} else if r.conf != nil {
		k = r.conf.ParallelRouteSetup
	}
	if k <= 0 {
		k = 1
	}
	if k > maxParallelRouteSetup {
		k = maxParallelRouteSetup
	}
	return k
}

// hasRouteSelectingHook reports whether an operator routing policy explicitly
// selects the route (RouteSelectingHook). When it does, the race is skipped:
// the policy picks a specific candidate, so racing several would defeat the
// operator's choice. The dial falls back to the sequential selecting path.
func (r *router) hasRouteSelectingHook() bool {
	if r.conf == nil || r.conf.DialHook == nil {
		return false
	}
	_, ok := r.conf.DialHook.(RouteSelectingHook)
	return ok
}

// candidateOutcome is one candidate's phase-1 (dial/reserve) result.
type candidateOutcome struct {
	idx   int
	rules routing.EdgeRules
	node  cipher.PubKey // connected setup node (for ReorderSetupNodes); may be null
	err   error
}

// dialFn reserves route IDs across one candidate's hops (phase 1, concurrent).
type dialFn func(ctx context.Context, cand routing.BidirectionalRoute) (routing.EdgeRules, cipher.PubKey, error)

// handshakeFn installs local rules and completes the reciprocal handshake for
// one reserved candidate (phase 2, serial). On failure it must leave no local
// rules/route-group behind.
type handshakeFn func(ctx context.Context, cand routing.BidirectionalRoute, rules routing.EdgeRules) (*NoiseRouteGroup, error)

// raceCandidateSetup runs the two-phase concurrent race described in the file
// header. It returns the winning route group, its installed rules, and the
// winning candidate index. onLoser is invoked for every candidate that fails
// its reservation OR its handshake (used to arm the suspect penalty); it is
// NOT invoked for the winner.
//
// Guarantees:
//   - first candidate to complete its handshake wins and is returned at once;
//   - remaining in-flight reservations are canceled via a child context, so no
//     goroutine leaks (the result channel is buffered to len(candidates));
//   - losers install no local rules (un-tried reservations are never saved;
//     tried-but-failed handshakes clean up inside handshake). Orphaned REMOTE
//     reservations expire via the remote visors' rule GC, exactly as failed
//     candidates in the legacy sequential loop did.
func (r *router) raceCandidateSetup(
	ctx context.Context,
	log *logging.Logger,
	candidates []routing.BidirectionalRoute,
	dial dialFn,
	handshake handshakeFn,
	onLoser func(cand routing.BidirectionalRoute),
) (*NoiseRouteGroup, routing.EdgeRules, int, error) {
	if log == nil {
		log = r.logger
	}
	if len(candidates) == 0 {
		return nil, routing.EdgeRules{}, -1, ErrNoRouteFound
	}

	raceCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	ch := make(chan candidateOutcome, len(candidates))
	for i, c := range candidates {
		go func(i int, c routing.BidirectionalRoute) {
			rules, node, err := dial(raceCtx, c)
			ch <- candidateOutcome{idx: i, rules: rules, node: node, err: err}
		}(i, c)
	}

	pending := len(candidates)
	var lastErr error
	for pending > 0 {
		select {
		case <-ctx.Done():
			return nil, routing.EdgeRules{}, -1, ctx.Err()
		case out := <-ch:
			pending--
			if out.err != nil {
				lastErr = out.err
				// A dead/unreachable reservation: penalize its intermediates
				// so the next dial routes around them, then let a healthier
				// candidate carry the race.
				if onLoser != nil {
					onLoser(candidates[out.idx])
				}
				continue
			}
			// Reservation succeeded. Prioritize the setup node that worked for
			// the next dial (same as the sequential path).
			if !out.node.Null() {
				r.conf.SetupNodes = ReorderSetupNodes(r.conf.SetupNodes, out.node)
			}
			// Serial handshake stage (shared descriptor forbids concurrency).
			nrg, herr := handshake(ctx, candidates[out.idx], out.rules)
			if herr != nil {
				lastErr = herr
				if onLoser != nil {
					onLoser(candidates[out.idx])
				}
				// If the parent dial ctx died, stop — the whole dial is done.
				if ctx.Err() != nil {
					return nil, routing.EdgeRules{}, -1, ctx.Err()
				}
				log.WithError(herr).Debugf("raced candidate %d failed handshake; trying next reserved candidate", out.idx)
				continue
			}
			// WINNER. Cancel remaining in-flight reservations and return the
			// working route immediately.
			cancel()
			log.Debugf("raced candidate %d won route setup (of %d)", out.idx, len(candidates))
			return nrg, out.rules, out.idx, nil
		}
	}
	if lastErr == nil {
		lastErr = ErrNoRouteFound
	}
	return nil, routing.EdgeRules{}, -1, lastErr
}

// fetchCandidateRoutes queries the route finder ONCE and extracts up to k
// candidate bidirectional routes for the race, ranked by (suspect-penalized)
// latency and preferring pairwise intermediate-disjoint candidates. It is a
// best-effort helper: on a finder error, or when fewer than 2 candidates can
// be assembled, it returns what it has (possibly nil) and the caller falls
// back to the full sequential fetchBestRoutes path (which has the local-calc
// fallback and retry machinery). It intentionally does NOT reimplement that
// machinery — the sequential path remains the source of truth for the hard
// cases.
//
// It respects opts.ExcludeIntermediatePKs (hard) and the visor min-hops /
// per-direction / mux constraints exactly as fetchBestRoutes does, and applies
// the same DMSG-multihop rejection.
func (r *router) fetchCandidateRoutes(
	ctx context.Context,
	log *logging.Logger,
	src, dst cipher.PubKey,
	opts *DialOptions,
	baseMinHops uint16,
	keepAlive time.Duration,
	desc routing.RouteDescriptor,
	k int,
) ([]routing.BidirectionalRoute, error) {
	if log == nil {
		log = r.logger
	}
	if opts == nil {
		opts = DefaultDialOptions() // nolint
	}
	if k < 1 {
		k = 1
	}

	forward := [2]cipher.PubKey{src, dst}
	backward := [2]cipher.PubKey{dst, src}

	fwdEff := opts.EffectiveMinHops(true)
	revEff := opts.EffectiveMinHops(false)
	fwdMinHops := baseMinHops
	revMinHops := baseMinHops
	if fwdEff > 0 {
		fwdMinHops = uint16(fwdEff) //nolint:gosec // CLI-bounded small values
	}
	if revEff > 0 {
		revMinHops = uint16(revEff) //nolint:gosec // CLI-bounded small values
	}

	// Request enough candidates to actually have K to race — the mux degree
	// OR k + headroom, whichever is larger.
	num := findRouteNum(opts.EffectiveMuxRoutes(true))
	if rn := findRouteNum(opts.EffectiveMuxRoutes(false)); rn > num {
		num = rn
	}
	wantK := uint16(k + muxRouteHeadroom) //nolint:gosec // k is clamped small
	if wantK > num {
		num = wantK
	}
	if num < baseRouteCandidates {
		num = baseRouteCandidates
	}

	var paths map[routing.PathEdges][][]routing.Hop
	var err error
	if fwdMinHops == revMinHops {
		paths, err = r.conf.RouteFinder.FindRoutes(ctx, []routing.PathEdges{forward, backward},
			&rfclient.RouteOptions{MinHops: fwdMinHops, MaxHops: r.conf.MaxHops, NumRoutes: num})
	} else {
		var fwdPaths, revPaths map[routing.PathEdges][][]routing.Hop
		fwdPaths, err = r.conf.RouteFinder.FindRoutes(ctx, []routing.PathEdges{forward},
			&rfclient.RouteOptions{MinHops: fwdMinHops, MaxHops: r.conf.MaxHops, NumRoutes: num})
		if err == nil {
			revPaths, err = r.conf.RouteFinder.FindRoutes(ctx, []routing.PathEdges{backward},
				&rfclient.RouteOptions{MinHops: revMinHops, MaxHops: r.conf.MaxHops, NumRoutes: num})
		}
		if err == nil {
			paths = make(map[routing.PathEdges][][]routing.Hop, 2)
			for kk, v := range fwdPaths {
				paths[kk] = v
			}
			for kk, v := range revPaths {
				paths[kk] = v
			}
		}
	}
	if err != nil {
		return nil, err
	}

	latencyFor, typeFor, throughputFor := r.buildHopLookups(ctx, paths[forward], paths[backward])
	fwdCands := rejectDMSGMultihop(paths[forward], typeFor)
	revCands := rejectDMSGMultihop(paths[backward], typeFor)

	fwdRanked := r.rankCandidatePaths(fwdCands, src, dst, opts.ExcludeIntermediatePKs, latencyFor, typeFor, throughputFor, k)
	revRanked := r.rankCandidatePaths(revCands, dst, src, opts.ExcludeIntermediatePKs, latencyFor, typeFor, throughputFor, k)
	if len(fwdRanked) == 0 || len(revRanked) == 0 {
		return nil, ErrNoRouteFound
	}

	// Zip forward[i] with reverse[i]; reuse the best reverse when the reverse
	// direction returned fewer candidates. Each pair is a distinct candidate
	// route group to race.
	n := len(fwdRanked)
	out := make([]routing.BidirectionalRoute, 0, n)
	for i := 0; i < n; i++ {
		rev := revRanked[i%len(revRanked)]
		out = append(out, routing.BidirectionalRoute{
			Desc:      desc,
			KeepAlive: keepAlive,
			Forward:   fwdRanked[i],
			Reverse:   rev,
		})
	}
	log.Debugf("assembled %d race candidate(s) to %s (requested k=%d)", len(out), dst, k)
	return out, nil
}

// rankCandidatePaths filters, scores, and returns up to k candidate paths for
// ONE direction. Endpoints for the direction are (from, to). Hard-excluded
// intermediates (opts.ExcludeIntermediatePKs) are dropped. Remaining paths are
// scored by summed per-hop latency PLUS a penalty per currently-suspect
// intermediate, then the lowest-scoring are taken — preferring paths that are
// pairwise intermediate-disjoint from those already chosen, and filling with
// the next-best (possibly overlapping) paths if fewer than k disjoint ones
// exist (more candidates => better odds of converging to a working route).
func (r *router) rankCandidatePaths(
	paths [][]routing.Hop,
	from, to cipher.PubKey,
	hardExclude []cipher.PubKey,
	latencyFor func(uuid.UUID) float64,
	typeFor func(uuid.UUID) string,
	throughputFor func(uuid.UUID) float64,
	k int,
) [][]routing.Hop {
	if len(paths) == 0 || k < 1 {
		return nil
	}
	excludeSet := make(map[cipher.PubKey]struct{}, len(hardExclude))
	for _, pk := range hardExclude {
		excludeSet[pk] = struct{}{}
	}
	now := time.Now()

	type scored struct {
		path  []routing.Hop
		score float64
	}
	acc := make([]scored, 0, len(paths))
	for _, p := range paths {
		if pathTouchesIntermediate(p, excludeSet) {
			continue
		}
		s := pathLatencyScore(p, latencyFor, typeFor, throughputFor)
		s += float64(r.suspects.suspectCount(p, from, to, now)) * suspectHopPenaltyMs
		acc = append(acc, scored{path: p, score: s})
	}
	if len(acc) == 0 {
		return nil
	}
	// Stable sort by ascending score.
	for i := 1; i < len(acc); i++ {
		for j := i; j > 0 && acc[j].score < acc[j-1].score; j-- {
			acc[j], acc[j-1] = acc[j-1], acc[j]
		}
	}

	out := make([][]routing.Hop, 0, k)
	used := make(map[cipher.PubKey]struct{})
	taken := make([]bool, len(acc))
	// Pass 1: greedily take disjoint candidates.
	for i := range acc {
		if len(out) >= k {
			break
		}
		inter := intermediatePKsOfPath(acc[i].path, from, to)
		overlap := false
		for _, pk := range inter {
			if _, hit := used[pk]; hit {
				overlap = true
				break
			}
		}
		if overlap {
			continue
		}
		out = append(out, acc[i].path)
		taken[i] = true
		for _, pk := range inter {
			used[pk] = struct{}{}
		}
	}
	// Pass 2: fill remaining slots with the next-best untaken candidates.
	for i := range acc {
		if len(out) >= k {
			break
		}
		if taken[i] {
			continue
		}
		out = append(out, acc[i].path)
	}
	return out
}
