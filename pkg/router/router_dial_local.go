//go:build !tinygo || (js && wasm)

// Package router pkg/router/router_dial_local.go c2-net-routing
package router

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// buildHopLookups constructs TpID → avg-latency-ms and TpID →
// transport-type lookups over the union of TpIDs appearing in fwd
// and rev path candidates. Sources, in preference order:
//
//  1. Local transport manager — `tm.Transport(id)` for any transport
//     this visor holds. Constant-time map read.
//  2. TPD `GetTransport(tpID)` — entries the local visor doesn't own
//     (intermediate-to-intermediate hops). One round-trip per unique
//     ID.
//
// Both lookups share a single GetTransportByID per ID so callers
// that need both (the dial path: pickDisjointPath's latency rank
// + rejectDMSGMultihop's type filter) don't double the TPD load.
// Before #2899 added the type lookup as a sibling, the dial path
// hit TPD once per hop; sharing brings it back to that level.
//
// typeFor returns "" for IDs the lookup failed for — rejectDMSGMultihop
// treats unknown as "not DMSG" (can't prove it's bad).
func (r *router) buildHopLookups(ctx context.Context, fwd, rev [][]routing.Hop) (latencyFor func(uuid.UUID) float64, typeFor func(uuid.UUID) string, throughputFor func(uuid.UUID) float64) {
	unique := make(map[uuid.UUID]struct{})
	for _, paths := range [][][]routing.Hop{fwd, rev} {
		for _, p := range paths {
			for _, h := range p {
				unique[h.TpID] = struct{}{}
			}
		}
	}
	latencyCache := make(map[uuid.UUID]float64, len(unique))
	typeCache := make(map[uuid.UUID]string, len(unique))
	throughputCache := make(map[uuid.UUID]float64, len(unique))

	var misses []uuid.UUID
	for id := range unique {
		// Local transport first — constant-time map read, with the
		// freshest latency stats this visor holds.
		if r.tm != nil {
			if tp := r.tm.Transport(id); tp != nil {
				if stats := tp.GetLatencyStats(); stats.Avg > 0 {
					latencyCache[id] = stats.Avg
				}
				typeCache[id] = string(tp.Entry.Type)
				if bps := tp.GetThroughputBps(); bps > 0 {
					throughputCache[id] = bps
				}
				// A local transport whose ping/pong has not landed a sample yet
				// (Avg == 0, e.g. freshly created, or the peer answers pings
				// late) used to end the lookup here and stay unknown forever,
				// even though TPD carries a measured latency_ms for that very
				// entry. The first hop of a diversify candidate is ALWAYS a
				// local transport, so that gap blanked the one signal the
				// first-hop ranking runs on. Fall through to the snapshot when
				// we have no local number; the local value still wins when we
				// do (the snapshot only fills empty cache slots below).
				if _, known := latencyCache[id]; known {
					continue
				}
			}
		}
		misses = append(misses, id)
	}

	// Resolve non-local hops (intermediate-to-intermediate) from ONE
	// cached GetAllTransports snapshot rather than a GetTransportByID
	// per ID. A multiplexed multi-hop dial touches 12+ unique IDs ×
	// retries; per-ID fetches blow through TPD's 30-req/min limit, so
	// every lookup 429s, buildHopLookups degrades to empty latency/
	// type, and the route group can't rank/filter → never assembles.
	// The bulk snapshot is a single fetch (cached, see tpd_cache.go),
	// so the same data costs one round-trip per TTL instead of one
	// per hop per dial.
	if len(misses) > 0 && r.tm != nil && r.tm.Conf != nil && r.tm.Conf.DiscoveryClient != nil && r.tpdCache != nil {
		snap, err := r.tpdCache.snapshot(ctx, r.tm.Conf.DiscoveryClient.GetAllTransports, versionProbe(r.tm.Conf.DiscoveryClient))
		if err != nil {
			// snap is non-nil when a prior snapshot was served stale;
			// only a cold-cache failure yields nil. Either way, log
			// and proceed — unknown hops degrade gracefully (latency 0,
			// type "" which rejectDMSGMultihop treats as "not DMSG").
			r.logger.WithError(err).Debug("buildHopLookups: transport snapshot refresh failed; using local + any stale data")
		}
		if snap != nil {
			for _, id := range misses {
				entry := snap.byID[id]
				if entry == nil {
					continue
				}
				// Fill only what the local pass could not: a locally-held
				// transport reaches this loop when it has no latency sample of
				// its own, and its own type / throughput readings are fresher
				// than TPD's.
				if _, known := latencyCache[id]; !known && entry.Latency > 0 {
					latencyCache[id] = entry.Latency
				}
				if typeCache[id] == "" {
					typeCache[id] = string(entry.Type)
				}
				if _, known := throughputCache[id]; !known && entry.ThroughputBps > 0 {
					throughputCache[id] = entry.ThroughputBps
				}
			}
		}
	}

	return func(id uuid.UUID) float64 { return latencyCache[id] },
		func(id uuid.UUID) string { return typeCache[id] },
		func(id uuid.UUID) float64 { return throughputCache[id] }
}

