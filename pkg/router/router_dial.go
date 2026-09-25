//go:build !tinygo || (js && wasm)

// Package router pkg/router/router_dial.go c2-net-routing
// router_dial.go contains route dialing and route finding logic.
package router

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/noise"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// DialRoutes dials to a given visor of 'rPK'.
// 'lPort'/'rPort' specifies the local/remote ports respectively.
// A nil 'opts' input results in a value of '1' for all DialOptions fields.
// A single call to DialRoutes should perform the following:
// - Find routes via RouteFinder (in one call).
// - Setup routes via SetupNode (in one call).
// - Save to routing.Table and internal RouteGroup map.
// - Return RouteGroup if successful.
func (r *router) DialRoutes(
	ctx context.Context,
	rPK cipher.PubKey,
	lPort, rPort routing.Port,
	opts *DialOptions,
) (net.Conn, error) {

	// DialRoutes may be called with nil opts — api_skynet.ConnectRawTCP passes
	// nil, and while the early path guards with `opts != nil`, the post-setup
	// success path dereferences it unconditionally (opts.Distribution in
	// applyDistribution, opts.AppName in the leg-change-hook wiring). Normalize
	// to a zero-value DialOptions so a successful skynet dial can't nil-deref
	// and crash the visor.
	if opts == nil {
		opts = &DialOptions{}
	}

	log := r.scopedLogForOpts(opts, lPort)

	if rPK.Null() {
		err := ErrRemoteEmptyPK
		log.WithError(err).Error("Failed to dial routes.")
		return nil, fmt.Errorf("failed to dial routes: %w", err)
	}

	// An adoption builds no route: the chain already exists (leg_adopt.go).
	if opts.AdoptReservePort != 0 {
		return r.adoptLegReserve(ctx, log, rPK, lPort, rPort, opts)
	}

	// Operator-programmable routing policy hook (RFC #2882).
	// When configured, the hook adjusts opts.MuxRoutes /
	// opts.MinHops before route setup, and can refuse the dial
	// entirely via Fallback="drop". A nil hook short-circuits
	// the entire block so policy-free deployments pay zero cost.
	// dialPolicyHook also returns nil for a --direct dial, which is
	// policy-free by definition (see its doc).
	if opts != nil && opts.EnsureDirectTransport {
		log.Debug("--direct dial: bypassing routing policy (policy-free 1-hop direct leg)")
	}
	if hook := r.dialPolicyHook(opts, rPort); hook != nil {
		if opts == nil {
			opts = &DialOptions{}
		}
		appName := ""
		if opts != nil {
			appName = opts.AppName
		}
		info := DialInfo{
			AppName:      appName,
			PeerPK:       rPK,
			LPort:        lPort,
			RPort:        rPort,
			CLIOverrides: buildCLIOverrides(opts),
		}
		if adj, hookErr := hook.BeforeDial(ctx, info); hookErr != nil {
			// Failure to evaluate the policy is non-fatal — the
			// hook's own failure-mode wiring decides whether to
			// return a "drop" adjustment or fall back to defaults.
			// Here we just log it and proceed without adjustment.
			log.WithError(hookErr).Debug("policy hook errored; proceeding without adjustment")
		} else if err := applyAdjustment(opts, adj); err != nil {
			log.WithField("policy_decision", "drop").Info("Dial refused by routing policy.")
			return nil, err
		} else if adj.MuxRoutes > 0 || adj.MinHops > 0 || adj.Distribution.Mode != DistributionUnset {
			log.
				WithField("policy_mux", adj.MuxRoutes).
				WithField("policy_min_hops", adj.MinHops).
				WithField("policy_distribution", adj.Distribution.Mode).
				Debug("Routing policy adjusted dial opts.")
		}
	}

	if r.conf.MinHops == 0 {
		log.Error("Routing disabled. (minhop=0)")
		return nil, fmt.Errorf("routing disabled. (minhop=0)")
	}

	lPK := r.conf.PubKey
	forwardDesc := routing.NewRouteDescriptor(lPK, rPK, lPort, rPort)

	// Multi-leg tunnels at dial time. An app tunnel asks for a single-route
	// group and has its legs added afterwards; when the operator has set a
	// dial-time tunnel width, dial the legs with it instead (see
	// applyDialTunnelLegs). Off by default, so nothing changes until asked.
	if legs := applyDialTunnelLegs(opts); legs > 0 {
		opts.note("dial-time tunnel legs: %d", legs)
		log.WithField("tunnel_legs", legs).Debug("Dialing tunnel with its legs.")
	}

	// Multi-tunnel bandwidth aggregation (docs/mux_aggregation_rfc.md step 3).
	// When the caller opts in (skysocks-client's extra tunnels), diverge this
	// tunnel from the ones already established to the SAME exit: exclude the
	// first-hop transports + intermediates that this visor's live route groups
	// to (rPK, rPort) already occupy, so the new tunnel leaves over a different
	// first-hop transport and the two tunnels' throughputs SUM. Bounded to
	// tunnel 2..N — the exclusions are added ONLY when a sibling route group to
	// this dst already exists (count > 0), so a first/lone dial is untouched.
	// Additive to any excludes the caller already set (e.g. the mux's own);
	// the route-finder / local calc treat these as soft preferences and fall
	// back to a shared path when no disjoint transport is free.
	if opts.DiversifyTransports {
		// The route groups that already exist are only half the picture: a pool
		// fill runs setup.fill_inflight dials at once, and a dial that has not
		// finished has no route group for the scan above to find. Its claimed
		// first hop counts all the same (#5125) — without this the startup burst
		// put eight tunnels on one direct transport inside a second, each of
		// them correctly reporting "no sibling group yet".
		if exIDs, exPeers, exIPs := r.inFlightFirstHopExclusions(rPK, rPort); len(exIDs) > 0 {
			opts.ExcludeTransportIDs = append(opts.ExcludeTransportIDs, exIDs...)
			opts.ExcludeFirstHopPeers = append(opts.ExcludeFirstHopPeers, exPeers...)
			opts.ExcludeFirstHopIPs = append(opts.ExcludeFirstHopIPs, exIPs...)
			opts.note("diversify: %d dial(s) in flight to %s:%d, excluding their claimed first hop(s) %s",
				len(exIDs), rPK.String()[:8], rPort, shortTpIDs(exIDs))
			log.WithField("inflight_dials", len(exIDs)).
				Debug("Diversifying around the first hops in-flight sibling dials have claimed.")
		}
		if exIDs, exPKs, exPeers, exIPs, count := r.siblingRouteGroupExclusions(lPK, rPK, rPort); count > 0 {
			opts.ExcludeTransportIDs = append(opts.ExcludeTransportIDs, exIDs...)
			opts.ExcludeIntermediatePKs = append(opts.ExcludeIntermediatePKs, exPKs...)
			opts.ExcludeFirstHopPeers = append(opts.ExcludeFirstHopPeers, exPeers...)
			opts.ExcludeFirstHopIPs = append(opts.ExcludeFirstHopIPs, exIPs...)
			opts.note("diversify: %d sibling group(s) to %s:%d, excluding first-hop tp(s) %s and %d first-hop peer(s)",
				count, rPK.String()[:8], rPort, shortTpIDs(exIDs), len(exPeers))
			log.WithField("sibling_tunnels", count).
				WithField("exclude_tps", len(exIDs)).
				WithField("exclude_first_hop_peers", len(exPeers)).
				WithField("exclude_intermediates", len(exPKs)).
				Debug("Diversifying multi-tunnel dial over disjoint first-hop transports.")
		} else {
			opts.note("diversify: no sibling group to %s:%d yet", rPK.String()[:8], rPort)
		}
	}

	// check if transport exist, then skip minhop value and consider it equal 0.
	// Per-call opts.MinHops > 1 suppresses this downgrade: the caller has
	// explicitly demanded a multi-hop path, and silently routing through the
	// direct transport when one happens to exist would defeat the constraint
	// (this was the symptom of `mux-bw --min-hops 2` riding direct stcpr
	// instead of going via intermediates).
	// baseMinHops is a LOCAL effective min-hops for this dial. We must NOT
	// mutate the shared r.conf.MinHops here: DialRoutes runs concurrently per
	// app dial, so writing the field both races other in-flight dials and —
	// because the old restore only ran on the success path while 7 early
	// `return nil, err` sit between mutation and restore — would permanently
	// pin the visor-global MinHops to 1 after the first failed dial, silently
	// disabling the operator's min-hops policy for every subsequent dial.
	baseMinHops := r.conf.MinHops
	// Suppress the direct-tp downgrade when ANY min-hops constraint is
	// set (symmetric or per-direction). Even if only ReverseMinHops > 1,
	// downgrading globally to 1 would defeat that constraint for the
	// reverse direction's route-finder query below.
	// --direct: ensure a direct transport to the destination exists, creating
	// one on demand if none is open. This is the self-healing half of the
	// `--direct` foot-gun fix — when the direct transport drops (peer restart,
	// etc.) the next dial recreates it instead of silently riding a flaky
	// multihop route (the route-finder only returns a 1-hop route when a live
	// direct transport is already known to TPD). After this, isTpdExist(rPK) is
	// true → baseMinHops downgrades to 1, and UseExistingTpOnly (set alongside
	// EnsureDirectTransport) bypasses the route-finder → a 1-hop direct dial.
	// Creation order is the host-aware direct-type preference
	// (EnsureBestTransport): every direct type this visor can actually create,
	// in the configured preference order, with the DMSG relay as the loud last
	// resort. The previous hardcoded stcpr→sudph→dmsg list never tried the
	// browser carriers at all — on a wasm visor (no raw TCP/UDP clients) both
	// direct attempts failed instantly and --direct minted a dmsg RELAY
	// transport when a webrtc/ws/wt one was there for the making.
	if opts != nil && opts.EnsureDirectTransport && !r.isTpdExist(rPK) {
		if err := r.tm.EnsureBestTransport(ctx, rPK); err != nil {
			log.WithError(err).WithField("remote", rPK).
				Debug("--direct: could not create a direct transport on demand")
		}
	}
	// The downgrade applies only when NOTHING asks for more than one hop —
	// and "nothing" includes the visor-global setting, not just the per-dial
	// overrides. opts.MinHops == 0 means "inherit Config.MinHops", so reading
	// the zero as "no constraint" handed an operator who set min_hops=3 a
	// one-hop route the moment a direct transport happened to exist, without
	// a word. On the VPN that is the privacy control silently not applying.
	if r.isTpdExist(rPK) && r.EffectiveMinHops(opts) <= 1 {
		baseMinHops = 1
	}

	// Check if existing transport only mode is set on the router
	routerExistingTpOnly := r.existingTpOnly.Load()

	// Only run route setup hooks (which may create new transports) if UseExistingTpOnly is false
	// on both the router level and the dial options level
	useExistingOnly := routerExistingTpOnly || (opts != nil && opts.UseExistingTpOnly)
	// hookDone, when non-nil, signals completion of a BACKGROUND transport-
	// creation hook (the race path). The fetch loop waits on it only if it can't
	// find a route over existing transports (see the fetch-error branch below).
	var hookDone chan struct{}
	if baseMinHops == 1 && !useExistingOnly {
		r.routeSetupHookMu.Lock()
		hooks := r.routeSetupHooks
		r.routeSetupHookMu.Unlock()
		if len(hooks) != 0 {
			if r.conf.DisableRaceRouteSetup {
				// Legacy synchronous path: create the transport, THEN route.
				for _, rsf := range hooks {
					if err := rsf(rPK, r.tm); err != nil {
						return nil, err
					}
				}
			} else {
				// RACE (thread 1): the transport-creation hook can block ~20s
				// (STCPR→SUDPH→DMSG dial attempts) before we ever query the
				// route-finder. Run it in the BACKGROUND and route over EXISTING
				// transports meanwhile — whichever lands first wins. The hook still
				// creates the direct transport regardless (it self-manages its own
				// context, so it's NOT canceled when this dial returns): it's ready
				// for the next dial / a later upgrade. The fetch loop waits on
				// hookDone only when no existing route is found, so a cold peer
				// still gets connectivity via the freshly-created transport.
				hookDone = make(chan struct{})
				go func() {
					defer close(hookDone)
					for _, rsf := range hooks {
						if err := rsf(rPK, r.tm); err != nil {
							log.WithError(err).Debug("background route-setup hook (transport creation) failed; relying on existing routes")
						}
					}
				}()
			}
		}
	} else if useExistingOnly {
		log.Debug("UseExistingTpOnly is set, skipping transport creation hooks")
	}

	// Retry route setup with fresh routes if it fails due to stale TPD data.
	// Route-finder may return routes with non-existent transports (TPD sync
	// issues), so we query for fresh routes on each retry instead of retrying
	// the same bad route.
	//
	// With --local-route enabled, fetchBestRoutes runs calculateLocalRoutes
	// instead — that's deterministic over the local transport graph, so a
	// retry against the same graph state would just burn CPU rebuilding the
	// transport cache (~1s, 1.5k entries) for the same answer. Fail fast in
	// that mode, and don't emit the "Route finder failed" wording either,
	// since no route-finder query was ever made.
	forceLocal := r.forceLocalRoutes.Load()
	// maxRetries bounds how many candidate routes the fallback loop below
	// walks before giving up. On each setup failure we exclude the failing
	// intermediate and re-fetch a fresh route (a different intermediate), so
	// a higher bound = more of the candidate set tried before the dial fails.
	// Multihop setups over flaky intermediates need several tries to land a
	// healthy path; combined with the tightened handshake/cascade timeouts
	// (fast per-attempt failure) this stays well within a reasonable dial
	// budget while greatly improving multihop establishment odds.
	const maxRetries = 6
	maxFetchAttempts := maxRetries
	if forceLocal {
		maxFetchAttempts = 1
	}

	// Fast-fail when this visor holds ZERO transports and no background
	// transport-creation is pending (hookDone == nil). The route-finder
	// needs a local first-hop transport to return any route, so querying it
	// 6× here is pure waste + log spam ("Route finder failed ... transport
	// not found"). A browser/wasm visor with no transports yet hits this via
	// app route setup. We must NOT short-circuit when hookDone != nil: that's
	// the cold-start race where a background hook is creating the FIRST
	// transport for a cold peer, and the loop below waits on it once.
	if hookDone == nil && r.tm != nil && r.tm.TransportCount() == 0 {
		log.Debug("No local transports and no pending transport creation; skipping route finder")
		// Naming min-hops when it is the reason there is no transport to
		// begin with. The hook that creates one on demand runs only at
		// baseMinHops == 1 (see above), so a visor holding none — a phone
		// that has just started, most of all — cannot dial at 2 or more:
		// route setup is skipped here, before the route finder is ever
		// asked, and "no transports available" on its own reads as a
		// network fault rather than a consequence of the chosen setting.
		if baseMinHops > 1 {
			// Naming public autoconnect because it is the lever, not a
			// detail: it is what fills the transport table with public
			// visors, and those are the intermediates a multi-hop route is
			// made of. Dialing once at one hop only builds a transport to
			// the exit — enough to get past this check, not enough to route
			// THROUGH anything, so the finder would then return nothing.
			return nil, fmt.Errorf("no transports available and none created for a multi-hop dial "+
				"(min_hops=%d): this visor holds no transport to route through — "+
				"enable autoconnect to public visors so it builds some, or lower the setting",
				baseMinHops)
		}
		return nil, fmt.Errorf("no transports available; route setup skipped")
	}

	// escalateToLegacy is flipped on when a cascade-installed route fails its
	// data-plane handshake (the fingerprint of a destination that doesn't trust
	// cascade-installed routes — a mixed-cascade fleet). Once set, subsequent
	// attempts dial through the classic trusted-setup-node path the destination
	// DOES accept. See WithForceLegacyRouteSetup.
	escalateToLegacy := false

	// First hops this dial has CLAIMED against concurrent sibling dials, held
	// until the dial returns — by then its route group is registered and
	// siblingRouteGroupExclusions reports the same hop on its own. See
	// dial_first_hop_holds.go (#5125).
	var claimReleases []func()
	defer func() {
		for _, release := range claimReleases {
			release()
		}
	}()

	for attempt := 1; attempt <= maxRetries; attempt++ {
		// PARALLEL candidate route-group setup — the steady-connection fix.
		// When configured (K>1) and this isn't the cold-start transport-
		// creation race (hookDone==nil), local-route mode, or an operator
		// route-selecting policy, fetch the top-K candidates in ONE finder
		// query and race their setup concurrently. The FIRST candidate to
		// complete its reciprocal handshake WINS and is returned immediately
		// as the working base — a dead/non-forwarding candidate loses the race
		// instead of burning the full handshake await and blocking the dial.
		// On total race failure we exclude the raced intermediates (already
		// marked suspect) and fall through to the sequential path this same
		// attempt, which carries the local-calc fallback.
		if K := r.effectiveParallelK(opts); K > 1 && hookDone == nil && !forceLocal && !r.hasRouteSelectingHook() {
			keepAlive := DefaultRouteKeepAlive
			if opts != nil && opts.KeepAlive > 0 {
				keepAlive = opts.KeepAlive
			}
			candidates, ferr := r.fetchCandidateRoutes(ctx, log, lPK, rPK, opts, baseMinHops, keepAlive, forwardDesc, K)
			if ferr == nil && len(candidates) >= 2 {
				nsConf := noise.Config{
					LocalPK:   r.conf.PubKey,
					LocalSK:   r.conf.SecKey,
					RemotePK:  rPK,
					Initiator: true,
				}
				appName := ""
				datagram := false
				role := ""
				if opts != nil {
					appName = opts.AppName
					datagram = opts.Datagram
					role = opts.TunnelRole
				}
				dial := func(dctx context.Context, c routing.BidirectionalRoute) (routing.EdgeRules, cipher.PubKey, error) {
					return r.conf.RouteGroupDialer.Dial(dctx, log, r.dmsgC, r.conf.SetupNodes, c)
				}
				handshake := func(hctx context.Context, _ routing.BidirectionalRoute, rules routing.EdgeRules) (*NoiseRouteGroup, error) {
					if err := r.SaveRoutingRules(rules.Forward, rules.Reverse); err != nil {
						return nil, err
					}
					nrg, err := r.saveRouteGroupRules(hctx, rules, nsConf, appName, role, datagram)
					if err != nil {
						// Free the local key route IDs so the next candidate /
						// attempt can reserve cleanly (mirrors the sequential path).
						r.rt.DelRules([]routing.RouteID{rules.Forward.KeyRouteID(), rules.Reverse.KeyRouteID()})
						return nil, err
					}
					return nrg, nil
				}
				onLoser := func(c routing.BidirectionalRoute) {
					// Penalize the losing/failed candidate's intermediates so the
					// NEXT dial front-loads known-good hops. Armed here for EVERY
					// failure mode (reservation, handshake-timeout, ctx-deadline),
					// which is the arming gap #4063 alone had.
					r.suspects.armAll(intermediatePKsOfPath(c.Forward, lPK, rPK))
				}
				// The raced candidates are set up CONCURRENTLY, so this dial is
				// occupying every one of their first hops for the length of the
				// race. Claim them, and drop any a sibling dial already holds —
				// racing over a hop another in-flight tunnel is claiming is the
				// same collision the sequential gate refuses (#5125). Released
				// as soon as the race ends: the winner's route group is
				// registered by then, so the exclusion scan sees it for itself.
				var raceReleases []func()
				releaseRaced := func() {
					for _, release := range raceReleases {
						release()
					}
					raceReleases = nil
				}
				if opts != nil && opts.DiversifyTransports {
					free := make([]routing.BidirectionalRoute, 0, len(candidates))
					for _, c := range candidates {
						release, claimed := r.claimFirstHop(rPK, rPort, c.Forward)
						if !claimed {
							continue
						}
						raceReleases = append(raceReleases, release)
						free = append(free, c)
					}
					if len(free) == 0 {
						opts.note("K-race: every candidate's first hop is claimed by an in-flight sibling dial; sequential dial")
						log.Debug("diversify: in-flight sibling dials hold every raced candidate's first hop; deferring to the sequential dial")
					}
					candidates = free
				}
				// An empty list here means the claim filter above took every
				// candidate; the sequential path below re-picks with those hops
				// excluded, which is what it is for (and nothing was claimed in
				// that case, so there is nothing to release here).
				if len(candidates) > 0 {
					nrg, rules, winIdx, rerr := r.raceCandidateSetup(ctx, log, candidates, dial, handshake, onLoser)
					releaseRaced()
					if rerr == nil {
						opts.note("K-race: candidate %d/%d won", winIdx+1, len(candidates))
						return r.finishDial(log, nrg, rules, candidates[winIdx].Forward, candidates[winIdx].Reverse, forwardDesc, opts, rPK, lPort, rPort), nil
					}
					if ctx.Err() != nil {
						return nil, ctx.Err()
					}
					// Every raced candidate failed. Exclude their intermediates and
					// fall through to the sequential path (fresh finder query +
					// local-calc fallback) this same attempt.
					for _, c := range candidates {
						for _, ipk := range intermediatePKsOfPath(c.Forward, lPK, rPK) {
							opts = appendExcludeIntermediate(opts, ipk)
						}
					}
					log.WithError(rerr).Warnf("Parallel route setup: all %d candidate(s) failed (attempt %d/%d); excluding intermediates, falling back to sequential setup",
						len(candidates), attempt, maxRetries)
				}
			}
			// ferr != nil or <2 candidates: fall through to the sequential path,
			// which has the full retry/local-calc machinery.
		}

		var forwardPath, reversePath []routing.Hop
		var err error
		if hookDone != nil {
			// RACE the background direct-transport creation against the
			// route-finder. At cold start (visor restart) the finder query blocks
			// on its full HTTP-over-dmsg timeout (RouteFinderTimeout, ~10s while the
			// dmsg path to the finder is still warming) AND can only ever return a
			// MULTIHOP path — a direct 1-hop route requires the transport already be
			// in TPD, which it isn't yet. Meanwhile the background hook is building
			// exactly that direct transport (ready in a few seconds). So run the
			// finder in a goroutine and take whichever lands first: if the direct
			// transport comes up, use it immediately via the cheap in-memory
			// directRoute probe (no TPD bulk fetch, no multihop calc) and abandon
			// the finder; otherwise fall back to the finder's (multihop) result.
			// This is consumed at most once — later attempts use the sync path.
			fetchCtx, cancelFetch := context.WithCancel(ctx)
			type fetchRes struct {
				fwd, rev []routing.Hop
				err      error
			}
			resCh := make(chan fetchRes, 1)
			go func() {
				f, rv, e := r.fetchBestRoutes(fetchCtx, log, lPK, rPK, opts, baseMinHops)
				resCh <- fetchRes{f, rv, e}
			}()
			select {
			case <-hookDone:
				hookDone = nil // consume the race signal once
				// A diversify dial's direct route must not leave over a transport a
				// sibling tunnel already holds: this race is won instantly when the
				// direct transport already exists, and it handed every extra tunnel
				// the same first hop with none of the filtered paths ever consulted
				// (the dial_decision trail ended at the seeded exclusion). Take the
				// finder's filtered result instead.
				f, rv, ok := r.directRoute(lPK, rPK)
				if ok && opts.DiversifyTransports && r.firstHopExcluded(f, opts) {
					// Every direct transport to this exit is a TWIN of the one
					// the sibling tunnel holds — same peer, same host, same
					// path — so once a direct tunnel exists none of them is a
					// free first hop and this race must not win with one. Only
					// a genuinely free direct transport (a different peer, which
					// for a 1-hop route to rPK cannot happen) is taken here;
					// otherwise wait for the ranked oracle / finder candidates,
					// which are the intermediates.
					if free := r.freeFirstHops(r.directRoutes(lPK, rPK), opts); len(free) > 0 {
						f = r.rankFreeFirstHops(ctx, opts, free, nil)[0]
						rv = reverseHops(f)
					} else {
						opts.note("hook-race: every direct route to %s shares the sibling's first hop (%s); awaiting the ranked candidates",
							rPK.String()[:8], f[0].TpID.String()[:8])
						ok = false
					}
				}
				if ok {
					cancelFetch() // direct transport up → abandon the finder query
					log.Debug("Direct transport ready; using 1-hop route (route-finder query abandoned)")
					if opts.DiversifyTransports {
						opts.note("hook-race: direct route %s", f[0].TpID.String()[:8])
					}
					forwardPath, reversePath, err = f, rv, nil
				} else {
					// Hook finished but no direct transport (dst not directly
					// reachable) — we genuinely need the finder's multihop route.
					res := <-resCh
					forwardPath, reversePath, err = res.fwd, res.rev, res.err
				}
			case res := <-resCh:
				// Finder (or a route over existing transports) returned first.
				forwardPath, reversePath, err = res.fwd, res.rev, res.err
			case <-ctx.Done():
				cancelFetch()
				return nil, ctx.Err()
			}
			cancelFetch()
		} else {
			forwardPath, reversePath, err = r.fetchBestRoutes(ctx, log, lPK, rPK, opts, baseMinHops)
		}
		if errors.Is(err, ErrNoDisjointFirstHop) {
			// Settled, not transient: retrying re-runs the same exhausted
			// candidate list against the same topology. Surface it at once so
			// the caller can stop growing its pool.
			return nil, err
		}
		if err != nil {
			// RACE fallback: no route over existing transports (yet). If the
			// background transport-creation hook is still running, wait for it
			// ONCE — the direct transport it creates yields a 1-hop route on the
			// next fetch. This is the cold-peer half of the race: when the shaped
			// network has no usable route, we still get connectivity via the new
			// transport, bounded by the dial ctx. A warm peer never reaches here
			// (its fetch succeeded), so it never blocks on the hook.
			if hookDone != nil {
				log.Debug("No route over existing transports; waiting for background transport creation")
				select {
				case <-hookDone:
					hookDone = nil // wait at most once
					continue
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			if attempt < maxFetchAttempts {
				log.WithError(err).Warnf("Route finder failed (attempt %d/%d), retrying with fresh query...", attempt, maxRetries)
				continue
			}
			if forceLocal {
				return nil, fmt.Errorf("local route calc: %w", err)
			}
			return nil, fmt.Errorf("route finder: %w", err)
		}

		// Last gate for RequireDisjointFirstHop. fetchBestRoutes reports
		// exhaustion from its own candidate list, but a route can also reach
		// here from the direct-transport hook race, the K-candidate race or a
		// local calc. Checking the path we are ABOUT to set up covers every one
		// of them, and costs a set lookup on a dial that is already seconds long.
		if opts != nil && opts.DiversifyTransports && opts.RequireDisjointFirstHop && r.firstHopExcluded(forwardPath, opts) {
			opts.note("required disjoint first hop: picked route shares %s; settling", forwardPath[0].TpID.String()[:8])
			return nil, noDisjointFirstHopErr(rPK, 1)
		}

		// CLAIM the first hop before setting the route up. The gate above tests
		// against a snapshot taken when this dial started; a sibling dial that
		// started at the same instant read the same empty snapshot and may be
		// about to set up over this very transport. The claim is the one place
		// where the test and the reservation happen together, so exactly one of
		// the two wins it and the other re-picks (#5125).
		if opts != nil && opts.DiversifyTransports && len(forwardPath) > 0 {
			release, claimed := r.claimFirstHop(rPK, rPort, forwardPath)
			if !claimed {
				opts.note("first hop %s is claimed by an in-flight sibling dial; re-picking", forwardPath[0].TpID.String()[:8])
				opts.ExcludeTransportIDs = append(opts.ExcludeTransportIDs, forwardPath[0].TpID)
				opts.ExcludeFirstHopPeers = append(opts.ExcludeFirstHopPeers, forwardPath[0].To)
				if attempt < maxFetchAttempts {
					continue
				}
				if opts.RequireDisjointFirstHop {
					return nil, noDisjointFirstHopErr(rPK, 1)
				}
			} else {
				claimReleases = append(claimReleases, release)
			}
		}

		keepAlive := DefaultRouteKeepAlive
		if opts != nil && opts.KeepAlive > 0 {
			keepAlive = opts.KeepAlive
		}
		req := routing.BidirectionalRoute{
			Desc:      forwardDesc,
			KeepAlive: keepAlive,
			Forward:   forwardPath,
			Reverse:   reversePath,
		}

		// Bound this single setup attempt so a setup-node / cascade RPC that
		// accepts but never replies can't wedge the whole dial (and the app in
		// "starting"). On timeout we fall through to the retry-with-exclude loop
		// below, which re-fetches a fresh route and tries again. See
		// routeSetupDialTimeout.
		setupCtx := ctx
		if escalateToLegacy {
			setupCtx = WithForceLegacyRouteSetup(ctx)
		}
		dialCtx, cancelDial := context.WithTimeout(setupCtx, routeSetupDialTimeout)
		rules, connectedNode, err := r.conf.RouteGroupDialer.Dial(dialCtx, log, r.dmsgC, r.conf.SetupNodes, req)
		cancelDial()
		if err != nil {
			// If the PARENT context (the overall dial deadline) is done, stop
			// retrying and surface it — the per-attempt timeout above only bounds
			// one attempt, this bounds the whole dial so the caller's Dial RPC
			// returns and the app's --reconnect loop can re-try from scratch.
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// On dial failure, attribute to the intermediate that broke
			// id_reservation and exclude it from the NEXT fetchBestRoutes
			// pick. The router.DialError carries the PK that failed.
			// Source / destination PKs are excluded from the exclude-set
			// (the dst can't be subbed out, and src failures are local).
			// This composes with #2750's per-intermediate breaker: the
			// intermediate's breaker counter ticks on this failure too,
			// so repeated failures eventually short-circuit it via
			// AllowIntermediate.
			//
			// We exclude only the SINGLE intermediate identified by the
			// DialError's PK per attempt — not the entire failed path.
			// id_reservation hops sequentially and fails fast on the
			// first unreachable peer, so the DialError's PK pinpoints
			// the actual blocker. Excluding the rest of the path's
			// intermediates would over-narrow the route-finder's
			// candidate set on multi-hop paths where ALL intermediates
			// except one might be healthy. If the next pick happens to
			// reuse one of those still-healthy intermediates, that's a
			// feature: cheap path through a known-good node.
			if failedPK, ok := extractFailedIntermediatePK(err, lPK, rPK); ok {
				opts = appendExcludeIntermediate(opts, failedPK)
				log.WithError(err).Warnf("Route setup failed on intermediate %s (attempt %d/%d); excluding from next route-finder pick",
					failedPK, attempt, maxRetries)
			}
			if attempt < maxRetries {
				log.WithError(err).Warnf("Route setup failed (attempt %d/%d), retrying with fresh route...", attempt, maxRetries)
				continue
			}
			log.WithError(err).Error("Error dialing route group")
			return nil, err
		}

		// Reorder setup nodes to prioritize the one that worked
		if !connectedNode.Null() {
			r.conf.SetupNodes = ReorderSetupNodes(r.conf.SetupNodes, connectedNode)
		}

		if err := r.SaveRoutingRules(rules.Forward, rules.Reverse); err != nil {
			if attempt < maxRetries {
				log.WithError(err).Warnf("Saving routing rules failed (attempt %d/%d), retrying with fresh route...", attempt, maxRetries)
				continue
			}
			log.WithError(err).Error("Error saving routing rules")
			return nil, err
		}

		nsConf := noise.Config{
			LocalPK:   r.conf.PubKey,
			LocalSK:   r.conf.SecKey,
			RemotePK:  rPK,
			Initiator: true,
		}

		appName := ""
		datagram := false
		role := ""
		if opts != nil {
			appName = opts.AppName
			datagram = opts.Datagram
			role = opts.TunnelRole
		}
		hsStart := time.Now()
		nrg, err := r.saveRouteGroupRules(ctx, rules, nsConf, appName, role, datagram)
		if err != nil {
			// Remember the ROUTE, not just its intermediates. The per-dial
			// opts exclusions below die with this DialOptions, so the next
			// dial — a standby-pool fill runs a dozen back to back — ranked
			// the same just-died route first all over again. This group never
			// carried a byte: it burned handshakeAwaitTimeout (10s) and closed.
			// See dead_route_cache.go.
			r.deadRoutes.mark(forwardPath, time.Since(hsStart), time.Now())
			// Clean up saved rules on failure
			r.rt.DelRules([]routing.RouteID{rules.Forward.KeyRouteID(), rules.Reverse.KeyRouteID()})
			// Check if context was canceled
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// The dialer (cascade or DMSG) returned SUCCESS — routing rules
			// were installed on every hop — yet saveRouteGroupRules failed:
			// the route's reverse handshake never completed, so the data
			// plane is dead. This is the dominant multihop failure mode: an
			// intermediate that ACKs rule-install (so the source-driven
			// cascade reports success and the dialer never falls back to the
			// legacy setup-node path) but does not actually forward packets.
			//
			// Because the cascade already "succeeded", neither the dialer's
			// own cascade->DMSG fallback nor the old ErrNoSuitableTransport
			// branch fired here, so a single bad intermediate failed the
			// whole dial. Treat a dead route like any other retryable route
			// failure: exclude THIS route's intermediates and re-fetch a
			// fresh route through different ones. Walking candidate
			// intermediates is what turns probabilistic multihop setup into
			// reliable setup. Direct routes (no intermediates) that aren't a
			// stale-TPD error still fail fast — retrying the same direct path
			// to a down peer would only burn the budget.
			deadInter := intermediatePKsOfPath(forwardPath, lPK, rPK)
			// A cascade-installed route that fails its handshake is ALSO the
			// fingerprint of a destination that does not trust cascade route
			// setup (rules ACKed over transports, but the destination — running
			// classic-only setup — rejects the cascade initiator so the
			// reciprocal route-group handshake never completes). Escalate the
			// next attempt to the classic trusted-setup-node path the
			// destination accepts. Worth a retry even for a direct/no-hop route,
			// since this changes WHO installs the rules (an RSN the destination
			// trusts), not which intermediates are used.
			firstLegacyEscalation := false
			if !escalateToLegacy {
				escalateToLegacy = true
				firstLegacyEscalation = true
				log.WithError(err).Warnf("Route data-plane handshake failed (attempt %d/%d); escalating remaining attempts to the classic setup-node path",
					attempt, maxRetries)
			}
			for _, ipk := range deadInter {
				opts = appendExcludeIntermediate(opts, ipk)
			}
			if (len(deadInter) > 0 || errors.Is(err, ErrNoSuitableTransport) || firstLegacyEscalation) && attempt < maxRetries {
				log.WithError(err).Warnf("Route setup failed at handshake/data plane (attempt %d/%d); excluding %d intermediate(s) and retrying with a fresh route",
					attempt, maxRetries, len(deadInter))
				continue
			}
			return nil, fmt.Errorf("saveRouteGroupRules: %w", err)
		}

		// The route group is up: this is the working base. Wire mux
		// growth / self-heal / hooks on top and return.
		return r.finishDial(log, nrg, rules, forwardPath, reversePath, forwardDesc, opts, rPK, lPort, rPort), nil
	}

	// Should never reach here, but handle it gracefully
	return nil, fmt.Errorf("failed to establish route after %d attempts", maxRetries)
}

// finishDial wires the post-setup machinery onto a freshly-established route
// group (the never-dropped working base) and returns it as a net.Conn: stores
// the forward hops, starts the service loops, grows any requested mux legs on
// top (best-effort, in the background — they never unseat the base), applies
// the per-dial distribution policy, and installs the leg-change / rotation /
// self-heal hooks. Shared by the sequential setup path and the parallel race
// winner so both converge to identical post-setup behavior.
func (r *router) finishDial(
	log *logging.Logger,
	nrg *NoiseRouteGroup,
	rules routing.EdgeRules,
	forwardPath, reversePath []routing.Hop,
	forwardDesc routing.RouteDescriptor,
	opts *DialOptions,
	rPK cipher.PubKey,
	lPort, rPort routing.Port,
) net.Conn {
	// Store the complete forward route hops for later retrieval
	nrg.SetForwardHops(forwardPath)
	// A multi-tunnel app labels its own tunnels (active / standby) at dial
	// time; every other dial leaves this empty.
	if opts != nil {
		nrg.rg.SetTunnelRole(opts.TunnelRole)
	}
	// A diversify dial leaves its decision trail on the group (see DialOptions.note).
	if opts != nil && len(opts.dialNotes) > 0 {
		first := "none"
		if len(forwardPath) > 0 {
			first = forwardPath[0].TpID.String()[:8]
		}
		nrg.rg.noteMuxEvent(MuxEvent{Event: MuxEventDialDecision, By: MuxByLocal, LegIndex: 0,
			Reason: strings.Join(opts.dialNotes, "; ") + "; first hop " + first})
	}
	// Record the PRIMARY leg's far-end transport (reversePath[0].TpID) — the one
	// the destination's route group registers for this leg. It is what every aux
	// mux leg planned later must avoid re-using on its own reverse path, since
	// the destination refuses a second leg over a transport already in its group.
	nrg.rg.recordLegRoute(forwardPath, reversePath)

	// A group whose handshake completed can still die seconds later without
	// ever carrying a byte (the peer closes it, or the exit-side app is not
	// there). That is the same evidence as a handshake timeout, so record it
	// the same way — and clear the memory the moment the route proves itself
	// by moving payload. See dead_route_cache.go.
	if r.deadRoutes != nil && len(forwardPath) > 0 {
		route := append([]routing.Hop(nil), forwardPath...)
		nrg.rg.SetCloseObserver(func(age time.Duration, carried uint64) {
			if carried > 0 {
				r.deadRoutes.clear(route)
				return
			}
			r.deadRoutes.mark(route, age, time.Now())
		})
	}

	nrg.rg.startOffServiceLoops()

	log.Debugf("Created new routes to %s on port %d", rPK, lPort)

	// Wire the post-setup leg-change hook (RFC #2882 phase 6).
	// The dial-side DialHook may also implement LegChangeHook;
	// when it does, the route group fires on_leg_change
	// callbacks whenever its leg set mutates after this point
	// (additional aux legs from appendRouteToGroup, or
	// transport-close pruning). This just installs a callback; wire
	// it before spawning the async leg establishment so legs that
	// come up in the background already fire the hook.
	if lch, ok := r.effectiveDialHook(rPort).(LegChangeHook); ok && lch != nil {
		nrg.rg.SetLegChangeHook(lch, DialInfo{
			AppName: opts.AppName,
			PeerPK:  rPK,
			LPort:   lPort,
			RPort:   rPort,
		})
	}

	// Add-leg callback for any multiplexed dial. It closes over the
	// dial context + forwardDesc so a replacement leg matches the
	// original dial shape (same peer, ports, min-hops, app). Used by
	// BOTH the route group's self-healing (restore the degree in the
	// background when a leg dies) AND the periodic rotation hook
	// (policy on_tick / bandwidth-spreading, RFC #2882).
	muxTarget := 1
	if opts != nil {
		if eff := opts.EffectiveMuxRoutes(true); eff > muxTarget {
			muxTarget = eff
		}
		if eff := opts.EffectiveMuxRoutes(false); eff > muxTarget {
			muxTarget = eff
		}
	}
	// A same-LAN destination is best reached over its single direct transport
	// (ms-latency). Growing a warm-standby mux to it forces the aux legs through
	// REMOTE 2-hop intermediates (same-LAN peers are excluded as intermediates),
	// adding latency + rotation churn for zero path diversity — the failure that
	// muxed a 3ms LAN forward across a 11.5s multi-hop leg. f77b928c fixed only
	// the direct-dial hook; this stops the warm-standby GROWTH too. Keep it direct.
	if muxTarget > 1 && r.isSameLANDest(forwardDesc.DstPK()) {
		r.logger.WithField("dst", forwardDesc.DstPK().String()).
			Debug("same-LAN destination: forcing direct (no warm-standby mux)")
		muxTarget = 1
	}
	// A control-plane destination port (dmsgpty, RPC, setup-node, hypervisor,
	// transport-setup, CXO telemetry, …) carries small request/response control
	// traffic, never bulk data — so the bandwidth-spreading warm-standby mux is
	// pure cost: it grows a large pool of multi-hop aux legs whose setup-node
	// dials churn (context deadline exceeded), and a multi-round-trip control
	// handshake (e.g. dmsgpty noise-XK) times out over the churning legs while a
	// single 1-hop request would have completed. Observed live as a 32-leg mux on
	// the pty port (22) that never carried a byte. Keep control channels direct;
	// this is the port-keyed complement to the --direct / same-LAN no-mux gates.
	// A STANDBY tunnel is single-leg from BIRTH. The role is seeded onto the
	// group in saveRouteGroupRules, so this gate — unlike the arbiter's, which
	// only ever sees an established group — is reached with the label already
	// in hand: a pooled tunnel never wires SetSelfHeal at the visor width and
	// never runs establishMuxRoutes, which is where its second leg came from
	// (rig 2026-09-22, 33 leg_added on 30 standby groups, zero pool_leg_taken).
	if clamped := nrg.rg.dialMuxTarget(muxTarget); clamped != muxTarget {
		r.logger.WithField("dst", forwardDesc.DstPK().String()).
			WithField("tunnel_role", nrg.rg.TunnelRole()).
			Debug("standby tunnel: single leg whatever the mux width says")
		muxTarget = clamped
	}
	if muxTarget > 1 && isControlPlanePort(rPort) {
		r.logger.WithField("dst", forwardDesc.DstPK().String()).
			WithField("port", rPort).
			Debug("control-plane destination port: forcing direct (no warm-standby mux)")
		muxTarget = 1
	}
	if muxTarget > 1 {
		// Capture everything the async establishment needs into locals so the
		// goroutine never races the dial return: a COPY of opts, the route
		// descriptor, the route group, and the primary transport id.
		optsCopy := *opts
		fwdDescCopy := forwardDesc
		nrgCapture := nrg
		primaryTpID := rules.Forward.NextTransportID()
		applyAdd := func(excludeHops []string) {
			addCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			excludePKs := make([]cipher.PubKey, 0, len(excludeHops))
			for _, h := range excludeHops {
				var pk cipher.PubKey
				if err := pk.Set(h); err == nil {
					excludePKs = append(excludePKs, pk)
				}
			}
			if err := r.addOneAuxForwardLeg(addCtx, nrgCapture, &optsCopy, fwdDescCopy, excludePKs); err != nil {
				r.logger.WithError(err).Debug("Mux add-leg failed; route group keeps current leg set")
			}
		}
		// Self-healing: any leg death triggers a background replacement
		// dial to restore the requested degree, while surviving legs
		// carry the traffic. General to every mux dial, not just ones
		// with a rotation policy. This only installs the callback (no
		// dialing) — wire it before the async establishment below so the
		// machinery is ready as legs appear.
		nrg.rg.SetSelfHeal(applyAdd, muxTarget)

		// Periodic rotation (policy on_tick) reuses the same callback.
		// Like SetSelfHeal, this only installs a callback / starts the
		// rotation ticker — no dialing — so it stays synchronous.
		if rh, ok := r.effectiveDialHook(rPort).(RotationHook); ok && rh != nil && opts.RotationIntervalSeconds > 0 {
			interval := time.Duration(opts.RotationIntervalSeconds) * time.Second
			// Forward-only add-leg callback: the adaptive preset's AddForwardLeg
			// (upload-saturation widen) dials an aux leg addFwd=true/addRev=false
			// so the extra upstream send capacity does not enlarge the
			// reverse/download set. Distinct from applyAdd (full-duplex) so the
			// two directions size independently.
			applyAddForward := func(excludeHops []string) {
				addCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				excludePKs := make([]cipher.PubKey, 0, len(excludeHops))
				for _, h := range excludeHops {
					var pk cipher.PubKey
					if err := pk.Set(h); err == nil {
						excludePKs = append(excludePKs, pk)
					}
				}
				if err := r.addOneAuxSendLeg(addCtx, nrgCapture, &optsCopy, fwdDescCopy, excludePKs); err != nil {
					r.logger.WithError(err).Debug("Mux forward add-leg failed; route group keeps current leg set")
				}
			}
			nrg.rg.SetRotation(rh, applyAdd, applyAddForward, interval)
		}

		// SLOW part: establishing the foreground mux legs plans each aux
		// leg through the route-finder, which can block for tens of seconds
		// when the RF times out. Run it in the background so Dial returns as
		// soon as the primary route + route group are up — the app serves on
		// the primary immediately and the mux legs fill in behind it. Use a
		// fresh background context: the dial's ctx may be canceled once Dial
		// returns, which would abort the establishment mid-flight.
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			r.establishMuxRoutes(bgCtx, nrgCapture, &optsCopy, fwdDescCopy, primaryTpID)

			// Apply per-dial distribution policy AFTER establishMuxRoutes so
			// the selector's rebuild sees every leg, not just the primary.
			// (appendForwardLeg also rebuilds weights on each add, so weights
			// refresh as legs come up regardless; this preserves the ordering
			// to be safe.) No-op when Distribution.Mode is DistributionUnset.
			nrgCapture.rg.applyDistribution(optsCopy.Distribution)

			// Top up an initial shortfall: establishMuxRoutes sets up aux
			// legs best-effort, so a flaky intermediate can leave the group
			// below the requested degree. Heal it now (same mechanism as
			// runtime leg death). Runs after the callbacks were wired above.
			nrgCapture.rg.maybeSelfHeal()
		}()
	} else {
		// No mux: establishMuxRoutes would be a no-op (maxCount <= 1), so
		// just apply the per-dial distribution policy synchronously.
		nrg.rg.applyDistribution(opts.Distribution)
	}

	// NOTE: no MinHops restore needed — baseMinHops is a local var now,
	// r.conf.MinHops is never mutated by the dial path.

	return nrg
}

// setupPingRoute sets up a ping route with the given forward and reverse paths.
// This is the common setup logic used by PingRoute for both calculated and direct transport routes.
func (r *router) setupPingRoute(
	ctx context.Context,
	forwardDesc routing.RouteDescriptor,
	forwardPath, reversePath []routing.Hop,
	rPK cipher.PubKey,
	opts *DialOptions,
) (net.Conn, error) {
	log := r.scopedLog(forwardDesc.SrcPort())
	keepAlive := DefaultRouteKeepAlive
	if opts != nil && opts.KeepAlive > 0 {
		keepAlive = opts.KeepAlive
	}
	req := routing.BidirectionalRoute{
		Desc:      forwardDesc,
		KeepAlive: keepAlive,
		Forward:   forwardPath,
		Reverse:   reversePath,
	}

	// Debug: log route details before sending to setup node
	log.Debugf("setupPingRoute: Desc.SrcPK=%s, Desc.DstPK=%s", forwardDesc.SrcPK(), forwardDesc.DstPK())
	// Log all forward hops with their transport IDs
	for i, hop := range forwardPath {
		log.Debugf("setupPingRoute: Forward[%d] TpID=%s From=%s To=%s", i, hop.TpID, hop.From, hop.To)
	}
	// Log all reverse hops with their transport IDs
	for i, hop := range reversePath {
		log.Debugf("setupPingRoute: Reverse[%d] TpID=%s From=%s To=%s", i, hop.TpID, hop.From, hop.To)
	}

	rules, connectedNode, err := r.conf.RouteGroupDialer.Dial(ctx, log, r.dmsgC, r.conf.SetupNodes, req)
	if err != nil {
		log.WithError(err).Error("Error dialing ping route group")
		return nil, err
	}

	// Reorder setup nodes to prioritize the one that worked
	if !connectedNode.Null() {
		r.conf.SetupNodes = ReorderSetupNodes(r.conf.SetupNodes, connectedNode)
	}

	if err := r.SaveRoutingRules(rules.Forward, rules.Reverse); err != nil {
		log.WithError(err).Error("Error saving ping routing rules")
		return nil, err
	}

	nsConf := noise.Config{
		LocalPK:   r.conf.PubKey,
		LocalSK:   r.conf.SecKey,
		RemotePK:  rPK,
		Initiator: true,
	}

	appName := ""
	datagram := false
	role := ""
	if opts != nil {
		appName = opts.AppName
		datagram = opts.Datagram
		role = opts.TunnelRole
	}
	nrg, err := r.saveRouteGroupRules(ctx, rules, nsConf, appName, role, datagram)
	if err != nil {
		// Clean up saved rules if route group setup fails
		r.rt.DelRules([]routing.RouteID{rules.Forward.KeyRouteID(), rules.Reverse.KeyRouteID()})
		return nil, fmt.Errorf("saveRouteGroupRules: %w", err)
	}

	// Store the complete forward route hops for later retrieval
	nrg.SetForwardHops(forwardPath)

	nrg.rg.startOffServiceLoops()

	lPort := forwardDesc.SrcPort()
	log.Debugf("Created new ping route to %s on port %d", rPK, lPort)

	return nrg, nil
}

// PingRoute dials to a given visor of 'rPK' to establish a ping route.
// Uses the same route-finding and setup machinery as DialRoutes but
// without route setup hooks (transport creation). This tests the routing
// infrastructure directly.
// If opts.TransportID is set, uses that specific transport directly (skips route calculation).
func (r *router) PingRoute(
	ctx context.Context,
	rPK cipher.PubKey,
	lPort, rPort routing.Port,
	opts *DialOptions,
) (net.Conn, error) {

	log := r.scopedLog(lPort)

	if rPK.Null() {
		err := ErrRemoteEmptyPK
		log.WithError(err).Error("Failed to dial ping route.")
		return nil, fmt.Errorf("failed to dial ping route: %w", err)
	}

	if opts == nil {
		opts = DefaultDialOptions()
	}

	lPK := r.conf.PubKey
	forwardDesc := routing.NewRouteDescriptor(lPK, rPK, lPort, rPort)

	// Operator-programmable routing policy. PingRoute is the
	// natural entry point operators use to test mux behavior
	// (`cli visor ping mux-bw --routes N --min-hops 2`); a
	// policy that wants to verify its distribution descriptor
	// applies on every multi-leg setup needs to see those dials
	// too. Same shape as DialRoutes' hook block, minus the
	// post-mux applyDistribution call — setupPingRoute handles
	// distribution + leg-change hook wiring downstream.
	if hook := r.effectiveDialHook(rPort); hook != nil {
		appName := ""
		if opts != nil {
			appName = opts.AppName
		}
		info := DialInfo{
			AppName:      appName,
			PeerPK:       rPK,
			LPort:        lPort,
			RPort:        rPort,
			CLIOverrides: buildCLIOverrides(opts),
		}
		if adj, hookErr := hook.BeforeDial(ctx, info); hookErr != nil {
			log.WithError(hookErr).Debug("policy hook errored on PingRoute; proceeding without adjustment")
		} else if err := applyAdjustment(opts, adj); err != nil {
			log.WithField("policy_decision", "drop").Info("PingRoute refused by routing policy.")
			return nil, err
		} else if adj.MuxRoutes > 0 || adj.MinHops > 0 || adj.Distribution.Mode != DistributionUnset {
			log.
				WithField("policy_mux", adj.MuxRoutes).
				WithField("policy_min_hops", adj.MinHops).
				WithField("policy_distribution", adj.Distribution.Mode).
				Debug("Routing policy adjusted PingRoute opts.")
		}
	}

	// Debug: log what options we received
	log.Debugf("PingRoute opts: TransportID=%s, ForwardHops=%d, ReverseHops=%d", opts.TransportID, len(opts.ForwardHops), len(opts.ReverseHops))

	// If full route is specified, use it directly without route calculation
	if len(opts.ForwardHops) > 0 && len(opts.ReverseHops) > 0 {
		log.Debugf("Using specified %d-hop route to %s", len(opts.ForwardHops), rPK)
		r.lastRouteCalcTime.Store(int64(0)) // No calculation needed
		return r.setupPingRoute(ctx, forwardDesc, opts.ForwardHops, opts.ReverseHops, rPK, opts)
	}

	// If TransportID is specified, use it directly without route calculation (single hop)
	if opts.TransportID != (uuid.UUID{}) {
		log.Debugf("Using specified transport %s for direct route to %s", opts.TransportID, rPK)
		forwardPath := []routing.Hop{{TpID: opts.TransportID, From: lPK, To: rPK}}
		reversePath := []routing.Hop{{TpID: opts.TransportID, From: rPK, To: lPK}}
		r.lastRouteCalcTime.Store(int64(0)) // No calculation needed
		return r.setupPingRoute(ctx, forwardDesc, forwardPath, reversePath, rPK, opts)
	}

	// Same fail-fast logic for ping: with --local-route on, the
	// fetch is deterministic so 3× the same calc just wastes ~3s
	// of cache rebuilds. The "Ping route finder failed" wording is
	// equally misleading when no route finder was queried.
	pingForceLocal := r.forceLocalRoutes.Load()
	// Match DialRoutes: walk up to 6 candidate intermediates (excluding the
	// dead route's hops each time) before giving up, so a flaky intermediate
	// that ACKs install but won't forward doesn't fail the whole probe.
	const maxRetries = 6
	pingMaxFetch := maxRetries
	if pingForceLocal {
		pingMaxFetch = 1
	}
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		// PingRoute has no direct-tp downgrade; use the visor-global base.
		forwardPath, reversePath, err := r.fetchBestRoutes(ctx, log, lPK, rPK, opts, r.conf.MinHops)
		if err != nil {
			if pingForceLocal {
				lastErr = fmt.Errorf("local route calc: %w", err)
			} else {
				lastErr = fmt.Errorf("route finder: %w", err)
			}
			if attempt < pingMaxFetch {
				log.WithError(err).Warnf("Ping route finder failed (attempt %d/%d), retrying...", attempt, maxRetries)
				continue
			}
			return nil, lastErr
		}

		conn, err := r.setupPingRoute(ctx, forwardDesc, forwardPath, reversePath, rPK, opts)
		if err != nil {
			lastErr = err
			// Same retry-with-exclude pattern as DialRoutes — see the
			// note there for the composition with #2750's per-intermediate
			// breaker. The intermediate identified here gets added to
			// opts.ExcludeIntermediatePKs so fetchBestRoutes' next
			// pickDisjointPath call avoids it.
			if failedPK, ok := extractFailedIntermediatePK(err, lPK, rPK); ok {
				opts = appendExcludeIntermediate(opts, failedPK)
				log.WithError(err).Warnf("Ping route setup failed on intermediate %s (attempt %d/%d); excluding from next route-finder pick",
					failedPK, attempt, maxRetries)
			} else {
				// Handshake/data-plane failure: the error names the dst, not
				// the intermediate that ACKed install but won't forward. Exclude
				// the whole dead route's intermediates so the retry diverges —
				// same rationale as DialRoutes' saveRouteGroupRules retry.
				for _, ipk := range intermediatePKsOfPath(forwardPath, lPK, rPK) {
					opts = appendExcludeIntermediate(opts, ipk)
				}
			}
			if attempt < maxRetries {
				log.WithError(err).Warnf("Ping route setup failed (attempt %d/%d), retrying...", attempt, maxRetries)
				continue
			}
			return nil, lastErr
		}

		return conn, nil
	}

	return nil, fmt.Errorf("failed to establish ping route after %d attempts: %w", maxRetries, lastErr)
}

