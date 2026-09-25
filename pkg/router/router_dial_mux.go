//go:build !tinygo || (js && wasm)

// Package router pkg/router/router_dial_mux.go c2-net-routing
package router

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// establishMuxRoutes attempts to establish additional parallel routes for a mux-enabled
// route group. Called after the primary route is established in DialRoutes.
//
// Supports asymmetric mux counts: ForwardMuxRoutes / ReverseMuxRoutes
// override the symmetric MuxRoutes for that direction (see
// DialOptions.EffectiveMuxRoutes). The loop runs max(fwd, rev) - 1
// iterations; per iteration, the aux route's forward leg is appended
// only if i < fwdCount and the reverse leg only if i < revCount. The
// unused direction's rule is deleted from the routing table to avoid
// stale-entry leaks. The most useful case is fwd=1 + rev=N for
// download-heavy workloads (1 forward upstream + N reverse legs that
// aggregate the bulk payload).
// initialForegroundMux bounds how many mux legs establishMuxRoutes sets up
// SYNCHRONOUSLY at dial time. The standby pool is uncapped (adaptStandbyMax=512),
// but dialing hundreds of legs at connect would storm the setup node and stall
// the connection before it serves; so the foreground dial builds a lean mux (a
// few active + a small warm reserve) and returns, and the background self-heal
// fills the rest of the disjoint pool one leg at a time (see SetSelfHeal /
// maybeSelfHeal). Chosen well above the adaptive active cap (adaptCap=8) so a
// download can grow onto warm legs immediately, but small enough that the
// initial dial is a handful of parallel setups, not a storm.
const initialForegroundMux = 16