// directRoute returns a 1-hop route over an existing OPEN local transport to dst,
// or ok=false if none exists. It is the cheap in-memory half of
// calculateLocalRoutes (the direct probe) WITHOUT the GetAllTransports() bulk
// fetch the multihop BFS needs. The race path in DialRoutes uses it to prefer a
// freshly-created direct transport the instant it lands, instead of either
// waiting out the route-finder timeout OR prematurely pulling (and blocking on)
// the full TPD dataset for a multihop calc at cold start — when that dataset is
// exactly what isn't warm yet. When several direct transports to dst exist the
// most-preferred type wins (STCPR > SUDPH > STCP > … > DMSG).
func (r *router) directRoute(src, dst cipher.PubKey) (fwd, rev []routing.Hop, ok bool) {
	if r.tm == nil {
		return nil, nil, false
	}
	type cand struct {
		id     uuid.UUID
		tpType string
	}
	var cands []cand
	r.tm.WalkTransports(func(tp *transport.ManagedTransport) bool {
		// Mirror calculateLocalRoutes' direct probe: skip closed / pong-dead legs
		// and setup-labeled (RSN control-plane) transports, and match dst.
		if tp == nil || tp.IsClosed() || tp.Entry.Label == transport.LabelSetup {
			return true
		}
		if tp.Entry.RemoteEdge(src) == dst {
			cands = append(cands, cand{id: tp.Entry.ID, tpType: string(tp.Entry.Type)})
		}
		return true
	})
	if len(cands) == 0 {
		return nil, nil, false
	}
	sort.SliceStable(cands, func(i, j int) bool {
		return tptypes.TypePreference(tptypes.Type(cands[i].tpType)) <
			tptypes.TypePreference(tptypes.Type(cands[j].tpType))
	})
	id := cands[0].id
	return []routing.Hop{{TpID: id, From: src, To: dst}}, []routing.Hop{{TpID: id, From: dst, To: src}}, true
}

// directRoutes returns EVERY live direct 1-hop route to dst, best first. It is
// directRoute's multi-candidate form: directRoute answers "the" direct route
// (type-preference winner) and its caller can then only accept or reject that
// one, so a diversify dial whose sibling holds it conceded the hook race even
// when this visor had a SECOND, free direct transport to the same exit. The
// order is the shared first-hop rule (rankByPathLatency): measured latency
// lowest first, unmeasured last, with the transport-type preference it inherits
// as the stable tiebreak. Each hop carries its own measured latency
// (tp.GetLatency(), ms — the field a leg snapshot reports as latency_ms), so the
// ranking needs no lookup.
func (r *router) directRoutes(src, dst cipher.PubKey) [][]routing.Hop {
	if r.tm == nil {
		return nil
	}
	type cand struct {
		id        uuid.UUID
		tpType    string
		latencyMs float64
	}
	var cands []cand
	r.tm.WalkTransports(func(tp *transport.ManagedTransport) bool {
		if tp == nil || tp.IsClosed() || tp.Entry.Label == transport.LabelSetup {
			return true
		}
		if tp.Entry.RemoteEdge(src) == dst {
			cands = append(cands, cand{id: tp.Entry.ID, tpType: string(tp.Entry.Type), latencyMs: tp.GetLatency()})
		}
		return true
	})
	if len(cands) == 0 {
		return nil
	}
	sort.SliceStable(cands, func(i, j int) bool {
		return tptypes.TypePreference(tptypes.Type(cands[i].tpType)) <
			tptypes.TypePreference(tptypes.Type(cands[j].tpType))
	})
	out := make([][]routing.Hop, 0, len(cands))
	for _, c := range cands {
		out = append(out, []routing.Hop{{TpID: c.id, From: src, To: dst, Latency: c.latencyMs}})
	}
	return rankByPathLatency(out, nil)
}