// baseMinHops is the per-dial base min-hops to use when opts carries no
// per-call / per-direction override. The caller (DialRoutes) computes it as a
// LOCAL value — applying the direct-transport downgrade there rather than
// mutating the shared r.conf.MinHops, which used to race across concurrent
// dials and permanently lose the constraint on any early-return error path.
// noRouteErr says which constraint could not be met, wrapping ErrNoRouteFound
// so existing errors.Is checks still hold.
//
// "no route founds" on its own is what a user is left with after setting
// min_hops to 2 or 3 and watching the VPN refuse to connect: it does not say
// that the hop count is what failed, so the setting is the last thing they
// suspect. The number they chose belongs in the message, because it is the
// one thing they can change.
func noRouteErr(fwdMinHops, revMinHops uint16, dst cipher.PubKey) error {
	minHops := fwdMinHops
	if revMinHops > minHops {
		minHops = revMinHops
	}
	if minHops <= 1 {
		return ErrNoRouteFound
	}
	// Hops are transports, so the intermediates are one fewer.
	intermediates := minHops - 1
	plural := "s"
	if intermediates == 1 {
		plural = ""
	}
	return fmt.Errorf("%w: none to %s with at least %d hop%s (min_hops=%d, %d intermediate%s) — "+
		"lower min_hops or wait for a route through more visors",
		ErrNoRouteFound, dst, minHops, plural, minHops, intermediates, plural)
}

