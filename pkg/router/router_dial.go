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
	"github.com/skycoin/dmsg/pkg/noise"

	"github.com/skycoin/skywire/pkg/routefinder/rfclient"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
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

	if rPK.Null() {
		err := ErrRemoteEmptyPK
		r.logger.WithError(err).Error("Failed to dial routes.")
		return nil, fmt.Errorf("failed to dial routes: %w", err)
	}

	if r.conf.MinHops == 0 {
		r.logger.Error("Routing disabled. (minhop=0)")
		return nil, fmt.Errorf("routing disabled. (minhop=0)")
	}

	lPK := r.conf.PubKey
	forwardDesc := routing.NewRouteDescriptor(lPK, rPK, lPort, rPort)

	// check if transport exist, then skip minhop value and consider it equal 0
	defaultMinHops := r.conf.MinHops
	if r.isTpdExist(rPK) {
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
		r.logger.Debug("UseExistingTpOnly is set, skipping transport creation hooks")
	}

	// Retry route setup with fresh routes if it fails due to stale TPD data.
	// Route-finder may return routes with non-existent transports (TPD sync issues),
	// so we query for fresh routes on each retry instead of retrying the same bad route.
	const maxRetries = 3
	for attempt := 1; attempt <= maxRetries; attempt++ {
		forwardPath, reversePath, err := r.fetchBestRoutes(ctx, lPK, rPK, opts)
		if err != nil {
			if attempt < maxRetries {
				r.logger.WithError(err).Warnf("Route finder failed (attempt %d/%d), retrying with fresh query...", attempt, maxRetries)
				continue
			}
			return nil, fmt.Errorf("route finder: %w", err)
		}

		req := routing.BidirectionalRoute{
			Desc:      forwardDesc,
			KeepAlive: DefaultRouteKeepAlive,
			Forward:   forwardPath,
			Reverse:   reversePath,
		}

		rules, connectedNode, err := r.conf.RouteGroupDialer.Dial(ctx, r.logger, r.dmsgC, r.conf.SetupNodes, req)
		if err != nil {
			if attempt < maxRetries {
				r.logger.WithError(err).Warnf("Route setup failed (attempt %d/%d), retrying with fresh route...", attempt, maxRetries)
				continue
			}
			r.logger.WithError(err).Error("Error dialing route group")
			return nil, err
		}

		// Reorder setup nodes to prioritize the one that worked
		if !connectedNode.Null() {
			r.conf.SetupNodes = ReorderSetupNodes(r.conf.SetupNodes, connectedNode)
		}

		if err := r.SaveRoutingRules(rules.Forward, rules.Reverse); err != nil {
			if attempt < maxRetries {
				r.logger.WithError(err).Warnf("Saving routing rules failed (attempt %d/%d), retrying with fresh route...", attempt, maxRetries)
				continue
			}
			r.logger.WithError(err).Error("Error saving routing rules")
			return nil, err
		}

		nsConf := noise.Config{
			LocalPK:   r.conf.PubKey,
			LocalSK:   r.conf.SecKey,
			RemotePK:  rPK,
			Initiator: true,
		}

		nrg, err := r.saveRouteGroupRules(ctx, rules, nsConf)
		if err != nil {
			// Clean up saved rules on failure
			r.rt.DelRules([]routing.RouteID{rules.Forward.KeyRouteID(), rules.Reverse.KeyRouteID()})
			// Check if context was canceled
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// Check if this is a "no suitable transport" error (stale TPD data)
			if strings.Contains(err.Error(), "no suitable transport") || strings.Contains(err.Error(), "transport") {
				if attempt < maxRetries {
					r.logger.WithError(err).Warnf("Route handshake failed due to transport issue (attempt %d/%d), querying route-finder for fresh route...", attempt, maxRetries)
					continue
				}
			}
			return nil, fmt.Errorf("saveRouteGroupRules: %w", err)
		}

		// Store the complete forward route hops for later retrieval
		nrg.SetForwardHops(forwardPath)

		nrg.rg.startOffServiceLoops()

		r.logger.Debugf("Created new routes to %s on port %d", rPK, lPort)

		// Establish additional mux routes if requested
		r.establishMuxRoutes(ctx, nrg, opts, forwardDesc, rules.Forward.NextTransportID())

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
	_ *DialOptions,
) (net.Conn, error) {
	req := routing.BidirectionalRoute{
		Desc:      forwardDesc,
		KeepAlive: DefaultRouteKeepAlive,
		Forward:   forwardPath,
		Reverse:   reversePath,
	}

	// Debug: log route details before sending to setup node
	r.logger.Debugf("setupPingRoute: Desc.SrcPK=%s, Desc.DstPK=%s", forwardDesc.SrcPK(), forwardDesc.DstPK())
	// Log all forward hops with their transport IDs
	for i, hop := range forwardPath {
		r.logger.Debugf("setupPingRoute: Forward[%d] TpID=%s From=%s To=%s", i, hop.TpID, hop.From, hop.To)
	}
	// Log all reverse hops with their transport IDs
	for i, hop := range reversePath {
		r.logger.Debugf("setupPingRoute: Reverse[%d] TpID=%s From=%s To=%s", i, hop.TpID, hop.From, hop.To)
	}

	rules, connectedNode, err := r.conf.RouteGroupDialer.Dial(ctx, r.logger, r.dmsgC, r.conf.SetupNodes, req)
	if err != nil {
		r.logger.WithError(err).Error("Error dialing ping route group")
		return nil, err
	}

	// Reorder setup nodes to prioritize the one that worked
	if !connectedNode.Null() {
		r.conf.SetupNodes = ReorderSetupNodes(r.conf.SetupNodes, connectedNode)
	}

	if err := r.SaveRoutingRules(rules.Forward, rules.Reverse); err != nil {
		r.logger.WithError(err).Error("Error saving ping routing rules")
		return nil, err
	}

	nsConf := noise.Config{
		LocalPK:   r.conf.PubKey,
		LocalSK:   r.conf.SecKey,
		RemotePK:  rPK,
		Initiator: true,
	}

	nrg, err := r.saveRouteGroupRules(ctx, rules, nsConf)
	if err != nil {
		// Clean up saved rules if route group setup fails
		r.rt.DelRules([]routing.RouteID{rules.Forward.KeyRouteID(), rules.Reverse.KeyRouteID()})
		return nil, fmt.Errorf("saveRouteGroupRules: %w", err)
	}

	// Store the complete forward route hops for later retrieval
	nrg.SetForwardHops(forwardPath)

	nrg.rg.startOffServiceLoops()

	lPort := forwardDesc.SrcPort()
	r.logger.Debugf("Created new ping route to %s on port %d", rPK, lPort)

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

	if rPK.Null() {
		err := ErrRemoteEmptyPK
		r.logger.WithError(err).Error("Failed to dial ping route.")
		return nil, fmt.Errorf("failed to dial ping route: %w", err)
	}

	if opts == nil {
		opts = DefaultDialOptions()
	}

	lPK := r.conf.PubKey
	forwardDesc := routing.NewRouteDescriptor(lPK, rPK, lPort, rPort)

	// Debug: log what options we received
	r.logger.Debugf("PingRoute opts: TransportID=%s, ForwardHops=%d, ReverseHops=%d", opts.TransportID, len(opts.ForwardHops), len(opts.ReverseHops))

	// If full route is specified, use it directly without route calculation
	if len(opts.ForwardHops) > 0 && len(opts.ReverseHops) > 0 {
		r.logger.Debugf("Using specified %d-hop route to %s", len(opts.ForwardHops), rPK)
		r.lastRouteCalcMu.Lock()
		r.lastRouteCalcTime = 0 // No calculation needed
		r.lastRouteCalcMu.Unlock()
		return r.setupPingRoute(ctx, forwardDesc, opts.ForwardHops, opts.ReverseHops, rPK, opts)
	}

	// If TransportID is specified, use it directly without route calculation (single hop)
	if opts.TransportID != (uuid.UUID{}) {
		r.logger.Debugf("Using specified transport %s for direct route to %s", opts.TransportID, rPK)
		forwardPath := []routing.Hop{{TpID: opts.TransportID, From: lPK, To: rPK}}
		reversePath := []routing.Hop{{TpID: opts.TransportID, From: rPK, To: lPK}}
		r.lastRouteCalcMu.Lock()
		r.lastRouteCalcTime = 0 // No calculation needed
		r.lastRouteCalcMu.Unlock()
		return r.setupPingRoute(ctx, forwardDesc, forwardPath, reversePath, rPK, opts)
	}

	const maxRetries = 3
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		forwardPath, reversePath, err := r.fetchBestRoutes(ctx, lPK, rPK, opts)
		if err != nil {
			lastErr = fmt.Errorf("route finder: %w", err)
			if attempt < maxRetries {
				r.logger.WithError(err).Warnf("Ping route finder failed (attempt %d/%d), retrying...", attempt, maxRetries)
				continue
			}
			return nil, lastErr
		}

		conn, err := r.setupPingRoute(ctx, forwardDesc, forwardPath, reversePath, rPK, opts)
		if err != nil {
			lastErr = err
			if attempt < maxRetries {
				r.logger.WithError(err).Warnf("Ping route setup failed (attempt %d/%d), retrying...", attempt, maxRetries)
				continue
			}
			return nil, lastErr
		}

		return conn, nil
	}

	return nil, fmt.Errorf("failed to establish ping route after %d attempts: %w", maxRetries, lastErr)
}