// calculateLocalRoutes attempts to calculate routes locally using the transport manager
// and transport discovery data, without relying on the route finder service.
// Supports 1-hop (direct), self-ping (src == dst), and N-hop routes via BFS over
// the local transport graph (bounded by Config.MaxHops). MinHops/ExcludeIntermediatePKs
// from DialOptions are honored — set MinHops > 1 to skip direct, and populate
// ExcludeIntermediatePKs to enforce DisjointMux. BFS expansion ordering is
// deterministic (sorted by remote-PK string) so the same inputs yield the same path.
func (r *router) calculateLocalRoutes(ctx context.Context, log *logging.Logger, src, dst cipher.PubKey, opts *DialOptions) (fwd, rev []routing.Hop, err error) {
	if log == nil {
		log = r.logger
	}
	dialOpts := opts
	if r.tm == nil {
		return nil, nil, errors.New("transport manager not available")
	}

	dc := r.tm.Conf.DiscoveryClient
	if dc == nil {
		return nil, nil, errors.New("discovery client not available")
	}

	isSelfPing := src == dst
	log.Debugf("Calculating route locally from %s to %s (self-ping=%v)", src, dst, isSelfPing)

	// Collect local transports
	var localTps []localTpRef

	r.tm.WalkTransports(func(tp *transport.ManagedTransport) bool {
		if tp == nil {
			return true
		}
		// Skip closed / black-holing first-hop transports. A transport pruned by
		// pong-liveness is closed+deregistered (see managed_transport), so
		// IsClosed() catches both explicitly-closed and silently-dead legs — we
		// must not build a route out over one. This is the local-calc analog
		// of the per-leg liveness probe: don't originate a leg on a dead edge.
		if tp.IsClosed() {
			return true
		}
		// Exclude "setup" labeled transports — those are for RSN control-plane
		// traffic only and must not be used as hops in data routes.
		if tp.Entry.Label == transport.LabelSetup {
			return true
		}
		localTps = append(localTps, localTpRef{
			id:       tp.Entry.ID,
			remotePK: tp.Entry.RemoteEdge(src),
			tpType:   string(tp.Entry.Type),
		})
		return true
	})

	if len(localTps) == 0 {
		return nil, nil, errors.New("no local transports available")
	}

	// Sort local transports by type preference so direct types (STCPR > SUDPH > STCP)
	// are tried before DMSG. WalkTransports iteration order is undefined.
	sort.Stable(localTpsByTypePref(localTps))

	log.Debugf("Found %d local transports", len(localTps))

	// The first-hop exclusions this dial is JUDGED against, applied here where
	// the first hop is chosen.
	//
	// The caller re-checks whatever this returns with firstHopExcluded and
	// throws the path away when it leaves over a hop a sibling tunnel already
	// holds. A local calc that ignores the exclusions therefore does not merely
	// waste a BFS — it returns the SAME refused path on every dial of a pool
	// fill, so the pool never discovers the first hops it has not used yet and
	// settles at a handful of tunnels over one transport.
	//
	// ExcludeTransportIDs was already honored, but only for the direct probe
	// below. These extend it to the PEER and its IP — one host answers on
	// stcpr, squicr and sudph alike, and all three ride the same link — and to
	// the multi-hop BFS.
	var (
		exclTpID  map[uuid.UUID]struct{}
		exclPeer  map[cipher.PubKey]struct{}
		exclHopIP map[string]struct{}
	)
	if dialOpts != nil {
		if len(dialOpts.ExcludeTransportIDs) > 0 {
			exclTpID = make(map[uuid.UUID]struct{}, len(dialOpts.ExcludeTransportIDs))
			for _, id := range dialOpts.ExcludeTransportIDs {
				exclTpID[id] = struct{}{}
			}
		}
		if len(dialOpts.ExcludeFirstHopPeers) > 0 {
			exclPeer = make(map[cipher.PubKey]struct{}, len(dialOpts.ExcludeFirstHopPeers))
			for _, pk := range dialOpts.ExcludeFirstHopPeers {
				exclPeer[pk] = struct{}{}
			}
		}
		if len(dialOpts.ExcludeFirstHopIPs) > 0 {
			exclHopIP = make(map[string]struct{}, len(dialOpts.ExcludeFirstHopIPs))
			for _, ip := range dialOpts.ExcludeFirstHopIPs {
				exclHopIP[ip] = struct{}{}
			}
		}
	}
	hasFirstHopExclusions := len(exclTpID) > 0 || len(exclPeer) > 0 || len(exclHopIP) > 0
	// localFirstHopHeld reports whether a sibling tunnel already leaves over
	// this local transport — by ID, by peer, or by the peer's IP.
	localFirstHopHeld := func(tp localTpRef) bool {
		if _, bad := exclTpID[tp.id]; bad {
			return true
		}
		if _, bad := exclPeer[tp.remotePK]; bad {
			return true
		}
		if len(exclHopIP) > 0 && r.tm != nil {
			if mt := r.tm.Transport(tp.id); mt != nil {
				if ip := mt.RemoteIP(); ip != "" {
					if _, bad := exclHopIP[ip]; bad {
						return true
					}
				}
			}
		}
		return false
	}

	// Skip the direct (1-hop) probe when the caller asked for
	// MinHops >= 2 — they want a non-direct path. Use the MAX of
	// per-direction MinHops since local-BFS mirrors forward to reverse
	// (reverseHops) — both directions share one path here, so we must
	// satisfy whichever direction has the higher constraint. Callers
	// that need genuinely asymmetric local paths must use the
	// route-finder service (fetchBestRoutes path) which queries each
	// direction independently.
	// The visor-global setting is part of the constraint, not a default that
	// the absence of per-dial options overrides. This is the fallback the
	// route finder's failure lands in, so reading only dialOpts made it the
	// place a min_hops=3 dial quietly became a direct one: the finder refused
	// to serve 3 hops, and the fallback — seeing opts.MinHops == 0 — decided
	// direct was fine.
	bfsMinHops := int(r.EffectiveMinHops(dialOpts))
	allowDirect := bfsMinHops <= 1

	// Check for direct (1-hop) route first
	for _, tp := range localTps {
		if !allowDirect {
			break
		}
		if tp.remotePK == dst {
			// Skip DMSG transports for mux (DMSG is a relay, not suitable for multiplexing)
			if dialOpts != nil && dialOpts.ExcludeDMSG && tp.tpType == "dmsg" {
				log.Debugf("Skipping DMSG transport %s (excluded for mux)", tp.id)
				continue
			}
			// Skip a first hop a sibling tunnel already holds — by transport ID
			// (used by mux to get different transports), by peer, or by IP. On
			// the direct route the first hop IS the destination, so a pool that
			// already holds the direct tunnel excludes the exit as a peer and
			// this is the check that stops the fill cloning it.
			if localFirstHopHeld(tp) {
				log.Debugf("Skipping excluded transport %s to destination", tp.id)
				dialOpts.note("local: skipped excluded direct tp %s", tp.id.String()[:8])
				continue
			}
			log.Debugf("Found direct transport to destination: %s (type=%s)", tp.id, tp.tpType)
			dialOpts.note("local: direct tp %s (%s)", tp.id.String()[:8], tp.tpType)
			fwdHop := routing.Hop{TpID: tp.id, From: src, To: dst}
			revHop := routing.Hop{TpID: tp.id, From: dst, To: src}
			return []routing.Hop{fwdHop}, []routing.Hop{revHop}, nil
		}
	}

	// Build transport cache from a single GetAllTransports() snapshot,
	// shared with buildHopLookups via r.tpdCache (5m TTL). This both
	// replaces N individual GetTransportsByEdge calls with one bulk
	// fetch AND amortizes that fetch across dials so repeated local
	// route calculations don't re-pull the whole dataset each time.
	// The by-edge and per-TpID metric lookups are derived ONCE per TPD
	// snapshot (see tpdSnapshot.deriveTransportLookups) rather than rebuilt
	// from the whole ~16k-entry set on every call. On a NAT'd visor with no
	// direct transport to dst the early-return above is skipped, so a browse
	// dial retry loop used to pay four full-dataset map builds + a per-edge
	// sort per call — enough to peg the single js/wasm thread in GC. When
	// there is no cache (tpdCache == nil, tests / bare router) we derive the
	// same lookups locally from a direct fetch.
	var (
		allEntries       []*transport.Entry
		transportsByEdge map[cipher.PubKey][]*transport.Entry
		tpLatencyMs      map[uuid.UUID]float64
		tpTypeOf         map[uuid.UUID]string
		tpThroughput     map[uuid.UUID]float64
		snapGen          uint64 // TPD-snapshot generation, for the local-route memo (zero on the no-cache path)
	)
	if r.tpdCache != nil {
		snap, serr := r.tpdCache.snapshot(ctx, dc.GetAllTransports, versionProbe(dc))
		if snap == nil {
			// Cold-cache failure only — a stale snapshot would be
			// returned non-nil. Nothing to compute routes from.
			log.WithError(serr).Warn("Failed to fetch all transports for route calculation")
			return nil, nil, fmt.Errorf("failed to fetch transport discovery data: %w", serr)
		}
		allEntries = snap.entries
		transportsByEdge = snap.byEdge
		tpLatencyMs = snap.latencyByID
		tpTypeOf = snap.typeByID
		tpThroughput = snap.throughputByID
		snapGen = snap.gen
	} else {
		allEntries, err = dc.GetAllTransports(ctx)
		if err != nil {
			log.WithError(err).Warn("Failed to fetch all transports for route calculation")
			return nil, nil, fmt.Errorf("failed to fetch transport discovery data: %w", err)
		}
		transportsByEdge, tpLatencyMs, tpTypeOf, tpThroughput = deriveTransportLookups(allEntries)
	}
	log.Debugf("Built transport cache with %d entries covering %d visors", len(allEntries), len(transportsByEdge))

	// For self-ping, try 2-hop route through any available transport partner
	// This allows testing the full route setup even without a direct self-transport
	if isSelfPing {
		log.Debug("Self-ping: looking for 2-hop loopback route through transport partner")
		for _, tp := range localTps {
			intermediatePK := tp.remotePK
			if intermediatePK == src {
				// Skip actual self-transports (already checked above)
				continue
			}

			// For self-ping via 2-hop: src -> intermediate -> src
			// We need the intermediate to have a transport back to us
			intermediateEntries := transportsByEdge[intermediatePK]
			if len(intermediateEntries) == 0 {
				continue
			}

			for _, entry := range intermediateEntries {
				if entry == nil {
					continue
				}
				remotePK := entry.RemoteEdge(intermediatePK)
				if remotePK == src {
					log.Debugf("Found 2-hop self-ping route via %s (tp1=%s, tp2=%s)", intermediatePK, tp.id, entry.ID)
					// Build loopback route: src -> intermediate -> src
					fwdHop1 := routing.Hop{TpID: tp.id, From: src, To: intermediatePK}
					fwdHop2 := routing.Hop{TpID: entry.ID, From: intermediatePK, To: src}

					// Reverse is the same for self-ping
					revHop1 := routing.Hop{TpID: entry.ID, From: src, To: intermediatePK}
					revHop2 := routing.Hop{TpID: tp.id, From: intermediatePK, To: src}

					return []routing.Hop{fwdHop1, fwdHop2}, []routing.Hop{revHop1, revHop2}, nil
				}
			}
		}
		return nil, nil, errors.New("self-ping: no 2-hop loopback route found through transport partners")
	}

	// Build the ExcludeIntermediatePKs lookup set once outside the
	// hot 2-hop loop. Empty set when DisjointMux is off (the common
	// single-route case), so the per-candidate check is one map miss.
	var excludeIntermediates map[cipher.PubKey]struct{}
	if dialOpts != nil && len(dialOpts.ExcludeIntermediatePKs) > 0 {
		excludeIntermediates = make(map[cipher.PubKey]struct{}, len(dialOpts.ExcludeIntermediatePKs))
		for _, pk := range dialOpts.ExcludeIntermediatePKs {
			excludeIntermediates[pk] = struct{}{}
		}
	}

	// BFS over the local transport graph to find a path whose length
	// is within [minHops, maxHops]. Replaces the previous "1-hop or
	// 2-hop only" logic so the local-route fast path can serve
	// deterministic multi-hop runs (mux-bw --local-route --min-hops 3+
	// for hop-by-hop measurement work). The BFS visits levels in
	// increasing order of hop count and returns the FIRST path
	// satisfying minHops, so paths returned are always shortest-
	// acceptable. Determinism: at each expansion the children are
	// sorted by intermediate-PK string before enqueueing, so the
	// same (src, dst, minHops, maxHops, transport graph) tuple
	// always yields the same path.
	// bfsMinHops (computed above) already resolves the per-dial overrides
	// against the visor-global setting, so it is the constraint. Local-BFS
	// mirrors forward→reverse so both directions share one path; satisfying
	// the higher constraint ensures the stricter direction is honored. The
	// 2 is only for a router with routing effectively unconfigured — this
	// branch is multi-hop search, so one hop is not an answer it can give.
	minHops := 2
	if bfsMinHops > 0 {
		minHops = bfsMinHops
	}
	// MaxHops is taken from the router-wide config (DialOptions
	// doesn't currently expose a per-call ceiling). Default to 7 if
	// somehow zero — bounds the BFS so it can't run away over a
	// large transport graph.
	maxHops := int(r.conf.MaxHops)
	if maxHops == 0 {
		maxHops = 7
	}

	// Local-route memo: the multi-hop BFS below is a pure function of (src, dst,
	// minHops, maxHops, the TPD snapshot, the local first-hop set), so cache it —
	// skysocks-lite's warm/keepalive sweep and the routing UI re-request the same
	// route far faster than the graph changes, and re-running the whole BFS pegs the
	// single js/wasm thread. Only the plain case (no DisjointMux intermediate
	// exclusions) is memoized; the mux path varies the exclusions per leg on purpose
	// and must always recompute.
	//
	// The generation is snapGen, a counter the snapshot cache bumps on every
	// rebuild — NOT the CXO sync timestamp it used to be. That timestamp is the
	// zero time on the cache's TTL path (a discovery client with no CXO version
	// signal, or a CXO feed that hasn't primed), and the memo used to require it
	// to be non-zero, so on exactly the visor this cache exists for — the wasm
	// visor, whose big tpd-all-transports feed frequently never primes — the memo
	// was unconditionally disabled and every dial re-ran the whole BFS.
	var (
		memoKey localRouteKey
		// Transport-ID exclusions shape the answer too (a diversify dial's first-hop
		// exclusions), so a dial carrying them must not be served another dial's path.
		memoEnabled = len(excludeIntermediates) == 0 && !hasFirstHopExclusions && r.localRoutes != nil && snapGen != 0
		localSig    uint64
	)
	if memoEnabled {
		localSig = localTpSignature(localTps)
		memoKey = localRouteKey{src: src, dst: dst, min: minHops, max: maxHops}
		if fwd, rev, found, ok := r.localRoutes.get(snapGen, localSig, memoKey); ok {
			if !found {
				log.Debugf("Local-route memo hit (no path) %s→%s (min=%d max=%d)", src, dst, minHops, maxHops)
				return nil, nil, fmt.Errorf("local BFS found no path to %s with min_hops=%d max_hops=%d", dst, minHops, maxHops)
			}
			log.Debugf("Local-route memo hit %s→%s (min=%d max=%d)", src, dst, minHops, maxHops)
			dialOpts.note("local: memo hit, first hops [%s]", firstHopsOf([][]routing.Hop{fwd}))
			return fwd, rev, nil
		}
	}

	// The BFS starts from this visor's own transports, so a held first hop is
	// not a starting point. Falling back to the unfiltered set when every hop
	// is held keeps the documented soft-preference behavior: a dial with
	// nowhere else to go still gets an answer, and the caller's own gate
	// decides whether to use it.
	bfsTps := localTps
	if hasFirstHopExclusions {
		keep := make([]localTpRef, 0, len(localTps))
		for _, tp := range localTps {
			if !localFirstHopHeld(tp) {
				keep = append(keep, tp)
			}
		}
		if len(keep) > 0 {
			bfsTps = keep
			dialOpts.note("local: %d of %d first hops free after exclusions", len(keep), len(localTps))
		} else {
			dialOpts.note("local: all %d first hops held; searching them anyway", len(localTps))
		}
	}

	best, level, found := localRouteBFS(src, dst, bfsTps, localBFSGraph{
		byEdge:         transportsByEdge,
		latencyByID:    tpLatencyMs,
		typeByID:       tpTypeOf,
		throughputByID: tpThroughput,
	}, excludeIntermediates, minHops, maxHops)
	if !found {
		// Cache the miss too. A search that finds nothing is the EXPENSIVE
		// case — it exhausts the graph to maxHops instead of returning at the
		// first level that reaches dst — and it is the common one on a visor
		// with no path to the destination, where the caller retries. Leaving
		// misses uncached meant the memo never covered the calls that cost
		// the most.
		if memoEnabled {
			r.localRoutes.putMiss(snapGen, localSig, memoKey)
		}
		return nil, nil, fmt.Errorf("local BFS found no path to %s with min_hops=%d max_hops=%d", dst, minHops, maxHops)
	}
	log.Debugf("Local BFS found %d-hop route via %v", level, hopPath(best))
	dialOpts.note("local: %d-hop BFS path, first hops [%s]", level, firstHopsOf([][]routing.Hop{best}))
	revPath := reverseHops(best)
	if memoEnabled {
		r.localRoutes.put(snapGen, localSig, memoKey, best, revPath)
	}
	return best, revPath, nil
}