// ErrNoDisjointFirstHop is returned to a dial that set
// DialOptions.RequireDisjointFirstHop when the ranked candidate list is
// exhausted: every route this visor can offer to the destination leaves over a
// first-hop transport / peer / IP that a sibling route group to the same
// destination already holds.
//
// It is a SETTLED answer, not a transient failure — the topology is what it is
// — so the caller must treat it as "stop dialing", never as "retry". The
// skysocks standby pool stops filling on it and re-arms only when a tunnel dies
// or the transport set changes; a retry loop against it is exactly the
// setup-node storm of #4325.
var ErrNoDisjointFirstHop = errors.New("no disjoint first-hop route is free")

// noDisjointFirstHopErr wraps ErrNoDisjointFirstHop with the numbers a reader
// needs: how many candidates were considered and which destination they were
// to. Kept separate from the sentinel so errors.Is still matches.
func noDisjointFirstHopErr(dst cipher.PubKey, considered int) error {
	return fmt.Errorf("%w: all %d candidate route(s) to %s leave over a first hop a sibling route group already holds",
		ErrNoDisjointFirstHop, considered, dst)
}

// Carrier classes for the diversify ranking, best first. The FIRST HOP's
// carrier decides a candidate's class, and the class outranks latency
// outright.
//
// Latency alone is the wrong key, measured on the rig 2026-09-17 (campaign21,
// mux-tunnels-2-up2): on a fresh start the ranked sibling dial took a SUDPH
// hop to a fleet peer as the second active tunnel and the pool then filled
// five of six standby slots with more sudph fleet hops, because their
// first-hop latencies beat the rig's stcpr intermediates. Three trials of two
// concurrent 50 MB uploads gave 8.6 + 0.5 MB/s against references of
// 10.1 + 8.8: the sudph tunnel carries about half a megabyte a second however
// fast it answers a ping. A hole-punched UDP flow through two NATs is not the
// same kind of link as a resolved TCP or QUIC connection, and latency does not
// say so.
//
// This is an ORDERING, not a filter: a sudph or webrtc route is still dialed
// and still held in the standby pool, and the promoter can still switch one in
// on measurement. It only stops such a route being taken FIRST while a direct
// one is free.
const (
	carrierClassDirect = 0 // stcpr / squicr / stcp: a resolved TCP or QUIC link this visor dials
	carrierClassHole   = 1 // sudph: UDP hole-punched through both NATs
	carrierClassP2P    = 2 // webrtc: DTLS+SCTP over ICE, browser-reachable
	carrierClassOther  = 3 // dmsg (relayed), swsr / swtr, and anything unknown
)