func (r *router) establishMuxRoutes(
	ctx context.Context,
	nrg *NoiseRouteGroup,
	opts *DialOptions,
	forwardDesc routing.RouteDescriptor,
	primaryTpID uuid.UUID,
) {
	log := r.scopedLogForOpts(opts, forwardDesc.SrcPort())
	fwdCount := 1
	revCount := 1
	if opts != nil {
		if eff := opts.EffectiveMuxRoutes(true); eff > 1 {
			fwdCount = eff
		}
		if eff := opts.EffectiveMuxRoutes(false); eff > 1 {
			revCount = eff
		}
	}
	maxCount := fwdCount
	if revCount > maxCount {
		maxCount = revCount
	}
	// Cap the FOREGROUND initial dial. With the standby pool uncapped
	// (adaptStandbyMax=512 → Mux ~513), planning+dialing every leg here — at
	// connect time, Phase-2 in parallel — would be a setup-node dial storm that
	// stalls the connection before it serves a byte. Instead set up a lean mux
	// now (a few active + a small warm reserve) so the dial returns fast, and let
	// the BACKGROUND self-heal (SetSelfHeal target = full Mux) fill the rest of
	// the disjoint pool one leg at a time. The reverse/forward split is preserved
	// below; this only bounds how many legs are attempted synchronously.
	if fg := DialForegroundMux(); maxCount > fg {
		maxCount = fg
	}
	if maxCount <= 1 || nrg.rg.mux == nil {
		return
	}

	// Don't multiplex when the primary route contains a DMSG transport.
	// DMSG hops share an unaccountable dmsg server intermediary, so multiplexing
	// alongside another DMSG-bearing route risks data looping through the same
	// dmsg server with no way to detect it.
	nrg.rg.mu.Lock()
	for _, tp := range nrg.rg.tps {
		if tp != nil && tp.Entry.Type == tptypes.DMSG {
			nrg.rg.mu.Unlock()
			log.Debug("Skipping mux setup: primary route contains a DMSG transport")
			return
		}
	}
	nrg.rg.mu.Unlock()

	lPK := forwardDesc.SrcPK()
	rPK := forwardDesc.DstPK()
	excludeIDs := []uuid.UUID{primaryTpID}

	// Far-end transports the group's live legs (here: the primary) already
	// occupy. See DialOptions.ExcludeRemoteTransportIDs — the destination
	// refuses a leg whose reverse path leaves it over one of these, so the
	// exclude set accumulates each planned leg's Reverse[0] exactly the way
	// excludeIDs accumulates its Forward[0].
	excludeRemoteIDs := nrg.rg.remoteLegTransportIDs()

	// Disjoint-intermediate routing is the default for multiplexed
	// routes: routes 2..N must NOT share intermediate visors with
	// any earlier route (primary or aux). Operators who want
	// overlapping intermediates must dial with explicit
	// ForwardHops/ReverseHops, which bypasses this path entirely.
	//
	// Seed with the primary route's intermediates so the first aux
	// route picks a disjoint path; accumulate each successful aux
	// route's intermediates into the same exclude set so subsequent
	// aux routes diverge from ALL prior ones.
	excludePKs := intermediatesOfRouteGroup(nrg, lPK, rPK)

	// Also exclude same-LAN peers as aux-leg intermediates. A leg that hops
	// through a peer on our OWN local network (a private/RFC1918 endpoint, or —
	// the NAT-hairpin case — our own public IP) provides ZERO path diversity:
	// same first-mile link, same NAT, same failure domain. Worse, such a peer
	// usually has the lowest latency (it's local), so the route-finder ranks it
	// first and the rotation loop re-selects that same useless leg every tick,
	// starving genuinely-diverse aux legs. Applying this only to aux legs (never
	// the primary) keeps connectivity unchanged — the primary may still route
	// through a same-LAN peer if that is the only path.
	excludePKs = appendUniquePKs(excludePKs, r.sameLANExcludedPKs())

	// Thread MinHops + AppName from the parent dial through to each
	// aux route. Without MinHops, fetchBestRoutes for the aux dial
	// drops back to r.conf.MinHops (usually 1) and the route-finder
	// happily returns the direct transport — so a `--routes N
	// --min-hops 2` call would establish route 0 multi-hop but routes
	// 1..N-1 over the direct stcpr, defeating the disjoint-mux intent.
	// Per-direction MinHops (Forward/ReverseMinHops) is threaded too
	// so asymmetric mux dials honor the same per-direction constraints
	// the parent set. AppName is threaded for log-attribution
	// consistency across the aux routes (the scoped logger keys on
	// app name).
	parentMinHops := 0
	parentFwdMinHops := 0
	parentRevMinHops := 0
	parentAppName := ""
	if opts != nil {
		parentMinHops = opts.MinHops
		parentFwdMinHops = opts.ForwardMinHops
		parentRevMinHops = opts.ReverseMinHops
		parentAppName = opts.AppName
	}

	// Cap on consecutive planning failures. A fully-broken
	// route-finder + local-calc combo would otherwise pin us for
	// ~3s × (maxCount-1) iterations on the way to giving up.
	// Raised from 2: when the oracle sources a big disjoint set (a busy exit
	// exposes dozens of intermediates), the destination-transport query is
	// occasionally flaky (a cold dmsg session → transient error) — 2 consecutive
	// misses stopped the pool at ~3 legs even though many more routes existed.
	// A higher bound rides through those transient misses to fill the standby
	// pool, while still bounding a genuinely-exhausted set (planning is
	// background/best-effort, never blocks the primary).
	const maxConsecutiveMuxFailures = 5

	// Phase 1 (sequential): plan each aux route. Excludes
	// propagate iteration-to-iteration so successive aux routes
	// pick disjoint intermediates. Planning is cheap relative
	// to the setup-node dial (cached route-finder lookups, in-
	// process BFS for local fallback), so keeping it serial
	// preserves the disjoint-mux guarantee without slowing things
	// down. The expensive setup-node dial runs in phase 2 below
	// in parallel.
	type muxAuxPlan struct {
		slot   int
		req    routing.BidirectionalRoute
		addFwd bool
		addRev bool
	}
	var plans []muxAuxPlan
	consecutiveFailures := 0

	for i := 1; i < maxCount; i++ {
		addFwd := i < fwdCount
		addRev := i < revCount
		if !addFwd && !addRev {
			continue
		}

		muxOpts := &DialOptions{
			MinForwardRts:             1,
			MaxForwardRts:             1,
			MinConsumeRts:             1,
			MaxConsumeRts:             1,
			Retries:                   1,
			MinHops:                   parentMinHops,
			ForwardMinHops:            parentFwdMinHops,
			ReverseMinHops:            parentRevMinHops,
			AppName:                   parentAppName,
			ExcludeTransportIDs:       excludeIDs,
			ExcludeRemoteTransportIDs: excludeRemoteIDs,
			ExcludeIntermediatePKs:    excludePKs,
			ExcludeDMSG:               true,
		}

		// Mux aux legs plan over the GLOBAL TPD graph via the route-finder
		// FIRST. calculateLocalRoutes only sees THIS visor's live managed
		// transports — on a NAT'd client that is just the webrtc NAT-traversal
		// fallback — so preferring it pinned every aux leg to a slow webrtc
		// first hop while the fast disjoint stcpr paths (which the mux-bw probe
		// proves exist over the full TPD graph) went unused, collapsing mux
		// throughput to a single slow leg. The route-finder plans over all of
		// TPD; the ExcludeIntermediatePKs/ExcludeDMSG in muxOpts keep the legs
		// disjoint and the transport-type-aware ranking (transportTypeCostMs)
		// makes the disjoint pick prefer stcpr/quic over webrtc. Local calc is
		// the fallback for when the RF can't reach a disjoint deep hop.
		muxFwd, muxRev, err := r.fetchBestRoutes(ctx, log, lPK, rPK, muxOpts, r.conf.MinHops)
		if err != nil {
			log.Debugf("Mux route %d/%d: route-finder no disjoint path: %v — trying local-calc fallback",
				i+1, maxCount, err)
			lcFwd, lcRev, lcErr := r.calculateLocalRoutes(ctx, log, lPK, rPK, muxOpts)
			if lcErr != nil {
				log.Debugf("Mux route %d/%d: local-calc fallback also failed: %v", i+1, maxCount, lcErr)
				consecutiveFailures++
				if consecutiveFailures >= maxConsecutiveMuxFailures {
					log.Debugf("Mux route planning: %d consecutive failures; giving up on remaining %d aux slots",
						consecutiveFailures, maxCount-1-i)
					break
				}
				continue
			}
			muxFwd, muxRev = lcFwd, lcRev
		}
		consecutiveFailures = 0

		// Hard mux invariants (post-fetch gate). Even though excludePKs is fed
		// to the finder as ExcludeIntermediatePKs, the local-calc fallback and
		// finder misses can still return a leg that overlaps an existing leg's
		// intermediates or loops through a visor twice. Reject such a candidate
		// HERE — before the setup-node dial — and claim its intermediates so the
		// next slot diverges from it. excludePKs is the running union of every
		// prior leg's intermediates (both directions), so it serves as the used
		// set for the forward and reverse checks alike.
		if !validMuxLeg(muxFwd, muxRev, lPK, rPK, excludePKs, excludePKs) {
			log.Debugf("Mux route %d/%d: candidate rejected — intra-route loop or intermediate overlap with an existing leg; skipping",
				i+1, maxCount)
			excludePKs = append(excludePKs, intermediatesOfHops(muxFwd, lPK, rPK)...)
			excludePKs = append(excludePKs, intermediatesOfHops(muxRev, rPK, lPK)...)
			continue
		}
		if !nrg.rg.legHopsMatch(muxFwd) {
			log.Debugf("Mux route %d/%d: candidate has %d hops, the group's legs have %d (leg.hops_match); skipping",
				i+1, maxCount, len(muxFwd), nrg.rg.legHopsTarget())
			excludePKs = append(excludePKs, intermediatesOfHops(muxFwd, lPK, rPK)...)
			continue
		}

		// The route-finder fallback ignores ExcludeTransportIDs, so it can hand
		// back a leg whose first hop reuses a transport already used by the
		// group (or an earlier planned leg). If we planned + dialed it, the
		// setup node would provision the leg on the far end, and only then would
		// appendRouteAsymmetric reject it locally — leaving the group
		// half-provisioned and its noise stream unable to open (the routes>=2
		// hang). Drop such a plan HERE, before the dial, so the far end is never
		// told about a leg we will reject. excludeIDs seeds with the primary
		// transport and accumulates each accepted plan's first hop below.
		if len(muxFwd) > 0 {
			dup := false
			for _, ex := range excludeIDs {
				if muxFwd[0].TpID == ex {
					dup = true
					break
				}
			}
			if dup {
				log.Debugf("Mux route %d/%d: planned leg reuses transport %s already in the group (route-finder fallback); skipping to avoid a half-provisioned group",
					i+1, maxCount, muxFwd[0].TpID)
				continue
			}
			// Reserve this leg's first-hop transport so later plans (and the
			// local-calc exclude set) diverge from it too.
			excludeIDs = append(excludeIDs, muxFwd[0].TpID)
		}

		// Destination-side twin of the check above. The setup node installs the
		// reverse route's first-hop ForwardRule on the DESTINATION, so its route
		// group holds one transport per leg — exactly Reverse[0].TpID — and it
		// refuses a second leg over a transport already in that set. Forward and
		// reverse are ranked independently, and a direct (0-intermediate) reverse
		// gives ExcludeIntermediatePKs nothing to bite on, so the finder happily
		// pairs a disjoint forward with a reverse over the destination's primary
		// link. That plan is guaranteed to be refused after a full setup-node
		// round trip, so drop it here and let the next slot diverge.
		if firstHopTransportExcluded(muxRev, excludeRemoteIDs) {
			log.Debugf("Mux route %d/%d: planned leg's reverse leaves the destination over transport %s, which already carries one of its legs (it would be refused); skipping before dial",
				i+1, maxCount, muxRev[0].TpID)
			continue
		}
		if len(muxRev) > 0 {
			excludeRemoteIDs = append(excludeRemoteIDs, muxRev[0].TpID)
		}

		muxKeepAlive := DefaultRouteKeepAlive
		if opts != nil && opts.KeepAlive > 0 {
			muxKeepAlive = opts.KeepAlive
		}
		plans = append(plans, muxAuxPlan{
			slot: i + 1,
			req: routing.BidirectionalRoute{
				Desc:      forwardDesc,
				KeepAlive: muxKeepAlive,
				Forward:   muxFwd,
				Reverse:   muxRev,
			},
			addFwd: addFwd,
			addRev: addRev,
		})

		// Accumulate this plan's intermediates into the exclude
		// set so the NEXT plan picks a disjoint path. Done at
		// plan time (not after dial success) — a plan whose dial
		// fails has still "claimed" those intermediates, and we
		// want the surviving plans to be disjoint from the
		// CLAIMED set, not the SUCCEEDED set, otherwise a slow
		// failure cascade could leave all surviving aux routes
		// sharing intermediates.
		if addFwd {
			excludePKs = append(excludePKs, intermediatesOfHops(muxFwd, lPK, rPK)...)
		}
		if addRev {
			excludePKs = append(excludePKs, intermediatesOfHops(muxRev, rPK, lPK)...)
		}
	}

	if len(plans) == 0 {
		return
	}

	// Phase 2 (parallel): fire all aux setup-node dials at once.
	// Each dial blocks ~setup-timeout (15s); doing them in
	// parallel collapses N × 15s into one window. Append happens
	// inside the goroutine — appendRouteAsymmetric takes rg.mu
	// internally, so concurrent appends serialize naturally.
	var wg sync.WaitGroup
	for _, p := range plans {
		wg.Add(1)
		go func(p muxAuxPlan) {
			defer wg.Done()
			muxRules, _, err := r.conf.RouteGroupDialer.Dial(ctx, log, r.dmsgC, r.conf.SetupNodes, p.req)
			if err != nil {
				log.Debugf("Mux route %d/%d: parallel setup failed: %v", p.slot, maxCount, err)
				return
			}
			if err := r.appendRouteAsymmetric(nrg, muxRules, p.addFwd, p.addRev); err != nil {
				log.Debugf("Mux route %d/%d: append failed (addFwd=%v addRev=%v): %v",
					p.slot, maxCount, p.addFwd, p.addRev, err)
				return
			}
			// Record this leg's full forward route so the per-leg mux view shows
			// its whole path (all hops, full PKs, per-hop transport type) instead
			// of falling back to the first-hop transport's remote — which for a
			// multihop leg is the first INTERMEDIATE, mislabeled as the exit. Only
			// when the forward leg was actually appended (addFwd); a reverse-only
			// slot installs no forward transport to key the hops on. The forward
			// path's first-hop TpID matches muxRules.Forward.NextTransportID (the
			// transport appendRouteAsymmetric registered), so legHopsFor finds it.
			if p.addFwd {
				nrg.rg.recordLegRoute(p.req.Forward, p.req.Reverse)
			}
			log.Infof("Mux route %d/%d established (fwd=%v rev=%v) via tp %s",
				p.slot, maxCount, p.addFwd, p.addRev, muxRules.Forward.NextTransportID())
		}(p)
	}
	wg.Wait()
}