// localRouteMemo caches calculateLocalRoutes' multi-hop BFS result. That BFS over
// the (up to ~16k-edge) TPD graph is the single most expensive thing the wasm
// visor's route-setup does, and it re-runs on EVERY dial/route-setup — skysocks-
// lite's warm/keepalive sweep and the routing UI re-request the same (src,dst) far
// faster than the graph changes, so the same BFS is recomputed over and over. On
// the single js/wasm thread that pegs mallocgc/GC and starves the very route-setup
// handshake it feeds (which then times out at ~28s, forcing exclusions that drive
// yet more local calc — a feedback loop). The BFS result is a pure function of
// (src, dst, min/max hops, the TPD snapshot, the local first-hop set), so it is
// safe to memoize on all of those. Single-generation: the whole map resets when
// the TPD snapshot version OR the local-transport signature changes, so a stale
// route is never served and the map stays bounded to one graph's worth of entries.
// Only the plain (no intermediate-exclusion) case is cached — the mux path varies
// ExcludeIntermediatePKs per leg on purpose and must always recompute.
type localRouteMemo struct {
	mu       sync.Mutex
	gen      uint64
	localSig uint64
	m        map[localRouteKey]localRoutePair
}

type localRouteKey struct {
	src, dst cipher.PubKey
	min, max int
}