// baseRouteCandidates mirrors the route-finder's historical default
// route count — request at least this many so non-mux dials keep their
// latency-rank choice. muxRouteHeadroom is the extra routes requested
// on top of the mux degree so the disjoint-path pick can still reach
// the target count when some returned candidates share intermediates.
const (
	baseRouteCandidates = 3
	muxRouteHeadroom    = 2
)

// controlPlanePorts is the set of reserved skywire service ports that carry
// small control / telemetry traffic (never bulk data). A route group whose
// destination lands on one of these must not grow a bandwidth-spreading
// warm-standby mux (see the gate in dialRoutesFwd). Mirrors the reserved
// service ports in pkg/skyenv; app data ports (skysocks 3, skysocks-client 13,
// vpn 43/44, the port-59 forward pool, skychat 1, dynamic ports) are absent so
// they keep their mux.
var controlPlanePorts = map[uint16]struct{}{
	skyenv.DmsgCtrlPort:                  {}, // 7
	skyenv.DmsgPingPort:                  {}, // 8
	skyenv.DmsgPtyPort:                   {}, // 22 — the observed 32-leg-mux victim
	skyenv.DmsgSetupPort:                 {}, // 36
	skyenv.DmsgHypervisorPort:            {}, // 46
	skyenv.DmsgTransportSetupPort:        {}, // 47
	skyenv.DmsgTransportSetupServicePort: {}, // 48
	skyenv.DmsgGRPCPort:                  {}, // 49
	skyenv.DmsgVisorRPCPort:              {}, // 65
	skyenv.DmsgTransportQueryPort:        {}, // 68
	skyenv.DmsgDHTPort:                   {}, // 100
	skyenv.DmsgAwaitSetupPort:            {}, // 136
}