// addOneAuxForwardLeg dials one additional forward aux leg and
// appends it to nrg. Used by the rotation hook to add a leg post-
// dial without going through establishMuxRoutes' multi-slot
// machinery.
//
// extraExcludePKs are policy-supplied hop exclusions (typically
// the PKs of legs the policy just asked to drop, or any peers it
// wants to avoid this rotation). They're merged with the route
// group's existing intermediates so the new leg is disjoint from
// what's already there AND from the policy's hint.
//
// Failure modes: route-finder + local-calc both empty → returns
// an error logged at debug. Setup-node dial failure → returns the
// error. Append failure → returns the error. None of these are
// fatal at the route group level (the group keeps its current
// leg set); the rotation goroutine logs and waits for the next
// tick.
func (r *router) addOneAuxForwardLeg(ctx context.Context, nrg *NoiseRouteGroup, opts *DialOptions, forwardDesc routing.RouteDescriptor, extraExcludePKs []cipher.PubKey) error {
	return r.addOneAuxLeg(ctx, nrg, opts, forwardDesc, extraExcludePKs, true)
}

// addOneAuxSendLeg dials one additional FORWARD-ONLY aux leg
// (appendRouteAsymmetric addFwd=true / addRev=false) and appends it to nrg.
// Used by the rotation hook for a RotationAction.AddForwardLeg — the adaptive
// preset's upload-saturation widen. It adds upstream send capacity WITHOUT a
// paired reverse rule, so it does not enlarge the reverse/download set.
//
// Caveat (see the full-duplex note in addOneAuxLeg): a leg with no local
// reverse (consume) rule black-holes any DOWNLOAD the far end spreads onto it.
// That is acceptable here precisely because this leg is grown for an
// upload-dominant flow (little reverse traffic) and the leg-dataprogress /
// leg-liveness prunes evict it if the far end mis-spreads bulk download onto
// it. It is the "forward actuation can't fully mirror reverse" corner: the warm
// standby pool is full-duplex, so a forward-only widen must be a fresh leg.
func (r *router) addOneAuxSendLeg(ctx context.Context, nrg *NoiseRouteGroup, opts *DialOptions, forwardDesc routing.RouteDescriptor, extraExcludePKs []cipher.PubKey) error {
	return r.addOneAuxLeg(ctx, nrg, opts, forwardDesc, extraExcludePKs, false)
}