// localRoutePair is one memoized answer. found=false records a searched-and-
// empty result — the "no path" case, which costs MORE to compute than a hit
// (it exhausts the graph rather than stopping at the first level that reaches
// dst) and so is the one most worth caching.
type localRoutePair struct {
	fwd, rev []routing.Hop
	found    bool
}

func newLocalRouteMemo() *localRouteMemo { return &localRouteMemo{} }

// get returns the cached answer for k when the memo's generation matches the
// current snapshot generation + local signature. ok=false means "not cached,
// recompute"; ok=true with found=false means "cached: this search finds no
// path".
func (c *localRouteMemo) get(gen, sig uint64, k localRouteKey) (fwd, rev []routing.Hop, found, ok bool) {
	if c == nil {
		return nil, nil, false, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || c.gen != gen || c.localSig != sig {
		return nil, nil, false, false
	}
	p, hit := c.m[k]
	if !hit {
		return nil, nil, false, false
	}
	if !p.found {
		return nil, nil, false, true
	}
	return append([]routing.Hop(nil), p.fwd...), append([]routing.Hop(nil), p.rev...), true, true
}

// put stores (fwd, rev) copies for k, resetting the whole generation first when the
// snapshot generation or local signature moved (keeps the map fresh and bounded).
func (c *localRouteMemo) put(gen, sig uint64, k localRouteKey, fwd, rev []routing.Hop) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reseatLocked(gen, sig)
	c.m[k] = localRoutePair{
		fwd:   append([]routing.Hop(nil), fwd...),
		rev:   append([]routing.Hop(nil), rev...),
		found: true,
	}
}