// MeasureTransportLatency measures the latency of a specific transport by creating
// a temporary direct route over that transport, performing multiple ping/pong measurements,
// and returning latency statistics (min/max/avg). The route is closed after measurement.
func (r *router) MeasureTransportLatency(ctx context.Context, remote cipher.PubKey, tpID uuid.UUID) (float64, error) {
	r.logger.Debugf("Measuring latency for transport %s to %s", tpID, remote)

	// Create ping route using the specific transport (skips route finder)
	opts := &DialOptions{
		TransportID: tpID,
	}

	// Use ephemeral ports for the ping route
	lPort := routing.Port(skyenv.LatencyProbePort)
	rPort := routing.Port(skyenv.LatencyProbePort)

	conn, err := r.PingRoute(ctx, remote, lPort, rPort, opts)
	if err != nil {
		r.logger.WithError(err).Debugf("Failed to establish ping route for latency measurement on transport %s", tpID)
		return 0, fmt.Errorf("failed to establish ping route: %w", err)
	}

	// Ensure we close the route when done
	defer func() {
		if err := conn.Close(); err != nil {
			r.logger.WithError(err).Debug("Failed to close ping route after latency measurement")
		}
	}()

	// Get the RouteGroup to perform actual ping/pong measurements
	rg, ok := conn.(*RouteGroup)
	if !ok {
		// Try NoiseRouteGroup
		if nrg, ok := conn.(*NoiseRouteGroup); ok {
			rg = nrg.rg
		} else {
			return 0, errors.New("unexpected connection type, cannot measure latency")
		}
	}

	// Perform multiple ping measurements (5 pings for good statistics)
	const pingCount = 5
	min, max, avg, err := rg.MeasureLatency(ctx, pingCount)
	if err != nil {
		r.logger.WithError(err).Debugf("Failed to measure latency for transport %s", tpID)
		return 0, fmt.Errorf("failed to measure latency: %w", err)
	}

	r.logger.Debugf("Transport %s latency: min=%.2f ms, max=%.2f ms, avg=%.2f ms", tpID, min, max, avg)

	// Set full stats on the transport if accessible
	r.mx.Lock()
	if tp := r.tm.Transport(tpID); tp != nil {
		tp.SetLatencyStats(transport.LatencyStats{
			Min: min,
			Max: max,
			Avg: avg,
		})
	}
	r.mx.Unlock()

	// Return average for backwards compatibility
	return avg, nil
}

