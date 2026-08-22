//go:build !tinygo || (js && wasm)

// Package router pkg/router/router_dial.go c2-net-routing
// router_dial.go contains route dialing and route finding logic.
package router

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/noise"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/rfclient"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
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

	// Operator-programmable routing policy hook (RFC #2882).
	// When configured, the hook adjusts opts.MuxRoutes /
	// opts.MinHops before route setup, and can refuse the dial
	// entirely via Fallback="drop". A nil hook short-circuits
	// the entire block so policy-free deployments pay zero cost.
	if r.conf.DialHook != nil {
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
		if adj, hookErr := r.conf.DialHook.BeforeDial(ctx, info); hookErr != nil {
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
	// Creation order follows the configured transport preference
	// (routing.transport_preference), same as the order the route/transport
	// SELECTION paths use — one knob decides "which type first" everywhere.
	// Unset preference = the built-in default = stcpr, sudph, dmsg, exactly
	// as this list was hardcoded.
	if opts != nil && opts.EnsureDirectTransport && !r.isTpdExist(rPK) {
		for _, nt := range tptypes.PreferredOrder(tptypes.STCPR, tptypes.SUDPH, tptypes.DMSG) {
			if _, sErr := r.tm.SaveTransport(ctx, rPK, nt, transport.LabelAutomatic); sErr == nil {
				log.WithField("tp_type", nt).WithField("remote", rPK).
					Debug("--direct: created direct transport on demand")
				break
			}
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
	r.existingTpOnlyMu.Lock()
	routerExistingTpOnly := r.existingTpOnly
	r.existingTpOnlyMu.Unlock()

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
	r.forceLocalRoutesMu.Lock()
	forceLocal := r.forceLocalRoutes
	r.forceLocalRoutesMu.Unlock()
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
				if opts != nil {
					appName = opts.AppName
					datagram = opts.Datagram
				}
				dial := func(dctx context.Context, c routing.BidirectionalRoute) (routing.EdgeRules, cipher.PubKey, error) {
					return r.conf.RouteGroupDialer.Dial(dctx, log, r.dmsgC, r.conf.SetupNodes, c)
				}
				handshake := func(hctx context.Context, _ routing.BidirectionalRoute, rules routing.EdgeRules) (*NoiseRouteGroup, error) {
					if err := r.SaveRoutingRules(rules.Forward, rules.Reverse); err != nil {
						return nil, err
					}
					nrg, err := r.saveRouteGroupRules(hctx, rules, nsConf, appName, datagram)
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
				nrg, rules, winIdx, rerr := r.raceCandidateSetup(ctx, log, candidates, dial, handshake, onLoser)
				if rerr == nil {
					return r.finishDial(ctx, log, nrg, rules, candidates[winIdx].Forward, forwardDesc, opts, rPK, lPort, rPort), nil
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
				if f, rv, ok := r.directRoute(lPK, rPK); ok {
					cancelFetch() // direct transport up → abandon the finder query
					log.Debug("Direct transport ready; using 1-hop route (route-finder query abandoned)")
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
		if opts != nil {
			appName = opts.AppName
			datagram = opts.Datagram
		}
		nrg, err := r.saveRouteGroupRules(ctx, rules, nsConf, appName, datagram)
		if err != nil {
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
		return r.finishDial(ctx, log, nrg, rules, forwardPath, forwardDesc, opts, rPK, lPort, rPort), nil
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
	ctx context.Context,
	log *logging.Logger,
	nrg *NoiseRouteGroup,
	rules routing.EdgeRules,
	forwardPath []routing.Hop,
	forwardDesc routing.RouteDescriptor,
	opts *DialOptions,
	rPK cipher.PubKey,
	lPort, rPort routing.Port,
) net.Conn {
	// Store the complete forward route hops for later retrieval
	nrg.SetForwardHops(forwardPath)

	nrg.rg.startOffServiceLoops()

	log.Debugf("Created new routes to %s on port %d", rPK, lPort)

	// Establish additional mux routes if requested
	r.establishMuxRoutes(ctx, nrg, opts, forwardDesc, rules.Forward.NextTransportID())

	// Apply per-dial distribution policy (from a routing-
	// policy script via DialAdjustment.Distribution, or a
	// CLI caller populating DialOptions.Distribution
	// directly). No-op when Mode is DistributionUnset.
	// Must run AFTER establishMuxRoutes so the selector's
	// rebuild sees every leg, not just the primary.
	nrg.rg.applyDistribution(opts.Distribution)

	// Wire the post-setup leg-change hook (RFC #2882 phase 6).
	// The dial-side DialHook may also implement LegChangeHook;
	// when it does, the route group fires on_leg_change
	// callbacks whenever its leg set mutates after this point
	// (additional aux legs from appendRouteToGroup, or
	// transport-close pruning).
	if lch, ok := r.conf.DialHook.(LegChangeHook); ok && lch != nil {
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
	if muxTarget > 1 {
		optsCopy := *opts
		fwdDescCopy := forwardDesc
		nrgCapture := nrg
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
		// with a rotation policy.
		nrg.rg.SetSelfHeal(applyAdd, muxTarget)

		// Top up an initial shortfall: establishMuxRoutes sets up aux
		// legs best-effort, so a flaky intermediate can leave the group
		// below the requested degree at dial time. Heal it now in the
		// background (same mechanism as runtime leg death) — the dial
		// still returns immediately on the legs that did establish.
		nrg.rg.maybeSelfHeal()

		// Periodic rotation (policy on_tick) reuses the same callback.
		if rh, ok := r.conf.DialHook.(RotationHook); ok && rh != nil && opts.RotationIntervalSeconds > 0 {
			interval := time.Duration(opts.RotationIntervalSeconds) * time.Second
			nrg.rg.SetRotation(rh, applyAdd, interval)
		}
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
	if opts != nil {
		appName = opts.AppName
		datagram = opts.Datagram
	}
	nrg, err := r.saveRouteGroupRules(ctx, rules, nsConf, appName, datagram)
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
	if r.conf.DialHook != nil {
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
		if adj, hookErr := r.conf.DialHook.BeforeDial(ctx, info); hookErr != nil {
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
		r.lastRouteCalcMu.Lock()
		r.lastRouteCalcTime = 0 // No calculation needed
		r.lastRouteCalcMu.Unlock()
		return r.setupPingRoute(ctx, forwardDesc, opts.ForwardHops, opts.ReverseHops, rPK, opts)
	}

	// If TransportID is specified, use it directly without route calculation (single hop)
	if opts.TransportID != (uuid.UUID{}) {
		log.Debugf("Using specified transport %s for direct route to %s", opts.TransportID, rPK)
		forwardPath := []routing.Hop{{TpID: opts.TransportID, From: lPK, To: rPK}}
		reversePath := []routing.Hop{{TpID: opts.TransportID, From: rPK, To: lPK}}
		r.lastRouteCalcMu.Lock()
		r.lastRouteCalcTime = 0 // No calculation needed
		r.lastRouteCalcMu.Unlock()
		return r.setupPingRoute(ctx, forwardDesc, forwardPath, reversePath, rPK, opts)
	}

	// Same fail-fast logic for ping: with --local-route on, the
	// fetch is deterministic so 3× the same calc just wastes ~3s
	// of cache rebuilds. The "Ping route finder failed" wording is
	// equally misleading when no route finder was queried.
	r.forceLocalRoutesMu.Lock()
	pingForceLocal := r.forceLocalRoutes
	r.forceLocalRoutesMu.Unlock()
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

func (r *router) fetchBestRoutes(ctx context.Context, log *logging.Logger, src, dst cipher.PubKey, opts *DialOptions, baseMinHops uint16) (fwd, rev []routing.Hop, err error) {
	if log == nil {
		log = r.logger
	}
	if opts == nil {
		opts = DefaultDialOptions() // nolint
	}

	// Check if force local routes is enabled
	r.forceLocalRoutesMu.Lock()
	forceLocal := r.forceLocalRoutes
	r.forceLocalRoutesMu.Unlock()

	if forceLocal {
		log.Info("Calculating route locally (--local-route enabled)")
		calcStart := time.Now()
		localFwd, localRev, localErr := r.calculateLocalRoutes(ctx, log, src, dst, opts)
		calcTime := time.Since(calcStart)
		r.lastRouteCalcMu.Lock()
		r.lastRouteCalcTime = calcTime
		r.lastRouteCalcMu.Unlock()
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
	if r.conf != nil && (r.conf.EnableRSNOracleRoutes || opts.UseRSNOracle2Hop) && src != dst {
		hi := baseMinHops
		if e := opts.EffectiveMinHops(true); uint16(e) > hi { //nolint:gosec
			hi = uint16(e) //nolint:gosec
		}
		if e := opts.EffectiveMinHops(false); uint16(e) > hi { //nolint:gosec
			hi = uint16(e) //nolint:gosec
		}
		if hi <= 2 {
			if oFwd, oRev, oErr := r.oracle2HopRoutes(ctx, log, src, dst, opts); oErr == nil {
				return oFwd, oRev, nil
			} else if !errors.Is(oErr, errRSNOracleInert) {
				log.WithError(oErr).Debug("RSN-oracle 2-hop path missed; falling through to route finder")
			}
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
	fwdNum := findRouteNum(opts.EffectiveMuxRoutes(true))
	revNum := findRouteNum(opts.EffectiveMuxRoutes(false))

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

	if err == rfclient.ErrTransportNotFound {
		// Try local route calculation - may find a local transport that's not yet in TPD
		log.Info("Route finder returned transport not found, attempting local route calculation...")
		localFwd, localRev, localErr := r.calculateLocalRoutes(ctx, log, src, dst, opts)
		if localErr == nil {
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
	// aggregated by pkg/transport-discovery/cxoaggregator from the
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
func transportCostMs(tpType string, throughputBps float64) float64 {
	if throughputBps <= 0 {
		return transportTypeCostMs(tpType) // no measurement yet — type prior
	}
	switch {
	case throughputBps >= 5_000_000:
		return 0
	case throughputBps >= 1_250_000:
		return 25
	case throughputBps >= 250_000:
		return 100
	case throughputBps >= 50_000:
		return 250
	default:
		return 400 // measured slow — worse than the webrtc prior
	}
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
	const unknownLatencyCostMs = 1000.0
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

// baseRouteCandidates mirrors the route-finder's historical default
// route count — request at least this many so non-mux dials keep their
// latency-rank choice. muxRouteHeadroom is the extra routes requested
// on top of the mux degree so the disjoint-path pick can still reach
// the target count when some returned candidates share intermediates.
const (
	baseRouteCandidates = 3
	muxRouteHeadroom    = 2
)

// findRouteNum returns the per-edge route count to request from the
// route-finder for a dial with the given effective mux degree. Zero
// (mux off) returns 0, letting the finder apply its own default. A
// positive mux degree returns mux+headroom, floored at the historical
// default so we never request fewer candidates than before. The
// finder clamps the value to its own ceiling.
func findRouteNum(mux int) uint16 {
	if mux <= 0 {
		return 0
	}
	n := mux + muxRouteHeadroom
	if n < baseRouteCandidates {
		n = baseRouteCandidates
	}
	return uint16(n) //nolint:gosec // mux degree is a small CLI-bounded value
}

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
				continue
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
		snap, err := r.tpdCache.snapshot(ctx, r.tm.Conf.DiscoveryClient.GetAllTransports)
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
				if entry.Latency > 0 {
					latencyCache[id] = entry.Latency
				}
				typeCache[id] = string(entry.Type)
				if entry.ThroughputBps > 0 {
					throughputCache[id] = entry.ThroughputBps
				}
			}
		}
	}

	return func(id uuid.UUID) float64 { return latencyCache[id] },
		func(id uuid.UUID) string { return typeCache[id] },
		func(id uuid.UUID) float64 { return throughputCache[id] }
}

// rejectDMSGMultihop returns paths with any multihop candidate
// containing a DMSG hop removed. Single-hop DMSG candidates are
// kept (direct DMSG dials are fine — both endpoints know who
// they're talking to). Stable order — survivors retain their
// original index, so downstream rank/pick logic isn't perturbed.
//
// Returns the input slice unchanged when nothing was filtered, to
// avoid an allocation in the common case.
func rejectDMSGMultihop(paths [][]routing.Hop, typeFor func(uuid.UUID) string) [][]routing.Hop {
	if len(paths) == 0 || typeFor == nil {
		return paths
	}
	anyDropped := false
	for _, p := range paths {
		if len(p) <= 1 {
			continue
		}
		for _, h := range p {
			if typeFor(h.TpID) == "dmsg" {
				anyDropped = true
				break
			}
		}
		if anyDropped {
			break
		}
	}
	if !anyDropped {
		return paths
	}
	out := make([][]routing.Hop, 0, len(paths))
	for _, p := range paths {
		if len(p) > 1 {
			drop := false
			for _, h := range p {
				if typeFor(h.TpID) == "dmsg" {
					drop = true
					break
				}
			}
			if drop {
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

// pathTouchesIntermediate reports whether route hops contain any PK
// in the exclude set, considering only INTERMEDIATE hops — the first
// hop's From is the source and the last hop's To is the destination;
// both are endpoints, not intermediates.
func pathTouchesIntermediate(path []routing.Hop, exclude map[cipher.PubKey]struct{}) bool {
	if len(path) == 0 {
		return false
	}
	// Intermediate PKs are the "To" of every hop except the last,
	// which is the destination endpoint. Equivalently: the "From" of
	// every hop except the first (which is the source endpoint).
	for i := 0; i < len(path)-1; i++ {
		if _, hit := exclude[path[i].To]; hit {
			return true
		}
	}
	return false
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
	type localTp struct {
		id       uuid.UUID
		remotePK cipher.PubKey
		tpType   string
	}
	var localTps []localTp

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
		localTps = append(localTps, localTp{
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
	sort.SliceStable(localTps, func(i, j int) bool {
		return tptypes.TypePreference(tptypes.Type(localTps[i].tpType)) <
			tptypes.TypePreference(tptypes.Type(localTps[j].tpType))
	})

	log.Debugf("Found %d local transports", len(localTps))

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
			// Skip excluded transport IDs (used by mux to get different transports)
			excluded := false
			if dialOpts != nil {
				for _, exID := range dialOpts.ExcludeTransportIDs {
					if tp.id == exID {
						excluded = true
						break
					}
				}
			}
			if excluded {
				log.Debugf("Skipping excluded transport %s to destination", tp.id)
				continue
			}
			log.Debugf("Found direct transport to destination: %s (type=%s)", tp.id, tp.tpType)
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
	var allEntries []*transport.Entry
	if r.tpdCache != nil {
		snap, serr := r.tpdCache.snapshot(ctx, dc.GetAllTransports)
		if snap == nil {
			// Cold-cache failure only — a stale snapshot would be
			// returned non-nil. Nothing to compute routes from.
			log.WithError(serr).Warn("Failed to fetch all transports for route calculation")
			return nil, nil, fmt.Errorf("failed to fetch transport discovery data: %w", serr)
		}
		allEntries = snap.entries
	} else {
		allEntries, err = dc.GetAllTransports(ctx)
		if err != nil {
			log.WithError(err).Warn("Failed to fetch all transports for route calculation")
			return nil, nil, fmt.Errorf("failed to fetch transport discovery data: %w", err)
		}
	}

	// Build lookup map: pubkey -> transports involving that pubkey.
	// Exclude "setup" labeled transports (RSN control-plane only).
	transportsByEdge := make(map[cipher.PubKey][]*transport.Entry)
	for _, entry := range allEntries {
		if entry == nil {
			continue
		}
		if entry.Label == transport.LabelSetup {
			continue
		}
		for _, edge := range entry.Edges {
			transportsByEdge[edge] = append(transportsByEdge[edge], entry)
		}
	}
	// Sort each edge's transport list by type preference so iteration tries
	// direct types before DMSG when picking an intermediate-to-dst hop.
	for edge := range transportsByEdge {
		entries := transportsByEdge[edge]
		sort.SliceStable(entries, func(i, j int) bool {
			return tptypes.TypePreference(entries[i].Type) <
				tptypes.TypePreference(entries[j].Type)
		})
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

	type bfsNode struct {
		pk   cipher.PubKey
		path []routing.Hop
	}

	// Seed the BFS with the local transports (each is a 1-hop path
	// from src to a direct neighbor). Sort for determinism — same
	// seeding order across calls means same expansion order.
	//
	// DMSG seeds are dropped here on purpose. A DMSG transport
	// relays through a dmsg server (and, since server-to-server
	// forwarding landed, potentially a chain of dmsg servers) that
	// neither end of the route can observe — the intermediate
	// server is unaccounted-for in the transport entry. Using a
	// DMSG hop anywhere in a multihop route would let data transit
	// the same dmsg server multiple times with no way to detect it.
	// Direct DMSG dials (1-hop, src→dst over a DMSG transport) are
	// fine and were already handled above; the BFS only produces
	// multihop paths, so we strictly require non-DMSG hops here.
	seed := make([]bfsNode, 0, len(localTps))
	for _, tp := range localTps {
		if tp.tpType == "dmsg" {
			continue
		}
		if _, hit := excludeIntermediates[tp.remotePK]; hit {
			continue
		}
		seed = append(seed, bfsNode{
			pk: tp.remotePK,
			path: []routing.Hop{
				{TpID: tp.id, From: src, To: tp.remotePK},
			},
		})
	}
	sort.SliceStable(seed, func(i, j int) bool {
		return seed[i].pk.String() < seed[j].pk.String()
	})

	// pkInPath returns true if pk already appears as a From or To in
	// the path (loop prevention). Per-path membership, not global —
	// the same intermediate can appear in different paths at
	// different depths.
	pkInPath := func(pk cipher.PubKey, path []routing.Hop) bool {
		if len(path) > 0 && path[0].From == pk {
			return true
		}
		for _, h := range path {
			if h.To == pk {
				return true
			}
		}
		return false
	}

	// expandedAtDepth caches which (pk, depth) pairs we've already
	// expanded — avoids redundant work when multiple inbound paths
	// converge on the same node at the same depth. State capped to
	// O(|visors| × MaxHops), bounded by Config.MaxHops. Without this
	// the search could revisit identical (node, depth) combinations
	// exponentially; with it, each (node, depth) is expanded at most
	// once and the BFS stays linear in graph size.
	expandedAtDepth := make(map[cipher.PubKey]map[int]bool)

	// Per-TpID latency lookup over the entries we just fetched —
	// hydrated by the CXO telemetry aggregator on TPD's side. Used to
	// rank multiple same-level dst-hits below.
	tpLatencyMs := make(map[uuid.UUID]float64)
	for _, entry := range allEntries {
		if entry == nil {
			continue
		}
		if entry.Latency > 0 {
			tpLatencyMs[entry.ID] = entry.Latency
		}
	}
	localLatencyFor := func(id uuid.UUID) float64 { return tpLatencyMs[id] }
	tpTypeOf := make(map[uuid.UUID]string)
	for _, entry := range allEntries {
		if entry != nil {
			tpTypeOf[entry.ID] = string(entry.Type)
		}
	}
	localTypeFor := func(id uuid.UUID) string { return tpTypeOf[id] }
	tpThroughput := make(map[uuid.UUID]float64)
	for _, entry := range allEntries {
		if entry != nil && entry.ThroughputBps > 0 {
			tpThroughput[entry.ID] = entry.ThroughputBps
		}
	}
	localThroughputFor := func(id uuid.UUID) float64 { return tpThroughput[id] }

	queue := seed
	for level := 1; level <= maxHops && len(queue) > 0; level++ {
		nextQueue := make([]bfsNode, 0)
		// Collect ALL dst-hits at this level, then pick the lowest-
		// latency among them. Pre-fix this loop returned on the first
		// hit, leaving the BFS's deterministic-by-PK ordering as the
		// only tiebreaker — which empirically picked geo-distant
		// intermediates over healthier same-hop-count alternatives.
		var dstCandidates [][]routing.Hop
		for _, node := range queue {
			// Did we reach the destination at an acceptable depth?
			if node.pk == dst && level >= minHops {
				dstCandidates = append(dstCandidates, node.path)
				continue
			}
			// Stop expanding nodes already at maxHops — children would
			// exceed the cap.
			if level >= maxHops {
				continue
			}
			// Skip excluded intermediates (DisjointMux).
			if _, hit := excludeIntermediates[node.pk]; hit {
				continue
			}
			// Skip if we've already expanded this (pk, depth) — same
			// children would be produced. Different from the previous
			// global visited[pk] check, which (incorrectly) blocked
			// the same node at OTHER depths and could shadow longer
			// paths when a shorter one existed.
			if expandedAtDepth[node.pk][level] {
				continue
			}
			if expandedAtDepth[node.pk] == nil {
				expandedAtDepth[node.pk] = make(map[int]bool)
			}
			expandedAtDepth[node.pk][level] = true
			// Expand: every transport from this node to a new neighbor
			// becomes a candidate next node, sorted by remote-PK for
			// deterministic order. DMSG entries are skipped — see the
			// seed-loop comment above; a DMSG hop anywhere in a
			// multihop path makes the route unaccountable since the
			// dmsg server (and any server-to-server hops) are
			// invisible to both endpoints.
			entries := transportsByEdge[node.pk]
			children := make([]bfsNode, 0, len(entries))
			for _, entry := range entries {
				if entry == nil {
					continue
				}
				if entry.Type == tptypes.DMSG {
					continue
				}
				nextPK := entry.RemoteEdge(node.pk)
				// Loop prevention: skip when the next PK is already
				// part of this path. Per-path check (not global) —
				// the same intermediate may legitimately appear in
				// other paths at other depths.
				if pkInPath(nextPK, node.path) {
					continue
				}
				newPath := make([]routing.Hop, len(node.path), len(node.path)+1)
				copy(newPath, node.path)
				newPath = append(newPath, routing.Hop{
					TpID: entry.ID,
					From: node.pk,
					To:   nextPK,
				})
				children = append(children, bfsNode{pk: nextPK, path: newPath})
			}
			sort.SliceStable(children, func(i, j int) bool {
				return children[i].pk.String() < children[j].pk.String()
			})
			nextQueue = append(nextQueue, children...)
		}
		// If any dst-candidates were collected at this level, pick the
		// lowest-latency one and return. BFS continues to higher levels
		// only when no dst-hit happened — shorter paths still beat
		// longer ones, as before; the change is purely a tiebreaker
		// among same-hop-count candidates.
		if len(dstCandidates) > 0 {
			best := dstCandidates[0]
			bestScore := pathLatencyScore(best, localLatencyFor, localTypeFor, localThroughputFor)
			for _, p := range dstCandidates[1:] {
				if s := pathLatencyScore(p, localLatencyFor, localTypeFor, localThroughputFor); s < bestScore {
					best = p
					bestScore = s
				}
			}
			log.Debugf("Local BFS found %d-hop route via %v (best of %d same-level candidates, score=%.1fms)",
				level, hopPath(best), len(dstCandidates), bestScore)
			return best, reverseHops(best), nil
		}
		queue = nextQueue
	}

	return nil, nil, fmt.Errorf("local BFS found no path to %s with min_hops=%d max_hops=%d", dst, minHops, maxHops)
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
			MinForwardRts:          1,
			MaxForwardRts:          1,
			MinConsumeRts:          1,
			MaxConsumeRts:          1,
			Retries:                1,
			MinHops:                parentMinHops,
			ForwardMinHops:         parentFwdMinHops,
			ReverseMinHops:         parentRevMinHops,
			AppName:                parentAppName,
			ExcludeTransportIDs:    excludeIDs,
			ExcludeIntermediatePKs: excludePKs,
			ExcludeDMSG:            true,
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

	parentMinHops := 0
	parentFwdMinHops := 0
	parentAppName := ""
	if opts != nil {
		parentMinHops = opts.MinHops
		parentFwdMinHops = opts.ForwardMinHops
		parentAppName = opts.AppName
	}

	muxOpts := &DialOptions{
		MinForwardRts:          1,
		MaxForwardRts:          1,
		MinConsumeRts:          1,
		MaxConsumeRts:          1,
		Retries:                1,
		MinHops:                parentMinHops,
		ForwardMinHops:         parentFwdMinHops,
		AppName:                parentAppName,
		ExcludeTransportIDs:    excludeIDs,
		ExcludeIntermediatePKs: excludePKs,
		ExcludeDMSG:            true,
	}

	// Plan the replacement/added leg over the GLOBAL TPD graph via the
	// route-finder first (same rationale as establishMuxRoutes): local calc is
	// restricted to this visor's live transports (webrtc on a NAT'd client), so
	// a self-healed/rotated leg re-collapses to a slow webrtc first hop unless
	// we let the RF reach fast disjoint stcpr intermediates over the full graph.
	// The disjoint excludes + transport-type-aware ranking keep it disjoint and
	// off webrtc; local calc is the fallback for a disjoint deep-hop the RF misses.
	muxFwd, muxRev, err := r.fetchBestRoutes(ctx, log, lPK, rPK, muxOpts, r.conf.MinHops)
	if err != nil {
		lcFwd, lcRev, lcErr := r.calculateLocalRoutes(ctx, log, lPK, rPK, muxOpts)
		if lcErr != nil {
			return fmt.Errorf("rotation add-leg: no route found (route-finder=%v, local-calc=%v)", err, lcErr)
		}
		muxFwd, muxRev = lcFwd, lcRev
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
	// Append the replacement leg FULL-DUPLEX (forward + reverse). Forward-only
	// deletes the initiator's consume (reverse) rule for this leg — but the
	// setup-node dial already installed that rule on the far end, which marks the
	// leg ready on our forward handshake and then spreads its bulk (download)
	// stream onto it. With the initiator's consume rule gone those packets are
	// dropped (errRouteDescNotExist), so the leg black-holes (recv=0) and, because
	// the reorder buffer is lossless, the missing sequences head-of-line-stall the
	// primary leg too — a net 0-byte transfer. aliveLegCount counts forward legs,
	// so a send-only leg still satisfies the degree target while being a download
	// blackhole. Keeping the reverse rule lets the aggregated download land here.
	if err := r.appendRouteAsymmetric(nrg, muxRules, true, true); err != nil {
		return fmt.Errorf("rotation add-leg: append: %w", err)
	}
	log.Infof("Rotation aux leg established via tp %s", muxRules.Forward.NextTransportID())
	return nil
}

// intermediatesOfHops extracts the set of intermediate-visor PKs
// from a forward hop sequence, excluding source (lPK) and destination
// (rPK). For a 1-hop path src->dst the returned slice is empty (no
// intermediates). For a 2-hop path src->X->dst the returned slice is
// [X]. Helpers below operate on either a raw []routing.Hop or a
// constructed *NoiseRouteGroup.
func intermediatesOfHops(hops []routing.Hop, src, dst cipher.PubKey) []cipher.PubKey {
	if len(hops) <= 1 {
		return nil
	}
	out := make([]cipher.PubKey, 0, len(hops)-1)
	for i := 0; i < len(hops)-1; i++ {
		pk := hops[i].To
		if pk == src || pk == dst {
			continue
		}
		out = append(out, pk)
	}
	return out
}

// intermediatesOfRouteGroup pulls intermediate-visor PKs from the
// transports currently registered in a NoiseRouteGroup. Used to seed
// the DisjointMux exclude set with the primary route's intermediates
// before the auxiliary routes are dialed.
//
// The primary route may already be appended by the time we reach the
// mux loop (it's the route_group's first transport); inspecting the
// transport-manager records gives us the same hop information that
// the original fetchBestRoutes call returned.
func intermediatesOfRouteGroup(nrg *NoiseRouteGroup, src, dst cipher.PubKey) []cipher.PubKey {
	if nrg == nil {
		return nil
	}
	nrg.rg.mu.Lock()
	defer nrg.rg.mu.Unlock()
	var out []cipher.PubKey
	for _, tp := range nrg.rg.tps {
		if tp == nil {
			continue
		}
		// Both edges of a transport are PKs; one is us, the other is
		// the next-hop visor. Add the non-self edge if it's not the
		// final destination.
		for _, pk := range tp.Entry.Edges {
			if pk == src || pk == dst {
				continue
			}
			out = append(out, pk)
		}
	}
	return out
}

// extractFailedIntermediatePK walks an error chain looking for a
// router.DialError and returns its PK if and only if the PK is an
// INTERMEDIATE (i.e., neither the local source nor the remote
// destination). Returns (PK, true) on a real intermediate failure;
// (zero, false) otherwise.
//
// Used by the DialRoutes / PingRoute retry loops to remember which
// intermediates have just failed during id_reservation so the next
// fetchBestRoutes call can route around them via the existing
// ExcludeIntermediatePKs filter (#2746) without a second route-finder
// candidate-selection algorithm. Composes with #2750's per-intermediate
// breaker — that fix tracks repeated failures across calls; this fix
// handles fast within-call fallback.
func extractFailedIntermediatePK(err error, src, dst cipher.PubKey) (cipher.PubKey, bool) {
	if err == nil {
		return cipher.PubKey{}, false
	}
	var de *DialError
	if !errors.As(err, &de) {
		return cipher.PubKey{}, false
	}
	failed := de.PK
	if failed == src || failed == dst {
		return cipher.PubKey{}, false
	}
	return failed, true
}

// appendExcludeIntermediate returns a (potentially new) DialOptions
// with pk added to ExcludeIntermediatePKs. Returns opts unchanged when
// pk is already in the slice (idempotent: re-failure of the same
// intermediate within a single retry cycle won't compound).
//
// MUTATES opts when opts is non-nil — ExcludeIntermediatePKs is grown
// in place via append. Callers that need to preserve the original opts
// must clone before calling. The current call sites (DialRoutes /
// PingRoute retry loops) operate on a single opts value across
// iterations and intentionally want the in-place mutation: carrying
// excluded PKs forward to the next iteration IS the point.
//
// opts is guaranteed non-nil on return so the caller can pass it
// straight back into fetchBestRoutes without a nil check.
func appendExcludeIntermediate(opts *DialOptions, pk cipher.PubKey) *DialOptions {
	if opts == nil {
		opts = DefaultDialOptions()
	}
	for _, existing := range opts.ExcludeIntermediatePKs {
		if existing == pk {
			return opts
		}
	}
	opts.ExcludeIntermediatePKs = append(opts.ExcludeIntermediatePKs, pk)
	return opts
}

// sameLANExcludedPKs returns the PKs of directly-connected peers that sit on
// this visor's own local network, to be excluded as routing INTERMEDIATES when
// routing.exclude_same_lan_hops is enabled (Config.ExcludeSameLANHops). Returns
// nil when the feature is off, the transport manager is absent, or no peer is
// same-LAN. See transport.Manager.SameLANPeers for the same-LAN definition. The
// visor's own public IP (for the NAT-hairpin case) is read from the SelfPublicIP
// getter; a nil getter or nil result still catches private-endpoint peers.
func (r *router) sameLANExcludedPKs() []cipher.PubKey {
	if r.conf == nil || !r.conf.ExcludeSameLANHops || r.tm == nil {
		return nil
	}
	var self net.IP
	if r.conf.SelfPublicIP != nil {
		self = r.conf.SelfPublicIP()
	}
	return r.tm.SameLANPeers(self)
}

// isSameLANDest reports whether dst is a peer on this visor's own local network
// (private-endpoint or NAT-hairpin, per transport.Manager.SameLANPeers). Unlike
// sameLANExcludedPKs (the INTERMEDIATE exclusion, gated on ExcludeSameLANHops),
// this is used for the DESTINATION mux decision and applies regardless of that
// config: a box on our LAN is always best reached over its single direct
// transport, never a warm-standby mux through remote intermediates.
func (r *router) isSameLANDest(dst cipher.PubKey) bool {
	if r.tm == nil {
		return false
	}
	var self net.IP
	if r.conf != nil && r.conf.SelfPublicIP != nil {
		self = r.conf.SelfPublicIP()
	}
	for _, pk := range r.tm.SameLANPeers(self) {
		if pk == dst {
			return true
		}
	}
	return false
}

// appendUniquePKs appends every pk in add that is not already in base,
// preserving order. Nil-safe on both arguments.
func appendUniquePKs(base, add []cipher.PubKey) []cipher.PubKey {
	if len(add) == 0 {
		return base
	}
	seen := make(map[cipher.PubKey]struct{}, len(base))
	for _, pk := range base {
		seen[pk] = struct{}{}
	}
	for _, pk := range add {
		if _, ok := seen[pk]; !ok {
			base = append(base, pk)
			seen[pk] = struct{}{}
		}
	}
	return base
}

// intermediatePKsOfPath returns the distinct intermediate-visor PKs of a
// forward path: every hop.To that is neither the source nor the destination.
// For a direct route (src->dst) it returns nil. Used to exclude a dead
// route's intermediates from the next route-finder pick when a route sets up
// (rules installed on every hop) but its data plane never carries the
// handshake — i.e. one of the intermediates ACKs install but won't forward.
func intermediatePKsOfPath(hops []routing.Hop, src, dst cipher.PubKey) []cipher.PubKey {
	seen := make(map[cipher.PubKey]struct{}, len(hops))
	var out []cipher.PubKey
	for _, h := range hops {
		pk := h.To
		if pk == src || pk == dst {
			continue
		}
		if _, ok := seen[pk]; ok {
			continue
		}
		seen[pk] = struct{}{}
		out = append(out, pk)
	}
	return out
}