// addOneAuxLeg is the shared implementation behind addOneAuxForwardLeg
// (addRev=true, full-duplex) and addOneAuxSendLeg (addRev=false, forward-only).
func (r *router) addOneAuxLeg(ctx context.Context, nrg *NoiseRouteGroup, opts *DialOptions, forwardDesc routing.RouteDescriptor, extraExcludePKs []cipher.PubKey, addRev bool) error {
	if nrg == nil || nrg.rg == nil {
		return fmt.Errorf("route group nil")
	}
	log := r.scopedLogForOpts(opts, forwardDesc.SrcPort())
	lPK := forwardDesc.SrcPK()
	rPK := forwardDesc.DstPK()

	// Existing intermediates from the route group + policy excludes + same-LAN
	// peers (see establishMuxRoutes: a same-LAN intermediate has best latency but
	// zero path diversity, so without this the rotation loop re-adds that same
	// useless leg every tick).
	excludePKs := intermediatesOfRouteGroup(nrg, lPK, rPK)
	excludePKs = append(excludePKs, extraExcludePKs...)
	excludePKs = appendUniquePKs(excludePKs, r.sameLANExcludedPKs())

	// Existing tp IDs to exclude (prevents picking the same
	// transports the route group already uses).
	nrg.rg.mu.Lock()
	excludeIDs := make([]uuid.UUID, 0, len(nrg.rg.tps))
	for _, tp := range nrg.rg.tps {
		if tp != nil {
			excludeIDs = append(excludeIDs, tp.Entry.ID)
		}
	}
	nrg.rg.mu.Unlock()

	// Far-end transports this group's live legs already occupy. The destination
	// refuses a leg whose reverse leaves it over one of these, so they must be
	// excluded from the REVERSE pick just as excludeIDs is from the forward one.
	excludeRemoteIDs := nrg.rg.remoteLegTransportIDs()

	parentMinHops := 0
	parentFwdMinHops := 0
	parentAppName := ""
	if opts != nil {
		parentMinHops = opts.MinHops
		parentFwdMinHops = opts.ForwardMinHops
		parentAppName = opts.AppName
	}

	muxOpts := &DialOptions{
		MinForwardRts:             1,
		MaxForwardRts:             1,
		MinConsumeRts:             1,
		MaxConsumeRts:             1,
		Retries:                   1,
		MinHops:                   parentMinHops,
		ForwardMinHops:            parentFwdMinHops,
		AppName:                   parentAppName,
		ExcludeTransportIDs:       excludeIDs,
		ExcludeRemoteTransportIDs: excludeRemoteIDs,
		ExcludeIntermediatePKs:    excludePKs,
		ExcludeDMSG:               true,
	}

	// SHARED WARM-ROUTE POOL (phase 1): before the route-finder round-trip, try
	// the visor-level plan cache. Every mux-enabled group's self-heal/rotation
	// dials an aux leg through here, so N tunnels aggregating to the same exit
	// would otherwise each re-run the same route-finder + disjointness work per
	// leg per tick — the "planning storm". A cached plan is a group-independent
	// path (no route IDs); it is fed into the SAME setup-node Dial + validMuxLeg
	// gate below, so a stale plan is rejected exactly like a fresh bad one and a
	// miss falls through to fetchBestRoutes — the cache can only save work, never
	// change the leg that gets built. See docs/design/shared-warm-route-pool.md.
	keyMinHops := r.conf.MinHops
	if e := muxOpts.EffectiveMinHops(true); e > 0 {
		keyMinHops = uint16(e) //nolint:gosec
	}
	// POOL-SOURCED LEGS: before the cache is consulted, offer it the routes of
	// this app's STANDBY sibling tunnels to the same exit — already ranked,
	// already measured, first-hop transport already up (see pool_legs.go). The
	// seed is lazy because the router cannot see the app's pool settlement, and
	// it is free of consequence: with no standby siblings nothing is seeded and
	// everything below is byte-for-byte what it was.
	r.seedPoolPlans(nrg.rg.desc, keyMinHops)
	var muxFwd, muxRev []routing.Hop
	if cf, cr, source, ok := r.warmRoutes.bestPlanSourced(rPK, keyMinHops, excludeIDs, excludeRemoteIDs, excludePKs); ok {
		muxFwd, muxRev = cf, cr
		log.WithField("first_tp", func() string {
			if len(cf) > 0 {
				return cf[0].TpID.String()
			}
			return ""
		}()).WithField("plan_source", planSource(source)).
			Debug("Mux add-leg: served disjoint plan from shared warm-route pool (skipped route-finder)")
		if source != "" {
			nrg.rg.noteMuxEvent(MuxEvent{
				Event: MuxEventDialDecision, By: MuxByLocal, LegIndex: -1, Legs: nrg.rg.legCount(),
				TpID: cf[0].TpID, Reason: source + "; grown without a route-finder call",
			})
		}
	} else {
		// Plan the replacement/added leg over the GLOBAL TPD graph via the
		// route-finder first (same rationale as establishMuxRoutes): local calc is
		// restricted to this visor's live transports (webrtc on a NAT'd client), so
		// a self-healed/rotated leg re-collapses to a slow webrtc first hop unless
		// we let the RF reach fast disjoint stcpr intermediates over the full graph.
		// The disjoint excludes + transport-type-aware ranking keep it disjoint and
		// off webrtc; local calc is the fallback for a disjoint deep-hop the RF misses.
		fFwd, fRev, err := r.fetchBestRoutes(ctx, log, lPK, rPK, muxOpts, r.conf.MinHops)
		if err != nil {
			lcFwd, lcRev, lcErr := r.calculateLocalRoutes(ctx, log, lPK, rPK, muxOpts)
			if lcErr != nil {
				return fmt.Errorf("rotation add-leg: no route found (route-finder=%v, local-calc=%v)", err, lcErr)
			}
			fFwd, fRev = lcFwd, lcRev
		}
		muxFwd, muxRev = fFwd, fRev
		// Populate the shared pool so the NEXT group/leg to this exit reuses this
		// freshly-discovered disjoint plan instead of re-querying the finder.
		r.warmRoutes.put(rPK, keyMinHops, muxFwd, muxRev)
	}

	// Hard mux invariants (post-fetch gate): the planned leg must be loop-free
	// and its intermediates disjoint from every existing leg. excludePKs holds
	// the group's intermediates plus the caller's exclude hints, so it is the
	// used set for both directions. A violating candidate is skipped (the
	// rotation goroutine keeps the current leg set and retries next tick).
	if !validMuxLeg(muxFwd, muxRev, lPK, rPK, excludePKs, excludePKs) {
		return errors.New("rotation add-leg: planned leg violates mux invariants (intra-route loop or intermediate overlap with an existing leg)")
	}
	if !nrg.rg.legHopsMatch(muxFwd) {
		return fmt.Errorf("rotation add-leg: planned leg has %d hops, the group's legs have %d (leg.hops_match)",
			len(muxFwd), nrg.rg.legHopsTarget())
	}

	// The route-finder honors ExcludeIntermediatePKs but NOT ExcludeTransportIDs,
	// so with a 1-hop-capable min_hops it keeps returning a leg whose first hop is
	// the direct transport that is already the primary leg (excludeIDs holds every
	// live leg's transport). Dialing it burns a setup-node round-trip every
	// rotation tick only for appendRouteAsymmetric to reject it locally
	// ("already in the group") — the dominant warm-standby churn (observed 65 such
	// dials in a 3-minute window, all for the same transport). Drop the plan HERE,
	// before the setup-node dial, exactly as establishMuxRoutes/GrowMuxRoute
	// already do on their planning paths.
	if len(muxFwd) > 0 {
		for _, ex := range excludeIDs {
			if muxFwd[0].TpID == ex {
				return fmt.Errorf("rotation add-leg: planned leg reuses transport %s already in the group (finder ignores ExcludeTransportIDs); skipping before dial", muxFwd[0].TpID)
			}
		}
	}

	// The DESTINATION-side twin of the check above, and the one that was missing.
	// The setup node installs the reverse route's first-hop ForwardRule on the
	// destination, so the destination's route group holds exactly Reverse[0].TpID
	// per leg — and its appendRouteToGroup refuses a second leg over a transport
	// already in that set. Forward and reverse are ranked INDEPENDENTLY and a
	// direct (0-intermediate) reverse carries nothing for ExcludeIntermediatePKs
	// to bite on, so the finder keeps handing back a reverse over the destination's
	// primary-leg transport. Dialing that burns a full setup-node round trip (ID
	// reservation on every hop + intermediary rule install) for a guaranteed
	// "refusing to append mux leg over transport %s already in the group".
	// Skip before the dial; the group keeps the legs it has and retries next tick.
	if firstHopTransportExcluded(muxRev, excludeRemoteIDs) {
		return fmt.Errorf("rotation add-leg: planned leg's reverse leaves the destination over transport %s, which already carries one of its legs (it would be refused); skipping before dial", muxRev[0].TpID)
	}

	muxKeepAlive := DefaultRouteKeepAlive
	if opts != nil && opts.KeepAlive > 0 {
		muxKeepAlive = opts.KeepAlive
	}
	req := routing.BidirectionalRoute{
		Desc:      forwardDesc,
		KeepAlive: muxKeepAlive,
		Forward:   muxFwd,
		Reverse:   muxRev,
	}
	muxRules, _, err := r.conf.RouteGroupDialer.Dial(ctx, log, r.dmsgC, r.conf.SetupNodes, req)
	if err != nil {
		return fmt.Errorf("rotation add-leg: setup-node dial: %w", err)
	}
	// Append the leg. Full-duplex (addRev=true, the default rotation/self-heal
	// path): forward-only would delete the initiator's consume (reverse) rule for
	// this leg — but the setup-node dial already installed that rule on the far
	// end, which marks the leg ready on our forward handshake and then spreads its
	// bulk (download) stream onto it. With the initiator's consume rule gone those
	// packets are dropped (errRouteDescNotExist), so the leg black-holes (recv=0)
	// and, because the reorder buffer is lossless, the missing sequences
	// head-of-line-stall the primary leg too — a net 0-byte transfer. So the
	// full-duplex path keeps the reverse rule. addRev=false is used ONLY by the
	// adaptive preset's forward-only (AddForwardLeg) upload widen, where the flow
	// is upload-dominant so little download lands here, and the data-progress /
	// liveness prunes evict the leg if the far end mis-spreads download onto it.
	if err := r.appendRouteAsymmetric(nrg, muxRules, true, addRev); err != nil {
		return fmt.Errorf("rotation add-leg: append: %w", err)
	}
	// Record this leg's full forward route (addFwd is always true here) so the
	// per-leg mux view shows its whole path instead of the first-hop transport's
	// remote — which for a multihop leg is the first intermediate, not the exit.
	nrg.rg.recordLegRoute(muxFwd, muxRev)
	log.Infof("Rotation aux leg established (addRev=%v) via tp %s", addRev, muxRules.Forward.NextTransportID())
	return nil
}

// validMuxLeg reports whether a candidate leg satisfies the two hard mux
// invariants given the intermediates already claimed by the group's other
// legs (usedFwd for the forward direction, usedRev for the reverse): the
// forward and reverse paths must each be loop-free, and each direction's
// intermediates must be fully disjoint from the corresponding used set. It
// is the post-fetch gate applied to every candidate before it is committed
// as a leg — the route-finder honors ExcludeIntermediatePKs but its
// local-calc fallback and finder misses can still return an overlapping or
// looping path.
func validMuxLeg(fwd, rev []routing.Hop, src, dst cipher.PubKey, usedFwd, usedRev []cipher.PubKey) bool {
	if hasLoop(fwd) || hasLoop(rev) {
		return false
	}
	if !disjointFrom(routeIntermediates(fwd, src, dst), usedFwd) {
		return false
	}
	return disjointFrom(routeIntermediates(rev, dst, src), usedRev)
}