// putMiss records that k has no acceptable path in this generation.
func (c *localRouteMemo) putMiss(gen, sig uint64, k localRouteKey) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reseatLocked(gen, sig)
	c.m[k] = localRoutePair{}
}

// reseatLocked drops the whole map when the generation moved, so a stale route
// is never served and the map stays bounded to one graph's worth of entries.
func (c *localRouteMemo) reseatLocked(gen, sig uint64) {
	if c.m == nil || c.gen != gen || c.localSig != sig {
		c.gen = gen
		c.localSig = sig
		c.m = make(map[localRouteKey]localRoutePair)
	}
}

// reverseHops builds the reverse path of a forward route by walking
// the forward hops in reverse order and swapping From/To on each.
// Used by calculateLocalRoutes since the local BFS only produces the
// forward direction.
func reverseHops(fwd []routing.Hop) []routing.Hop {
	rev := make([]routing.Hop, len(fwd))
	for i, h := range fwd {
		rev[len(fwd)-1-i] = routing.Hop{
			TpID: h.TpID,
			From: h.To,
			To:   h.From,
		}
	}
	return rev
}

// hopPath renders a hops list as "pk1→pk2→…→pkN" for log lines.
// Used to make BFS-found routes inspectable in the visor journal.
func hopPath(path []routing.Hop) string {
	if len(path) == 0 {
		return ""
	}
	out := path[0].From.String()
	for _, h := range path {
		out += "→" + h.To.String()
	}
	return out
}