func (r *router) fetchBestRoutes(ctx context.Context, src, dst cipher.PubKey, opts *DialOptions) (fwd, rev []routing.Hop, err error) {
	if opts == nil {
		opts = DefaultDialOptions() // nolint
	}

	// Check if force local routes is enabled
	r.forceLocalRoutesMu.Lock()
	forceLocal := r.forceLocalRoutes
	r.forceLocalRoutesMu.Unlock()

	if forceLocal {
		r.logger.Info("Calculating route locally (--local-route enabled)")
		calcStart := time.Now()
		localFwd, localRev, localErr := r.calculateLocalRoutes(ctx, src, dst, opts)
		calcTime := time.Since(calcStart)
		r.lastRouteCalcMu.Lock()
		r.lastRouteCalcTime = calcTime
		r.lastRouteCalcMu.Unlock()
		if localErr == nil {
			r.logger.Infof("Local route calculated in %v: Forward=%v, Reverse=%v", calcTime, localFwd, localRev)
		}
		return localFwd, localRev, localErr
	}

	retries := opts.Retries

	r.logger.Debugf("Requesting new routes from %s to %s", src, dst)

	timer := time.NewTimer(retryDuration)
	defer timer.Stop()

	forward := [2]cipher.PubKey{src, dst}
	backward := [2]cipher.PubKey{dst, src}

fetchRoutesAgain:
	// Check context before making network calls
	if err := ctx.Err(); err != nil {
		return nil, nil, fmt.Errorf("context canceled before route fetch: %w", err)
	}

	paths, err := r.conf.RouteFinder.FindRoutes(ctx, []routing.PathEdges{forward, backward},
		&rfclient.RouteOptions{MinHops: r.conf.MinHops, MaxHops: r.conf.MaxHops})

	if err == rfclient.ErrTransportNotFound {
		// Try local route calculation - may find a local transport that's not yet in TPD
		r.logger.Info("Route finder returned transport not found, attempting local route calculation...")
		localFwd, localRev, localErr := r.calculateLocalRoutes(ctx, src, dst, opts)
		if localErr == nil {
			r.logger.Infof("Local route calculation succeeded: Forward=%v, Reverse=%v", localFwd, localRev)
			return localFwd, localRev, nil
		}
		r.logger.WithError(localErr).Debug("Local route calculation also failed")
		return nil, nil, err
	}
	// simple retries condition
	if retries == 0 {
		// Try local route calculation as fallback before giving up
		r.logger.Info("Route finder exhausted retries, attempting local route calculation...")
		localFwd, localRev, localErr := r.calculateLocalRoutes(ctx, src, dst, opts)
		if localErr == nil {
			r.logger.Infof("Local route calculation succeeded: Forward=%v, Reverse=%v", localFwd, localRev)
			return localFwd, localRev, nil
		}
		r.logger.WithError(localErr).Warn("Local route calculation also failed")
		r.logger.Errorf(ErrNoRouteFound.Error())
		return nil, nil, ErrNoRouteFound
	}
	if retries > 0 {
		retries--
	}

	if err != nil {
		select {
		case <-timer.C:
			// Try local route calculation as fallback
			r.logger.Info("Route finder timed out, attempting local route calculation...")
			localFwd, localRev, localErr := r.calculateLocalRoutes(ctx, src, dst, opts)
			if localErr == nil {
				r.logger.Infof("Local route calculation succeeded: Forward=%v, Reverse=%v", localFwd, localRev)
				return localFwd, localRev, nil
			}
			r.logger.WithError(localErr).Warn("Local route calculation also failed")
			return nil, nil, err
		default:
			time.Sleep(retryInterval)
			goto fetchRoutesAgain
		}
	}

	r.logger.Debugf("Found routes Forward: %s. Reverse %s", paths[forward], paths[backward])

	return paths[forward][0], paths[backward][0], nil
}

