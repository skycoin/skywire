// router_dial.go contains route dialing and route finding logic.
package router

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/noise"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/rfclient"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
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
			AppName: appName,
			PeerPK:  rPK,
			LPort:   lPort,
			RPort:   rPort,
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
	defaultMinHops := r.conf.MinHops
	// Suppress the direct-tp downgrade when ANY min-hops constraint is
	// set (symmetric or per-direction). Even if only ReverseMinHops > 1,
	// downgrading globally to 1 would defeat that constraint for the
	// reverse direction's route-finder query below.
	if r.isTpdExist(rPK) && !opts.AnyMinHopsConstraint() {
		r.conf.MinHops = 1
	}

	// Check if existing transport only mode is set on the router
	r.existingTpOnlyMu.Lock()
	routerExistingTpOnly := r.existingTpOnly
	r.existingTpOnlyMu.Unlock()

	// Only run route setup hooks (which may create new transports) if UseExistingTpOnly is false
	// on both the router level and the dial options level
	useExistingOnly := routerExistingTpOnly || (opts != nil && opts.UseExistingTpOnly)
	if r.conf.MinHops == 1 && !useExistingOnly {
		r.routeSetupHookMu.Lock()
		if len(r.routeSetupHooks) != 0 {
			for _, rsf := range r.routeSetupHooks {
				if err := rsf(rPK, r.tm); err != nil {
					r.routeSetupHookMu.Unlock()
					return nil, err
				}
			}
		}
		r.routeSetupHookMu.Unlock()
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
	const maxRetries = 3
	maxFetchAttempts := maxRetries
	if forceLocal {
		maxFetchAttempts = 1
	}
	for attempt := 1; attempt <= maxRetries; attempt++ {
		forwardPath, reversePath, err := r.fetchBestRoutes(ctx, log, lPK, rPK, opts)
		if err != nil {
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

		rules, connectedNode, err := r.conf.RouteGroupDialer.Dial(ctx, log, r.dmsgC, r.conf.SetupNodes, req)
		if err != nil {
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
		if opts != nil {
			appName = opts.AppName
		}
		nrg, err := r.saveRouteGroupRules(ctx, rules, nsConf, appName)
		if err != nil {
			// Clean up saved rules on failure
			r.rt.DelRules([]routing.RouteID{rules.Forward.KeyRouteID(), rules.Reverse.KeyRouteID()})
			// Check if context was canceled
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// Check if this is a "no suitable transport" error (stale TPD data)
			if errors.Is(err, ErrNoSuitableTransport) {
				if attempt < maxRetries {
					log.WithError(err).Warnf("Route handshake failed due to transport issue (attempt %d/%d), querying route-finder for fresh route...", attempt, maxRetries)
					continue
				}
			}
			return nil, fmt.Errorf("saveRouteGroupRules: %w", err)
		}

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

		// reset MinHops default value if changed before
		if defaultMinHops != 1 {
			r.conf.MinHops = defaultMinHops
		}

		return nrg, nil
	}

	// Should never reach here, but handle it gracefully
	return nil, fmt.Errorf("failed to establish route after %d attempts", maxRetries)
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
	if opts != nil {
		appName = opts.AppName
	}
	nrg, err := r.saveRouteGroupRules(ctx, rules, nsConf, appName)
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
	const maxRetries = 3
	pingMaxFetch := maxRetries
	if pingForceLocal {
		pingMaxFetch = 1
	}
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		forwardPath, reversePath, err := r.fetchBestRoutes(ctx, log, lPK, rPK, opts)
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

// MeasureTransportLatency measures the latency of a specific transport by creating
// a temporary direct route over that transport, performing ping/pong measurements,
// and returning average latency. Used as a fallback when the remote visor doesn't
// support transport-level ping frames.
func (r *router) MeasureTransportLatency(ctx context.Context, remote cipher.PubKey, tpID uuid.UUID) (float64, error) {
	r.logger.Debugf("Measuring latency (RSN fallback) for transport %s to %s", tpID, remote)

	opts := &DialOptions{TransportID: tpID}
	lPort := routing.Port(skyenv.LatencyProbePort)
	rPort := routing.Port(skyenv.LatencyProbePort)

	conn, err := r.PingRoute(ctx, remote, lPort, rPort, opts)
	if err != nil {
		return 0, fmt.Errorf("failed to establish ping route: %w", err)
	}
	defer conn.Close() //nolint:errcheck,gosec

	// Extract the RouteGroup for ping measurement.
	rg, ok := conn.(*RouteGroup)
	if !ok {
		if nrg, ok := conn.(*NoiseRouteGroup); ok {
			rg = nrg.rg
		} else {
			return 0, errors.New("unexpected connection type")
		}
	}

	const pingCount = 5
	min, max, avg, err := rg.MeasureLatency(ctx, pingCount)
	if err != nil {
		return 0, fmt.Errorf("failed to measure latency: %w", err)
	}

	r.logger.Debugf("Transport %s latency (RSN): min=%.2f max=%.2f avg=%.2f ms", tpID, min, max, avg)

	r.mx.Lock()
	if tp := r.tm.Transport(tpID); tp != nil {
		tp.SetLatencyStats(transport.LatencyStats{Min: min, Max: max, Avg: avg})
	}
	r.mx.Unlock()

	return avg, nil
}

func (r *router) fetchBestRoutes(ctx context.Context, log *logging.Logger, src, dst cipher.PubKey, opts *DialOptions) (fwd, rev []routing.Hop, err error) {
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
	fwdMinHops := r.conf.MinHops
	revMinHops := r.conf.MinHops
	if fwdEff > 0 {
		fwdMinHops = uint16(fwdEff) //nolint:gosec // bounded by caller; CLI flag values are small
	}
	if revEff > 0 {
		revMinHops = uint16(revEff) //nolint:gosec // bounded by caller; CLI flag values are small
	}

	var paths map[routing.PathEdges][][]routing.Hop
	if fwdMinHops == revMinHops {
		// Common path: single query covers both directions.
		paths, err = r.conf.RouteFinder.FindRoutes(ctx, []routing.PathEdges{forward, backward},
			&rfclient.RouteOptions{MinHops: fwdMinHops, MaxHops: r.conf.MaxHops})
	} else {
		// Asymmetric: two queries with different MinHops per direction.
		// Merge their results into a single map keyed by PathEdges.
		var fwdPaths, revPaths map[routing.PathEdges][][]routing.Hop
		fwdPaths, err = r.conf.RouteFinder.FindRoutes(ctx, []routing.PathEdges{forward},
			&rfclient.RouteOptions{MinHops: fwdMinHops, MaxHops: r.conf.MaxHops})
		if err == nil {
			revPaths, err = r.conf.RouteFinder.FindRoutes(ctx, []routing.PathEdges{backward},
				&rfclient.RouteOptions{MinHops: revMinHops, MaxHops: r.conf.MaxHops})
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
		log.Errorf(ErrNoRouteFound.Error())
		return nil, nil, ErrNoRouteFound
	}
	if retries > 0 {
		retries--
	}

	if err != nil {
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
	latencyFor, typeFor := r.buildHopLookups(ctx, paths[forward], paths[backward])

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
				latSum := pathLatencyScore(hops, latencyFor)
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
				} else if best, ok := pickBestDirection(paths[forward], excludeSet, latencyFor); ok {
					chosenFwd = best
				}
				if haveRev {
					chosenRev = paths[backward][sel.ReverseChosen]
				} else if best, ok := pickBestDirection(paths[backward], excludeSet, latencyFor); ok {
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

	fwdPath, revPath, ok := pickDisjointPath(paths[forward], paths[backward], opts.ExcludeIntermediatePKs, latencyFor)
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
func pickDisjointPath(forward, reverse [][]routing.Hop, exclude []cipher.PubKey, latencyFor func(uuid.UUID) float64) ([]routing.Hop, []routing.Hop, bool) {
	if len(forward) == 0 || len(reverse) == 0 {
		return nil, nil, false
	}
	if len(exclude) == 0 && latencyFor == nil {
		// Hot path: no constraint, no ranker. Preserve the original
		// first-element behavior so back-compat single-route callers
		// are unaffected (tests that don't pass a latencyFor).
		return forward[0], reverse[0], true
	}
	excludeSet := make(map[cipher.PubKey]struct{}, len(exclude))
	for _, pk := range exclude {
		excludeSet[pk] = struct{}{}
	}
	fwdPath, fwdOK := pickBestDirection(forward, excludeSet, latencyFor)
	revPath, revOK := pickBestDirection(reverse, excludeSet, latencyFor)
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
func pickBestDirection(paths [][]routing.Hop, excludeSet map[cipher.PubKey]struct{}, latencyFor func(uuid.UUID) float64) ([]routing.Hop, bool) {
	bestIdx := -1
	bestScore := 0.0
	for i, p := range paths {
		if pathTouchesIntermediate(p, excludeSet) {
			continue
		}
		if latencyFor == nil {
			return p, true
		}
		score := pathLatencyScore(p, latencyFor)
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

// pathLatencyScore returns the sum of per-hop avg-latency-ms across a
// path, treating unknown latencies (latencyFor returning 0) as a high
// cost so paths with measured latency outrank paths with no signal at
// all. The penalty (unknownLatencyCostMs) is set above the practical
// upper bound of healthy single-hop latency — currently 1000ms — so a
// single unknown hop already loses to a known 500ms hop, but a path
// composed entirely of unknown hops is still preferred over no path.
func pathLatencyScore(path []routing.Hop, latencyFor func(uuid.UUID) float64) float64 {
	const unknownLatencyCostMs = 1000.0
	var total float64
	for _, h := range path {
		ms := latencyFor(h.TpID)
		if ms <= 0 {
			total += unknownLatencyCostMs
			continue
		}
		total += ms
	}
	return total
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
func (r *router) buildHopLookups(ctx context.Context, fwd, rev [][]routing.Hop) (latencyFor func(uuid.UUID) float64, typeFor func(uuid.UUID) string) {
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
	for id := range unique {
		// Local transport first.
		if r.tm != nil {
			if tp := r.tm.Transport(id); tp != nil {
				if stats := tp.GetLatencyStats(); stats.Avg > 0 {
					latencyCache[id] = stats.Avg
				}
				typeCache[id] = string(tp.Entry.Type)
				continue
			}
		}
		// TPD fallback — one fetch covers both lookups.
		if r.tm != nil && r.tm.Conf != nil && r.tm.Conf.DiscoveryClient != nil {
			entry, err := r.tm.Conf.DiscoveryClient.GetTransportByID(ctx, id)
			if err != nil {
				r.logger.WithError(err).Debugf("buildHopLookups: GetTransportByID failed for %s", id)
				continue
			}
			if entry == nil {
				continue
			}
			if entry.Latency > 0 {
				latencyCache[id] = entry.Latency
			}
			typeCache[id] = string(entry.Type)
		}
	}
	return func(id uuid.UUID) float64 { return latencyCache[id] },
		func(id uuid.UUID) string { return typeCache[id] }
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
	bfsMinHops := 0
	if dialOpts != nil {
		bfsMinHops = dialOpts.MinHops
		if fwdEff := dialOpts.EffectiveMinHops(true); fwdEff > bfsMinHops {
			bfsMinHops = fwdEff
		}
		if revEff := dialOpts.EffectiveMinHops(false); revEff > bfsMinHops {
			bfsMinHops = revEff
		}
	}
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

	// Build transport cache from single GetAllTransports() call
	// This replaces N individual GetTransportsByEdge API calls with one bulk fetch
	allEntries, err := dc.GetAllTransports(ctx)
	if err != nil {
		log.WithError(err).Warn("Failed to fetch all transports for route calculation")
		return nil, nil, fmt.Errorf("failed to fetch transport discovery data: %w", err)
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
	minHops := 2
	// Use bfsMinHops computed above (max across symmetric + per-
	// direction). Local-BFS mirrors forward→reverse so both directions
	// share one path; satisfying the higher constraint ensures the
	// stricter direction is honored.
	if bfsMinHops > 0 {
		minHops = bfsMinHops
	} else if dialOpts != nil && dialOpts.MinHops > 0 {
		minHops = dialOpts.MinHops
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
			bestScore := pathLatencyScore(best, localLatencyFor)
			for _, p := range dstCandidates[1:] {
				if s := pathLatencyScore(p, localLatencyFor); s < bestScore {
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

	for i := 1; i < maxCount; i++ {
		// Per-iteration direction selection: extend forward only when
		// this leg is needed for fwdCount, reverse only when needed
		// for revCount. When both are false we skip the entire setup
		// (rare; only happens if asymmetric counts disagree on which
		// side is bigger mid-loop, which can't with the current
		// max() shape — but the guard is cheap).
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

		// NOTE: aux mux routes deliberately DO NOT use #2774's
		// retry-with-exclude-on-failure loop. The mux design is
		// graceful degradation — if aux route i can't establish (no
		// disjoint candidate found, or its dial fails), we stop
		// trying to add more aux routes but the PRIMARY route is
		// already up and the app is serviceable. A reject-the-whole-
		// dial retry loop would punish the user for transient aux
		// flakiness when the primary is healthy. For mux-bw style
		// callers that DO want strict N-route guarantees, the failure
		// surfaces via MuxRouteEstablished events (#2756) and the
		// caller can decide whether to fail or accept partial mux.
		muxFwd, muxRev, err := r.fetchBestRoutes(ctx, log, lPK, rPK, muxOpts)
		if err != nil {
			log.Debugf("Mux route %d/%d: no additional route found: %v", i+1, maxCount, err)
			break
		}

		muxKeepAlive := DefaultRouteKeepAlive
		if opts != nil && opts.KeepAlive > 0 {
			muxKeepAlive = opts.KeepAlive
		}
		muxReq := routing.BidirectionalRoute{
			Desc:      forwardDesc,
			KeepAlive: muxKeepAlive,
			Forward:   muxFwd,
			Reverse:   muxRev,
		}

		muxRules, _, err := r.conf.RouteGroupDialer.Dial(ctx, log, r.dmsgC, r.conf.SetupNodes, muxReq)
		if err != nil {
			log.Debugf("Mux route %d/%d: setup failed: %v", i+1, maxCount, err)
			break
		}

		if err := r.appendRouteAsymmetric(nrg, muxRules, addFwd, addRev); err != nil {
			log.Debugf("Mux route %d/%d: append failed (addFwd=%v addRev=%v): %v", i+1, maxCount, addFwd, addRev, err)
			break
		}

		// Track this route's forward TpID + intermediate-PK set ONLY
		// when we actually appended the forward direction (the
		// reverse-only case doesn't contribute to forward-side
		// disjointness exclusion, since no forward transport was
		// added). For reverse-only legs we still want disjoint reverse
		// paths but the exclude set is the union of all-direction
		// intermediates — accumulating the reverse path's
		// intermediates handles that.
		if addFwd {
			excludeIDs = append(excludeIDs, muxRules.Forward.NextTransportID())
			excludePKs = append(excludePKs, intermediatesOfHops(muxFwd, lPK, rPK)...)
		}
		if addRev {
			excludePKs = append(excludePKs, intermediatesOfHops(muxRev, rPK, lPK)...)
		}
		log.Infof("Mux route %d/%d established (fwd=%v rev=%v) via tp %s",
			i+1, maxCount, addFwd, addRev, muxRules.Forward.NextTransportID())
	}
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