// directHop returns a 1-hop route over a live transport to dst, if this visor
// already holds one.
//
// Setup transports are excluded: they carry route-setup traffic, not app data.
// Closed transports are skipped so a dead leg is never turned into a route —
// falling through to the route finder is the right answer there, not dialing
// over something known to be down.
func (r *router) directHop(src, dst cipher.PubKey) (routing.Hop, bool) {
	return r.directHopExcluding(src, dst, nil)
}

// directHopExcluding is directHop with the transports in exclude ruled out, so a
// direct mux group under leg.hops_match can take the NEXT direct transport to
// the exit (squicr beside stcpr) for its second leg.
func (r *router) directHopExcluding(src, dst cipher.PubKey, exclude []uuid.UUID) (routing.Hop, bool) {
	if r.tm == nil {
		return routing.Hop{}, false
	}
	// Every live transport to dst, then the best of them. This used to take the
	// first one the walk produced — map order — so with stcpr, squicr and sudph
	// all up to the same exit a --direct dial landed on whichever came out
	// first: measured live, the same proxy did 4 MB/s on stcpr and 1 Mbit/s
	// with gaps on sudph, restart to restart, from the same three transports.
	var best *transport.ManagedTransport
	r.tm.WalkTransports(func(tp *transport.ManagedTransport) bool {
		if tp == nil || tp.IsClosed() || tp.Entry.Label == transport.LabelSetup {
			return true
		}
		if tp.Entry.RemoteEdge(src) != dst || slices.Contains(exclude, tp.Entry.ID) {
			return true
		}
		if best == nil || betterDirectTransport(tp, best) {
			best = tp
		}
		return true
	})
	if best == nil {
		return routing.Hop{}, false
	}
	return routing.Hop{TpID: best.Entry.ID, From: src, To: dst}, true
}