// calculateLocalRoutes attempts to calculate routes locally using the transport manager
// and transport discovery data, without relying on the route finder service.
// It supports 1-hop (direct), 2-hop routes, and self-ping (src == dst).
func (r *router) calculateLocalRoutes(ctx context.Context, src, dst cipher.PubKey, opts *DialOptions) (fwd, rev []routing.Hop, err error) {
	dialOpts := opts
	if r.tm == nil {
		return nil, nil, errors.New("transport manager not available")
	}

	dc := r.tm.Conf.DiscoveryClient
	if dc == nil {
		return nil, nil, errors.New("discovery client not available")
	}

	isSelfPing := src == dst
	r.logger.Debugf("Calculating route locally from %s to %s (self-ping=%v)", src, dst, isSelfPing)

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

	r.logger.Debugf("Found %d local transports", len(localTps))

	// Check for direct (1-hop) route first
	for _, tp := range localTps {
		if tp.remotePK == dst {
			// Skip DMSG transports for mux (DMSG is a relay, not suitable for multiplexing)
			if dialOpts != nil && dialOpts.ExcludeDMSG && tp.tpType == "dmsg" {
				r.logger.Debugf("Skipping DMSG transport %s (excluded for mux)", tp.id)
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
				r.logger.Debugf("Skipping excluded transport %s to destination", tp.id)
				continue
			}
			r.logger.Debugf("Found direct transport to destination: %s (type=%s)", tp.id, tp.tpType)
			fwdHop := routing.Hop{TpID: tp.id, From: src, To: dst}
			revHop := routing.Hop{TpID: tp.id, From: dst, To: src}
			return []routing.Hop{fwdHop}, []routing.Hop{revHop}, nil
		}
	}

	// Build transport cache from single GetAllTransports() call
	// This replaces N individual GetTransportsByEdge API calls with one bulk fetch
	allEntries, err := dc.GetAllTransports(ctx)
	if err != nil {
		r.logger.WithError(err).Warn("Failed to fetch all transports for route calculation")
		return nil, nil, fmt.Errorf("failed to fetch transport discovery data: %w", err)
	}

	// Build lookup map: pubkey -> transports involving that pubkey
	transportsByEdge := make(map[cipher.PubKey][]*transport.Entry)
	for _, entry := range allEntries {
		if entry == nil {
			continue
		}
		for _, edge := range entry.Edges {
			transportsByEdge[edge] = append(transportsByEdge[edge], entry)
		}
	}
	r.logger.Debugf("Built transport cache with %d entries covering %d visors", len(allEntries), len(transportsByEdge))

	// For self-ping, try 2-hop route through any available transport partner
	// This allows testing the full route setup even without a direct self-transport
	if isSelfPing {
		r.logger.Debug("Self-ping: looking for 2-hop loopback route through transport partner")
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
					r.logger.Debugf("Found 2-hop self-ping route via %s (tp1=%s, tp2=%s)", intermediatePK, tp.id, entry.ID)
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

	// Try 2-hop routes through intermediate visors
	for _, tp := range localTps {
		intermediatePK := tp.remotePK

		// Look up transports from cache (built from single GetAllTransports call)
		intermediateEntries := transportsByEdge[intermediatePK]
		if len(intermediateEntries) == 0 {
			continue
		}

		// Check if any of the intermediate visor's transports connect to our destination
		for _, entry := range intermediateEntries {
			if entry == nil {
				continue
			}
			// Check if this transport connects to our destination
			remotePK := entry.RemoteEdge(intermediatePK)
			if remotePK == dst {
				r.logger.Debugf("Found 2-hop route via %s (tp1=%s, tp2=%s)", intermediatePK, tp.id, entry.ID)

				// Build forward route: src -> intermediate -> dst
				fwdHop1 := routing.Hop{TpID: tp.id, From: src, To: intermediatePK}
				fwdHop2 := routing.Hop{TpID: entry.ID, From: intermediatePK, To: dst}

				// Build reverse route: dst -> intermediate -> src
				revHop1 := routing.Hop{TpID: entry.ID, From: dst, To: intermediatePK}
				revHop2 := routing.Hop{TpID: tp.id, From: intermediatePK, To: src}

				return []routing.Hop{fwdHop1, fwdHop2}, []routing.Hop{revHop1, revHop2}, nil
			}
		}
	}

	return nil, nil, errors.New("no route found through local transports")
}

// establishMuxRoutes attempts to establish additional parallel routes for a mux-enabled
// route group. Called after the primary route is established in DialRoutes.
func (r *router) establishMuxRoutes(
	ctx context.Context,
	nrg *NoiseRouteGroup,
	opts *DialOptions,
	forwardDesc routing.RouteDescriptor,
	primaryTpID uuid.UUID,
) {
	muxCount := 1
	if opts != nil && opts.MuxRoutes > 1 {
		muxCount = opts.MuxRoutes
	}
	if muxCount <= 1 || nrg.rg.mux == nil {
		return
	}

	lPK := forwardDesc.SrcPK()
	rPK := forwardDesc.DstPK()
	excludeIDs := []uuid.UUID{primaryTpID}

	for i := 1; i < muxCount; i++ {
		muxOpts := &DialOptions{
			MinForwardRts:       1,
			MaxForwardRts:       1,
			MinConsumeRts:       1,
			MaxConsumeRts:       1,
			Retries:             1,
			ExcludeTransportIDs: excludeIDs,
			ExcludeDMSG:         true,
		}

		muxFwd, muxRev, err := r.fetchBestRoutes(ctx, lPK, rPK, muxOpts)
		if err != nil {
			r.logger.Debugf("Mux route %d/%d: no additional route found: %v", i+1, muxCount, err)
			break
		}

		muxReq := routing.BidirectionalRoute{
			Desc:      forwardDesc,
			KeepAlive: DefaultRouteKeepAlive,
			Forward:   muxFwd,
			Reverse:   muxRev,
		}

		muxRules, _, err := r.conf.RouteGroupDialer.Dial(ctx, r.logger, r.dmsgC, r.conf.SetupNodes, muxReq)
		if err != nil {
			r.logger.Debugf("Mux route %d/%d: setup failed: %v", i+1, muxCount, err)
			break
		}

		if err := r.appendRouteToGroup(nrg, muxRules); err != nil {
			r.logger.Debugf("Mux route %d/%d: append failed: %v", i+1, muxCount, err)
			break
		}

		excludeIDs = append(excludeIDs, muxRules.Forward.NextTransportID())
		r.logger.Infof("Mux route %d/%d established via transport %s", i+1, muxCount, muxRules.Forward.NextTransportID())
	}
}