// isControlPlanePort reports whether p is a reserved control/telemetry service
// port (not bulk data) — such route groups stay single-route (no warm-standby
// mux). See controlPlanePorts.
func isControlPlanePort(p routing.Port) bool {
	_, ok := controlPlanePorts[uint16(p)]
	return ok
}

// effectiveDialHook returns the routing-policy DialHook to apply for a dial to
// rPort, or nil when rPort is a control-plane port. Control/telemetry channels
// (pty, dmsgctrl, setup, hypervisor RPC, transport-setup, …) must never run the
// operator's routing policy at all: its per-dial evaluation, warm-standby
// reserve, and reshape/rotation churn destabilize their small noise-XK
// handshakes — the pty (port 22) 32-leg mux that "never carried a byte" and the
// ~seconds-of-reshape that time out a request a single 1-hop route would have
// completed. These channels always take a plain base route. This is the
// policy-application complement to the muxTarget>1 no-mux gate in dialRoutesFwd:
// that keeps an already-policied route from GROWING a mux; this keeps the policy
// from evaluating/managing (BeforeDial/LegChange/Rotation) the route in the
// first place, regardless of which policy (adaptive or a custom preset) is set.
//
// The control-plane exemption is the DEFAULT but not mandatory: setting
// Config.PolicyOnControlPorts opts the policy back in for these ports too, for
// an operator who deliberately wants a policy tuned for control traffic.
func (r *router) effectiveDialHook(rPort routing.Port) DialHook {
	if r.conf.DialHook == nil {
		return nil
	}
	if isControlPlanePort(rPort) && !r.conf.PolicyOnControlPorts {
		return nil
	}
	return r.conf.DialHook
}

// dialPolicyHook returns the routing-policy hook that should adjust THIS dial, or
// nil to skip policy entirely. It layers a --direct exemption over
// effectiveDialHook: a dial with EnsureDirectTransport asked for a single,
// stable, 1-hop direct leg (no route-finder, no setup node, no adaptive mux), so
// it is policy-free by definition. Without this, effectiveDialHook still returns
// the GLOBAL default hook even after the per-app policy override was cleared, and
// that hook re-imposes the adaptive mux (e.g. policy_mux=513) — sending the router
// off to chase RSN-oracle / route-finder aux legs alongside the direct primary,
// which is exactly what --direct exists to avoid.
func (r *router) dialPolicyHook(opts *DialOptions, rPort routing.Port) DialHook {
	if opts != nil && opts.EnsureDirectTransport {
		return nil
	}
	return r.effectiveDialHook(rPort)
}