// betterDirectTransport orders two live transports to the same peer for a
// direct route: the configured type preference first (stcpr > quic > sudph …,
// the same order every other route decision uses), then, within a type, the
// higher measured throughput, then the lower measured latency. Unmeasured
// values (zero) never beat measured ones.
func betterDirectTransport(a, b *transport.ManagedTransport) bool {
	pa, pb := tptypes.TypePreference(a.Entry.Type), tptypes.TypePreference(b.Entry.Type)
	if pa != pb {
		return pa < pb
	}
	ta, tb := a.GetThroughputBps(), b.GetThroughputBps()
	if ta != tb {
		return ta > tb
	}
	la, lb := a.GetLatency(), b.GetLatency()
	if la != lb {
		if la <= 0 {
			return false
		}
		if lb <= 0 {
			return true
		}
		return la < lb
	}
	return false
}

// shortTpIDs renders transport IDs by their first 8 characters, for the dial
// decision trail.
func shortTpIDs(ids []uuid.UUID) string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String()[:8])
	}
	return strings.Join(out, ",")
}

// firstHopsOf renders each candidate path's first-hop transport (8 chars) for
// the dial decision trail.
func firstHopsOf(paths [][]routing.Hop) string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if len(p) == 0 {
			out = append(out, "-")
			continue
		}
		out = append(out, p[0].TpID.String()[:8])
	}
	return strings.Join(out, ",")
}
