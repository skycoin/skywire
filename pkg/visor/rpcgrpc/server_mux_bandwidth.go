// Package rpcgrpc pkg/visor/rpcgrpc/server_mux_bandwidth.go c3-vis-core
// server-side implementation of the StreamMuxBandwidth gRPC RPC.
//
// Test the operator's hypothesis: bandwidth aggregated across N
// parallel routes (with min_hops >= 2 forcing the routes through
// intermediates) should exceed the bandwidth of the single direct
// transport between the same two visors. This RPC drives N
// concurrent pump goroutines, sums their throughput, and emits
// periodic samples + a terminal Done event.
//
// Pump shape (per route, in its own goroutine):
//  1. Dial (with MinHops constraint when set).
//  2. Pump bytes via PingOnceWithEcho until ctx-cancel or
//     cfg.Duration elapsed. Each call returns (sent, recvd, _, err).
//  3. On goroutine exit, decrement activeRoutes.
//
// Sampler shape (one goroutine total):
//   - On a sample_interval ticker, sum all routes' counters and emit
//     a MuxBandwidthSample event.
//
// Optional probe shape (one goroutine when ProbeRtt is set):
//   - On a probe_interval ticker, run a single PingOnce on the
//     first established route. Emit a MuxRttProbe event per attempt.
//
// All goroutines share a bounded event channel; a single sender
// drains it to stream.Send so events arrive in a serializable order.
package rpcgrpc

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	routeFinder "github.com/skycoin/skywire/pkg/route-finder/store"
	"github.com/skycoin/skywire/pkg/routing"
)

// Defaults for unset MuxBandwidthRequest fields.
const (
	defaultMuxBwRoutes         = 1
	defaultMuxBwDuration       = 30 * time.Second
	defaultMuxBwPacketSizeKb   = 32
	defaultMuxBwSetupTimeout   = 30 * time.Second
	defaultMuxBwProbeInterval  = 100 * time.Millisecond
	defaultMuxBwSampleInterval = 1 * time.Second
	muxBwEventChannelCapacity  = 256
	// muxBwProbeTimeout caps a single RTT probe's round-trip so it cannot
	// block the pump WaitGroup past the measurement window. Shrunk to the
	// remaining ctx per-iteration in muxBwProbeLoop.
	muxBwProbeTimeout = 5 * time.Second
	// defaultMuxBwMaxHops caps the route-finder BFS depth when
	// pre-computing the N disjoint mux routes. Mirrors StreamCalcRoutes'
	// zero-max-hops default so mux-bw's route planning matches what
	// `route calc` would return for the same target.
	defaultMuxBwMaxHops = 5
	// muxBwWarmupDuration is how long to push traffic before measuring,
	// when probing, so the idle baseline and load phases both run on a
	// WARM route (routing rules settled at every hop, hop transports
	// primed). Without it a freshly set-up route is "cold" — early packets
	// dropped (rules not yet active) or slow (setup still completing) —
	// which made the idle baseline measure cold-vs-warm instead of
	// unloaded-vs-loaded (nonsensical negative queueing deltas at 2-3 hops).
	muxBwWarmupDuration = 5 * time.Second
)

// muxBwCfg is the normalized request. Pass this around inside the
// handler instead of the raw proto.
type muxBwCfg struct {
	TargetPK             cipher.PubKey
	Routes               int
	Duration             time.Duration
	PacketSizeKb         int
	MinHops              int
	SetupTimeout         time.Duration
	ProbeRTT             bool
	ProbeInterval        time.Duration
	SampleInterval       time.Duration
	LocalRoute           bool
	IdleBaselineDuration time.Duration // 0 = no pre-pump idle probe phase
}

func normalizeMuxBwRequest(req *MuxBandwidthRequest) (muxBwCfg, error) {
	var c muxBwCfg
	if err := c.TargetPK.Set(req.TargetPk); err != nil {
		return c, fmt.Errorf("invalid target_pk: %w", err)
	}
	c.Routes = int(req.Routes)
	c.Duration = time.Duration(req.DurationNs)
	c.PacketSizeKb = int(req.PacketSizeKb)
	c.MinHops = int(req.MinHops)
	c.SetupTimeout = time.Duration(req.SetupTimeoutNs)
	c.ProbeRTT = req.ProbeRtt
	c.ProbeInterval = time.Duration(req.ProbeIntervalNs)
	c.SampleInterval = time.Duration(req.SampleIntervalNs)
	c.LocalRoute = req.LocalRoute
	c.IdleBaselineDuration = time.Duration(req.IdleBaselineDurationNs)

	// Idle baseline only makes sense when probes are running — force
	// ProbeRTT on so the operator gets the queueing-delay comparison
	// they asked for without remembering to pass both flags.
	if c.IdleBaselineDuration > 0 {
		c.ProbeRTT = true
	}

	if c.Routes <= 0 {
		c.Routes = defaultMuxBwRoutes
	}
	if c.Duration <= 0 {
		c.Duration = defaultMuxBwDuration
	}
	if c.PacketSizeKb <= 0 {
		c.PacketSizeKb = defaultMuxBwPacketSizeKb
	}
	if c.SetupTimeout <= 0 {
		c.SetupTimeout = defaultMuxBwSetupTimeout
	}
	if c.ProbeInterval <= 0 {
		c.ProbeInterval = defaultMuxBwProbeInterval
	}
	if c.SampleInterval <= 0 {
		c.SampleInterval = defaultMuxBwSampleInterval
	}
	return c, nil
}

// muxBwRouteState tracks per-route counters during the pump. Each
// pump goroutine owns one routeState; the sampler reads all of them
// to compute aggregated totals.
type muxBwRouteState struct {
	index       int
	established atomic.Bool
	bytesSent   atomic.Uint64
	bytesRecv   atomic.Uint64
	// activeFlag flips false when the goroutine exits (success or
	// failure). The sampler reads this to compute active_routes.
	activeFlag atomic.Bool
	// fwdHops/revHops, when populated, pin THIS route to an explicit
	// pre-computed DISJOINT path (distinct intermediate visor). Set by
	// the disjoint-route planner before the dial loop so each leg rides
	// its own path instead of N-copies of the finder's single best
	// route. Empty → muxBwSetupRoute falls back to the finder dial
	// (MinHops), preserving prior behavior.
	fwdHops []RouteHopInfo
	revHops []RouteHopInfo
	// intermediatePK / transportKind are this leg's distinguishing
	// identity, captured from the route's chosen FIRST hop during
	// setup (the same hop data the route_established event carries).
	// intermediatePK is the first hop's `to` PK — the first
	// intermediate — left empty for a direct (single-hop) route.
	// transportKind is the first hop's transport type (stcpr, sudph,
	// dmsg, ...). Both are written once in muxBwSetupRoute, on the
	// success path, BEFORE the pump phase begins (the setup
	// WaitGroup happens-before the sampler goroutine), then only read
	// by the sampler — so plain fields are race-free and need no
	// atomics.
	intermediatePK string
	transportKind  string
}

// StreamMuxBandwidth is the canonical implementation of the RPC.
// See ping.proto for the wire contract.
func (s *PingServer) StreamMuxBandwidth(req *MuxBandwidthRequest, stream PingService_StreamMuxBandwidthServer) error {
	ctx := stream.Context()
	cfg, err := normalizeMuxBwRequest(req)
	if err != nil {
		return s.sendMuxBwError(stream, "invalid_request", err.Error())
	}

	// Event channel + sender goroutine — same shape as the
	// ping-tree handler so the operational properties match.
	eventCh := make(chan *MuxBandwidthEvent, muxBwEventChannelCapacity)
	sendErrCh := make(chan error, 1)
	go muxBwSender(stream, eventCh, sendErrCh)

	emit := func(payload isMuxBandwidthEvent_Payload) {
		ev := &MuxBandwidthEvent{
			TimestampNs: time.Now().UnixNano(),
			Payload:     payload,
		}
		select {
		case eventCh <- ev:
		case <-ctx.Done():
		}
	}

	// Phase 0: pre-compute up to cfg.Routes DISJOINT routes to the
	// target — each via a DISTINCT intermediate visor — using the same
	// transport-graph BFS that `route calc --count N` rides. Each leg is
	// then dialed with its explicit ForwardHops/ReverseHops so the N
	// routes are genuinely disjoint rather than N copies of the
	// route-finder's single best path (the bug this fixes: `--routes 4
	// --min-hops 2` returned R0=R1=R2=R3, all through one intermediate).
	//
	// Best-effort: if planning fails (no TPD graph, build error) or
	// yields nothing, we fall back to the per-route finder dial (MinHops)
	// so behavior degrades to the previous path rather than failing.
	plans, planErr := s.muxBwPlanDisjointRoutes(ctx, cfg.TargetPK, cfg.MinHops, defaultMuxBwMaxHops, cfg.Routes)
	if planErr != nil {
		s.log.Debugf("StreamMuxBandwidth: disjoint route planning failed (%v); falling back to finder dial", planErr)
		plans = nil
	}

	// Graceful degradation: when the graph yields fewer disjoint routes
	// than requested, dial the ones found. Done still reports
	// RoutesRequested = cfg.Routes so the shortfall is visible. When
	// planning produced nothing we keep cfg.Routes states on the finder
	// path (unchanged fallback behavior).
	numRoutes := cfg.Routes
	if len(plans) > 0 && len(plans) < numRoutes {
		numRoutes = len(plans)
	}

	// Phase 1: dial all routes in parallel. Each goroutine emits a
	// RouteEstablished event when its setup completes (success or
	// failure). Routes that fail setup are excluded from the pump
	// phase; the pump_time clock starts after ALL setup goroutines
	// have either succeeded or returned setup errors.
	routes := make([]*muxBwRouteState, numRoutes)
	for i := 0; i < numRoutes; i++ {
		routes[i] = &muxBwRouteState{index: i}
		if i < len(plans) {
			routes[i].fwdHops = plans[i].forward
			routes[i].revHops = plans[i].reverse
		}
	}

	// Dedicated probe route. When probing, the RTT probe rides its OWN
	// route/conn (index = cfg.Routes), never a pump route's. PingOnceWithEcho
	// (the pump) clears the conn deadline on every call; on a conn shared
	// with the probe that wiped the probe's read deadline mid-read, so the
	// probe blocked forever and hung the whole run. Kept OUT of `routes` so
	// it is never pumped, sampled, or counted toward throughput.
	var probeRoute *muxBwRouteState
	if cfg.ProbeRTT {
		probeRoute = &muxBwRouteState{index: cfg.Routes}
	}

	setupWg := &sync.WaitGroup{}
	setupStart := time.Now()
	for i := range routes {
		setupWg.Add(1)
		go func(rs *muxBwRouteState) {
			defer setupWg.Done()
			s.muxBwSetupRoute(ctx, &cfg, rs, emit)
		}(routes[i])
	}
	if probeRoute != nil {
		setupWg.Add(1)
		go func(rs *muxBwRouteState) {
			defer setupWg.Done()
			// Suppress this route's RouteEstablished so the dedicated probe
			// route stays invisible in the route list / counts.
			s.muxBwSetupRoute(ctx, &cfg, rs, muxBwDropRouteEstablished(emit))
		}(probeRoute)
	}
	setupWg.Wait()
	setupTotal := time.Since(setupStart)

	// Count how many routes actually came up.
	establishedCount := 0
	for _, r := range routes {
		if r.established.Load() {
			establishedCount++
			r.activeFlag.Store(true)
		}
	}
	if establishedCount == 0 {
		emit(&MuxBandwidthEvent_Done{Done: &MuxBandwidthDone{
			RoutesRequested:   int32(cfg.Routes), //nolint:gosec
			RoutesEstablished: 0,
			SetupTotalNs:      setupTotal.Nanoseconds(),
			TerminationReason: "all_routes_failed",
		}})
		close(eventCh)
		return <-sendErrCh
	}

	// Phase 1.4: warm-up. When probing, push traffic on every established
	// route (real echo bytes on the pump routes to prime their hop
	// transports; small probes on the probe route to settle its rules) for
	// muxBwWarmupDuration, DISCARDING everything. This brings the routes to
	// steady state — rules active at every hop, transports primed — so the
	// idle baseline and load phases measure a WARM route, not the cold
	// just-set-up one. Crucially this does NOT tear the conns down (it
	// avoids muxBwPumpRoute, whose defer closes the route) and does NOT
	// touch the byte counters, so reported throughput is measurement-only.
	if cfg.ProbeRTT {
		warmCtx, warmCancel := context.WithTimeout(ctx, muxBwWarmupDuration)
		warmWg := &sync.WaitGroup{}
		warmRoute := func(index int, echo bool) {
			defer warmWg.Done()
			conf := PingConf{
				PK:         cfg.TargetPK,
				Tries:      1,
				PcktSize:   cfg.PacketSizeKb,
				LocalRoute: cfg.LocalRoute,
				RouteIndex: index,
			}
			for warmCtx.Err() == nil {
				if echo {
					_, _, _, _ = s.visor.PingOnceWithEcho(conf, true) //nolint:errcheck
				} else {
					probeConf := conf
					probeConf.PcktSize = 1
					probeConf.Timeout = muxBwProbeTimeout
					_, _ = s.visor.PingOnce(probeConf) //nolint:errcheck
				}
			}
		}
		for _, r := range routes {
			if r.established.Load() {
				warmWg.Add(1)
				go warmRoute(r.index, true)
			}
		}
		if probeRoute != nil && probeRoute.established.Load() {
			warmWg.Add(1)
			go warmRoute(probeRoute.index, false)
		}
		warmWg.Wait()
		warmCancel()
	}

	// Phase 1.5: optional idle-baseline probe. Runs the probe loop
	// only (no pump goroutines, no sampler) for IdleBaselineDuration.
	// Captures a clean RTT distribution on the same routes the pump
	// will load — letting the caller compute queueing delay as
	// (loaded_pXX - idle_pXX) over identical paths. We bookend it
	// inside a small WaitGroup so the probe goroutine exits cleanly
	// before the pump phase starts.
	idleProbeSamples := make([]int64, 0)
	var idleProbesMu sync.Mutex
	if cfg.IdleBaselineDuration > 0 && probeRoute != nil && probeRoute.established.Load() {
		// Idle baseline rides the SAME dedicated probe route the loaded
		// phase uses, so queueing delay (loaded_pXX - idle_pXX) is an
		// apples-to-apples comparison on identical paths.
		idleCtx, idleCancel := context.WithTimeout(ctx, cfg.IdleBaselineDuration)
		idleStart := time.Now()
		idleWg := &sync.WaitGroup{}
		idleWg.Add(1)
		go func() {
			defer idleWg.Done()
			s.muxBwProbeLoop(idleCtx, &cfg, probeRoute.index, idleStart, &idleProbesMu, &idleProbeSamples, emit)
		}()
		idleWg.Wait()
		idleCancel()
	}

	// Phase 2: pump bytes through each established route + run the
	// sampler + optional probe. All goroutines share the eventCh
	// via the emit closure.
	pumpStart := time.Now()
	pumpCtx, pumpCancel := context.WithTimeout(ctx, cfg.Duration)
	defer pumpCancel()

	// Sampler tracks peaks for the Done event.
	peakSend, peakRecv := 0.0, 0.0
	probeSamples := make([]int64, 0, int(cfg.Duration/cfg.ProbeInterval)+1)
	var probesMu sync.Mutex

	pumpWg := &sync.WaitGroup{}
	for _, r := range routes {
		if !r.established.Load() {
			continue
		}
		pumpWg.Add(1)
		go func(rs *muxBwRouteState) {
			defer pumpWg.Done()
			defer rs.activeFlag.Store(false)
			s.muxBwPumpRoute(pumpCtx, &cfg, rs, pumpStart, emit)
		}(r)
	}

	// Sampler.
	pumpWg.Add(1)
	go func() {
		defer pumpWg.Done()
		s.muxBwSamplerLoop(pumpCtx, &cfg, routes, pumpStart, &peakSend, &peakRecv, emit)
	}()

	// Probe (optional) — rides the dedicated probe route, NOT a pump conn,
	// so the pump's per-call deadline clears can't wipe the probe's read
	// deadline. If the probe route failed setup, probing is skipped (the
	// pump still runs); queueing delay is simply unavailable for that run.
	if cfg.ProbeRTT && probeRoute != nil && probeRoute.established.Load() {
		pumpWg.Add(1)
		go func(idx int) {
			defer pumpWg.Done()
			s.muxBwProbeLoop(pumpCtx, &cfg, idx, pumpStart, &probesMu, &probeSamples, emit)
		}(probeRoute.index)
	}

	pumpWg.Wait()
	pumpDuration := time.Since(pumpStart)

	// Aggregate totals + emit Done.
	var totalSent, totalRecv uint64
	for _, r := range routes {
		totalSent += r.bytesSent.Load()
		totalRecv += r.bytesRecv.Load()
	}
	pumpSec := pumpDuration.Seconds()
	avgSendBps := 0.0
	avgRecvBps := 0.0
	if pumpSec > 0 {
		avgSendBps = float64(totalSent*8) / pumpSec
		avgRecvBps = float64(totalRecv*8) / pumpSec
	}

	// RTT stats over loaded probes (during the pump).
	probesMu.Lock()
	probeCount := len(probeSamples)
	probeAvg, probeP50, probeP99, probeJitter := aggregatePingSamples(int64ToFloat(probeSamples))
	probesMu.Unlock()

	// RTT stats over idle probes (Phase 1.5 baseline). Zero when no
	// idle-baseline phase ran.
	idleProbesMu.Lock()
	idleCount := len(idleProbeSamples)
	idleAvg, idleP50, idleP99, idleJitter := aggregatePingSamples(int64ToFloat(idleProbeSamples))
	idleProbesMu.Unlock()

	terminationReason := "duration"
	if ctx.Err() != nil {
		terminationReason = "context_cancel"
	}

	emit(&MuxBandwidthEvent_Done{Done: &MuxBandwidthDone{
		TotalBytesSent:     totalSent,
		TotalBytesReceived: totalRecv,
		WallTimeNs:         (setupTotal + pumpDuration).Nanoseconds(),
		PumpTimeNs:         pumpDuration.Nanoseconds(),
		AvgSendBps:         avgSendBps,
		AvgRecvBps:         avgRecvBps,
		PeakSendBps:        peakSend,
		PeakRecvBps:        peakRecv,
		RoutesRequested:    int32(cfg.Routes),       //nolint:gosec
		RoutesEstablished:  int32(establishedCount), //nolint:gosec
		SetupTotalNs:       setupTotal.Nanoseconds(),
		ProbeCount:         int32(probeCount), //nolint:gosec
		ProbeAvgNs:         probeAvg,
		ProbeP50Ns:         probeP50,
		ProbeP99Ns:         probeP99,
		ProbeJitterNs:      probeJitter,
		IdleProbeCount:     int32(idleCount), //nolint:gosec
		IdleProbeAvgNs:     idleAvg,
		IdleProbeP50Ns:     idleP50,
		IdleProbeP99Ns:     idleP99,
		IdleProbeJitterNs:  idleJitter,
		TerminationReason:  terminationReason,
	}})

	close(eventCh)
	return <-sendErrCh
}

// muxBwDropRouteEstablished wraps emit to swallow RouteEstablished events.
// Used for the dedicated probe route so it never surfaces as an extra
// "R{N}" in the route list / counts — it exists only to give the RTT
// probe its own conn, isolated from the pump.
func muxBwDropRouteEstablished(emit func(isMuxBandwidthEvent_Payload)) func(isMuxBandwidthEvent_Payload) {
	return func(p isMuxBandwidthEvent_Payload) {
		if _, ok := p.(*MuxBandwidthEvent_RouteEstablished); ok {
			return
		}
		emit(p)
	}
}

// muxBwRoutePlan is one pre-computed disjoint route to the target: the
// forward hop list plus its mirrored reverse. muxBwSetupRoute dials it
// via PingConf.ForwardHops/ReverseHops (explicit path, finder skipped).
type muxBwRoutePlan struct {
	forward []RouteHopInfo
	reverse []RouteHopInfo
}

// muxBwPlanDisjointRoutes computes up to `count` routes to dstPK whose
// intermediate visors are mutually DISJOINT, honoring minHops. It builds
// the transport graph from the same source `route calc` and StreamCalcRoutes
// use (VisorAPI.FetchAllTransportEntries → route-finder BFS), then greedily
// selects shortest-first paths, skipping any whose intermediate PKs overlap
// an already-selected route. This is what turns "N copies of the finder's
// single best route" into a genuine N-way disjoint mux.
//
// Returns fewer than `count` (or an empty slice) when the graph can't yield
// that many disjoint paths — callers degrade gracefully rather than fail. A
// non-nil error means the graph itself was unavailable (no transports, build
// failure); callers fall back to the per-route finder dial.
func (s *PingServer) muxBwPlanDisjointRoutes(
	ctx context.Context,
	dstPK cipher.PubKey,
	minHops, maxHops, count int,
) ([]muxBwRoutePlan, error) {
	if count <= 0 {
		return nil, nil
	}
	if minHops < 0 {
		minHops = 0
	}
	if maxHops <= 0 {
		maxHops = defaultMuxBwMaxHops
	}

	srcPK := s.visor.LocalPK()
	entries, err := s.visor.FetchAllTransportEntries(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetch transports: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no transport entries available")
	}

	// TpID -> carrier kind, so each planned hop can carry its transport
	// type for per-leg telemetry (routing.Hop drops it).
	tpTypes := make(map[string]string, len(entries))
	for _, e := range entries {
		if e != nil {
			tpTypes[e.ID.String()] = string(e.Type)
		}
	}

	graph, err := routeFinder.NewGraphWithDepth(ctx, newCalcMemStore(entries), srcPK, maxHops)
	if err != nil {
		return nil, fmt.Errorf("build graph: %w", err)
	}

	plans := make([]muxBwRoutePlan, 0, count)
	usedInter := make(map[cipher.PubKey]struct{})
	streamErr := graph.StreamRoutesWithCap(ctx, srcPK, dstPK, minHops, maxHops, routeFinder.DefaultMaxBFSQueue,
		func(r routing.Route) bool {
			inter := muxBwIntermediates(r, srcPK, dstPK)
			for _, pk := range inter {
				if _, seen := usedInter[pk]; seen {
					// Shares an intermediate with an already-selected
					// route — not disjoint; keep searching.
					return true
				}
			}
			fwd := muxBwRoutingHopsToInfo(r.Hops, tpTypes)
			plans = append(plans, muxBwRoutePlan{forward: fwd, reverse: muxBwReverseHops(fwd)})
			for _, pk := range inter {
				usedInter[pk] = struct{}{}
			}
			return len(plans) < count
		})
	// ErrRouteNotFound after emitting ≥1 route is a clean end-of-search,
	// not a failure — same translation StreamCalcRoutes applies.
	if streamErr != nil && (len(plans) == 0 || streamErr != routeFinder.ErrRouteNotFound) {
		return plans, streamErr
	}
	return plans, nil
}

// muxBwIntermediates returns the DISTINCT intermediate visor PKs of a route
// — every hop endpoint that is neither the source nor the destination. A
// direct (single-hop) route has none. Used to test two routes for
// intermediate-disjointness.
func muxBwIntermediates(r routing.Route, src, dst cipher.PubKey) []cipher.PubKey {
	seen := make(map[cipher.PubKey]struct{})
	out := make([]cipher.PubKey, 0, len(r.Hops))
	for _, h := range r.Hops {
		for _, pk := range [2]cipher.PubKey{h.From, h.To} {
			if pk == src || pk == dst {
				continue
			}
			if _, ok := seen[pk]; ok {
				continue
			}
			seen[pk] = struct{}{}
			out = append(out, pk)
		}
	}
	return out
}

// muxBwRoutingHopsToInfo converts route-finder routing.Hops into the
// RouteHopInfo wire shape the explicit-hops dial path consumes. TpID/From/To
// drive the dial (PingContextWithRoute resolves the transport by TpID);
// TpType is filled from tpTypes (TpID -> carrier kind) purely so the plan can
// serve as the per-leg identity source for telemetry — the explicit-hops dial
// leaves the conn's RouteHopDetails empty, so the plan is the only reliable
// place to read each leg's intermediate PK + carrier from.
func muxBwRoutingHopsToInfo(hops []routing.Hop, tpTypes map[string]string) []RouteHopInfo {
	out := make([]RouteHopInfo, 0, len(hops))
	for _, h := range hops {
		id := h.TpID.String()
		out = append(out, RouteHopInfo{
			TpID:   id,
			From:   h.From.String(),
			To:     h.To.String(),
			TpType: tpTypes[id],
		})
	}
	return out
}

// muxBwReverseHops mirrors a forward hop list into its reverse: hops in
// reverse order with From/To swapped. Same derivation the CLI's `route calc`
// uses so the reverse leg matches what `proxy mux set --legs` would dial.
func muxBwReverseHops(fwd []RouteHopInfo) []RouteHopInfo {
	rev := make([]RouteHopInfo, len(fwd))
	for i, h := range fwd {
		rev[len(fwd)-1-i] = RouteHopInfo{TpID: h.TpID, From: h.To, To: h.From, TpType: h.TpType}
	}
	return rev
}

// muxBwSetupRoute dials one route + emits a RouteEstablished event.
// Marks rs.established on success so the pump phase knows which
// routes to spin up.
//
// Each route uses a distinct RouteIndex (0..N-1) — the visor's
// per-route conn map (keyed by PingRouteRef) keeps the N parallel
// conns separate. Previously the conn map was keyed by PK alone, so
// concurrent DialPing calls to the same target overwrote each other
// and "N parallel routes" was N parallel uses of one conn.
func (s *PingServer) muxBwSetupRoute(
	ctx context.Context,
	cfg *muxBwCfg,
	rs *muxBwRouteState,
	emit func(isMuxBandwidthEvent_Payload),
) {
	conf := PingConf{
		PK:             cfg.TargetPK,
		Tries:          1,
		PcktSize:       cfg.PacketSizeKb,
		LocalRoute:     cfg.LocalRoute,
		RouteIndex:     rs.index,
		MinHops:        cfg.MinHops, // enforce --min-hops at the router-finder layer
		SetupTimeoutNs: cfg.SetupTimeout.Nanoseconds(),
	}
	// When the disjoint-route planner assigned this leg an explicit path,
	// dial it directly (skips the finder). This is what makes the N routes
	// genuinely disjoint — each rides its own pre-computed distinct-
	// intermediate path. The visor's DialPing explicit-hops branch takes
	// priority over MinHops, so the field stays set only as the fallback
	// signal when no plan exists.
	if len(rs.fwdHops) > 0 && len(rs.revHops) > 0 {
		conf.ForwardHops = rs.fwdHops
		conf.ReverseHops = rs.revHops
	}
	setupCtx, cancel := context.WithTimeout(ctx, cfg.SetupTimeout)
	defer cancel()

	setupStart := time.Now()
	dialDone := make(chan error, 1)
	go func() {
		dialDone <- s.visor.DialPing(conf)
	}()

	var dialErr error
	select {
	case err := <-dialDone:
		dialErr = err
	case <-setupCtx.Done():
		dialErr = fmt.Errorf("setup timeout after %s", cfg.SetupTimeout)
	}

	setupLatency := time.Since(setupStart)

	ev := &MuxRouteEstablished{
		RouteIndex:     int32(rs.index), //nolint:gosec
		SetupLatencyNs: setupLatency.Nanoseconds(),
	}
	if dialErr != nil {
		ev.Failed = true
		ev.SetupErr = dialErr.Error()
	} else {
		rs.established.Store(true)
		// Surface the chosen hops for this specific route. Now that
		// the conn map is keyed by PingRouteRef, GetPingRouteDetailsAt
		// can pick out this route's hops without ambiguity. Consumers
		// (mux-bw NDJSON, mux-bw-tui dashboard, treeprobe harness)
		// can verify route diversity from this field.
		ref := PingRouteRef{PK: cfg.TargetPK, Index: rs.index}
		// Normalize the leg's hops to (tpID, from, to, tpType) tuples. Prefer
		// the conn's recorded details; the explicit-hops dial path (disjoint
		// planner) leaves those empty, so fall back to this leg's plan hops
		// (rs.fwdHops) — authoritative for what we actually dialed.
		type hopTuple struct{ tpID, from, to, tpType string }
		var hopTuples []hopTuple
		for _, h := range s.visor.GetPingRouteDetailsAt(ref) {
			hopTuples = append(hopTuples, hopTuple{h.TpID, h.From, h.To, h.TpType})
		}
		if len(hopTuples) == 0 {
			for _, h := range rs.fwdHops {
				hopTuples = append(hopTuples, hopTuple{h.TpID, h.From, h.To, h.TpType})
			}
		}
		for _, h := range hopTuples {
			ev.Hops = append(ev.Hops, &RouteHop{
				TpId:   h.tpID,
				From:   h.from,
				To:     h.to,
				TpType: h.tpType,
			})
		}
		// Capture the leg's identity from its first hop for the
		// per-leg sampler. transportKind = the carrier this leg
		// egresses on; intermediatePK = the first intermediate visor
		// (first hop's `to`), left empty for a direct route where the
		// only hop lands straight on the target (no intermediate).
		if len(hopTuples) > 0 {
			rs.transportKind = hopTuples[0].tpType
			if len(hopTuples) > 1 {
				rs.intermediatePK = hopTuples[0].to
			}
		}
	}
	emit(&MuxBandwidthEvent_RouteEstablished{RouteEstablished: ev})
}

// muxBwPumpRoute pumps bytes through one route until ctx-cancel or
// pump duration elapsed. Each PingOnceWithEcho call returns the
// bytes shipped; we accumulate into the per-route state atomics so
// the sampler can read them race-free.
func (s *PingServer) muxBwPumpRoute(
	ctx context.Context,
	cfg *muxBwCfg,
	rs *muxBwRouteState,
	pumpStart time.Time,
	emit func(isMuxBandwidthEvent_Payload),
) {
	conf := PingConf{
		PK:         cfg.TargetPK,
		Tries:      1,
		PcktSize:   cfg.PacketSizeKb,
		LocalRoute: cfg.LocalRoute,
		RouteIndex: rs.index,
	}
	defer func() {
		// Per-route teardown — closes JUST this route's conn, not
		// the other parallel routes to the same target. Without
		// this, the first pump goroutine to finish would tear down
		// every aux route's conn, leaving the other pump goroutines
		// chasing closed conns.
		_ = s.visor.StopPingRoute(PingRouteRef{PK: cfg.TargetPK, Index: rs.index}) //nolint:errcheck
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		bytesSent, bytesRecvd, _, err := s.visor.PingOnceWithEcho(conf, true)
		if err != nil {
			// One stalled route doesn't tear down the run — the
			// other routes can keep pumping. Surface the failure
			// via a MuxRouteFailure event so the consumer can see
			// WHY this route's contribution stopped (previously
			// silently logged to the visor's debug log only — the
			// 2026-05-21 mux-via-intermediates measurement was
			// blocked because pump errors were invisible on the
			// wire, leaving the operator unable to attribute zero
			// throughput to a specific cause).
			//
			// Emit when (a) pumpCtx is still alive (clear in-run
			// failure), OR (b) we never moved a byte — a clean
			// shutdown of a healthy pump has bytes > 0, so a
			// zero-bytes exit is unambiguous "route never worked"
			// regardless of pumpCtx timing. Pre-task-#131 this
			// guard suppressed failures when pumpCtx and the
			// per-call deadline expired in the same instant,
			// leaving the operator with no MuxRouteFailure event
			// to explain bytes=0.
			if ctx.Err() == nil || rs.bytesSent.Load() == 0 {
				emit(&MuxBandwidthEvent_RouteFailure{
					RouteFailure: buildRouteFailureEvent(rs, err, pumpStart),
				})
			}
			s.log.Debugf("StreamMuxBandwidth: route %d pump error: %v", rs.index, err)
			return
		}
		rs.bytesSent.Add(bytesSent)
		rs.bytesRecv.Add(bytesRecvd)
	}
}

// buildRouteFailureEvent constructs a MuxRouteFailure proto from the
// pump-route state + the error that caused pumpRoute to exit. Split
// out from the inline emit site so the field-population semantics
// are unit-testable without standing up a PingServer + VisorAPI
// mock. ElapsedNs is captured at emit time so the consumer can see
// where in the pump window the failure landed.
func buildRouteFailureEvent(rs *muxBwRouteState, err error, pumpStart time.Time) *MuxRouteFailure {
	return &MuxRouteFailure{
		RouteIndex:                 int32(rs.index), //nolint:gosec // route index fits int32
		ErrorMessage:               err.Error(),
		BytesSentBeforeFailure:     rs.bytesSent.Load(),
		BytesReceivedBeforeFailure: rs.bytesRecv.Load(),
		ElapsedNs:                  time.Since(pumpStart).Nanoseconds(),
	}
}

// muxBwSamplerLoop ticks every SampleInterval and emits a
// MuxBandwidthSample with cumulative + instant throughput numbers.
// Tracks peak throughput for the Done aggregation.
func (s *PingServer) muxBwSamplerLoop(
	ctx context.Context,
	cfg *muxBwCfg,
	routes []*muxBwRouteState,
	pumpStart time.Time,
	peakSend, peakRecv *float64,
	emit func(isMuxBandwidthEvent_Payload),
) {
	ticker := time.NewTicker(cfg.SampleInterval)
	defer ticker.Stop()

	var lastSent, lastRecv uint64
	lastTick := pumpStart

	// Per-leg previous-sample counters, indexed by position in the
	// routes slice, so each leg's inst_*_bps is a delta since ITS own
	// last reading — mirroring how the aggregate inst rate is computed
	// from lastSent/lastRecv.
	lastLegSent := make([]uint64, len(routes))
	lastLegRecv := make([]uint64, len(routes))

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			elapsed := now.Sub(pumpStart)
			intervalSec := now.Sub(lastTick).Seconds()
			lastTick = now

			var totalSent, totalRecv uint64
			var activeRoutes int32
			// Per-leg breakdown, emitted alongside the aggregate. One
			// entry per route, in route-index order, carrying the leg's
			// cumulative bytes, its per-interval instant rates, and its
			// distinguishing identity (intermediate PK + transport kind).
			legs := make([]*MuxLegSample, 0, len(routes))
			for i, r := range routes {
				legSent := r.bytesSent.Load()
				legRecv := r.bytesRecv.Load()
				totalSent += legSent
				totalRecv += legRecv
				alive := r.activeFlag.Load()
				if alive {
					activeRoutes++
				}

				legInstSendBps := 0.0
				legInstRecvBps := 0.0
				if intervalSec > 0 {
					legInstSendBps = float64((legSent-lastLegSent[i])*8) / intervalSec
					legInstRecvBps = float64((legRecv-lastLegRecv[i])*8) / intervalSec
				}
				lastLegSent[i], lastLegRecv[i] = legSent, legRecv

				legs = append(legs, &MuxLegSample{
					RouteIndex:     int32(r.index), //nolint:gosec // route index fits int32
					IntermediatePk: r.intermediatePK,
					TransportKind:  r.transportKind,
					BytesSent:      int64(legSent), //nolint:gosec // pumped byte totals fit int64
					BytesRecv:      int64(legRecv), //nolint:gosec // pumped byte totals fit int64
					InstSendBps:    legInstSendBps,
					InstRecvBps:    legInstRecvBps,
					Alive:          alive,
				})
			}

			instantSendBps := 0.0
			instantRecvBps := 0.0
			if intervalSec > 0 {
				instantSendBps = float64((totalSent-lastSent)*8) / intervalSec
				instantRecvBps = float64((totalRecv-lastRecv)*8) / intervalSec
			}
			if instantSendBps > *peakSend {
				*peakSend = instantSendBps
			}
			if instantRecvBps > *peakRecv {
				*peakRecv = instantRecvBps
			}
			lastSent, lastRecv = totalSent, totalRecv

			avgSendBps := 0.0
			avgRecvBps := 0.0
			elapsedSec := elapsed.Seconds()
			if elapsedSec > 0 {
				avgSendBps = float64(totalSent*8) / elapsedSec
				avgRecvBps = float64(totalRecv*8) / elapsedSec
			}

			emit(&MuxBandwidthEvent_Sample{Sample: &MuxBandwidthSample{
				ElapsedNs:      elapsed.Nanoseconds(),
				BytesSent:      totalSent,
				BytesReceived:  totalRecv,
				InstantSendBps: instantSendBps,
				InstantRecvBps: instantRecvBps,
				AvgSendBps:     avgSendBps,
				AvgRecvBps:     avgRecvBps,
				ActiveRoutes:   activeRoutes,
				Legs:           legs,
			}})
		}
	}
}

// muxBwProbeLoop runs a small-packet RTT probe concurrently with
// the bulk pump. The probe rides RouteIndex 0 (the primary pump's
// route) — they serialize via the visor's ping mutex so protocol
// framing stays well-formed even though they share the conn.
// Adding a dedicated probe-only route (e.g. RouteIndex = N) is a
// future optimization. Accumulates samples into probesOut for the
// Done aggregation.
func (s *PingServer) muxBwProbeLoop(
	ctx context.Context,
	cfg *muxBwCfg,
	routeIndex int,
	pumpStart time.Time,
	probesMu *sync.Mutex,
	probesOut *[]int64,
	emit func(isMuxBandwidthEvent_Payload),
) {
	ticker := time.NewTicker(cfg.ProbeInterval)
	defer ticker.Stop()

	probeConf := PingConf{
		PK:         cfg.TargetPK,
		Tries:      1,
		PcktSize:   1, // 1 KB probe — small enough to not contribute meaningfully to load
		LocalRoute: cfg.LocalRoute,
		// Probe must use the same RouteIndex as the established
		// route being measured. Hardcoding RouteIndex 0 made every
		// probe fail with "no ping connection" whenever the first
		// established route happened to be at a non-zero index
		// (Phase D's 1-of-8 case where only R2 came up). Caller
		// passes the route index it picked.
		RouteIndex: routeIndex,
	}
	var sequence int64

	// probe runs one RTT probe and emits its event. It returns false when the
	// measurement window has closed (ctx done, or no time left for a bounded
	// read) and the caller should stop looping.
	probe := func() bool {
		// Re-check ctx before probing. Without this, a probe that landed in
		// the same scheduler window as ctx.Done() calls PingOnce *after* the
		// pump goroutines' StopPingRoute defer has torn down the conn —
		// producing trailing "no ping connection ... call DialPing first"
		// events at every run-end. Cosmetic but noisy.
		if ctx.Err() != nil {
			return false
		}
		// Bound this probe so its reads cannot outlive the measurement window
		// (pumpCtx / idleCtx). Without this, PingOnce's default read deadline
		// blocks pumpWg.Wait() past the run end — the mux-bw "never returns"
		// hang. Cap at muxBwProbeTimeout, shrunk to whatever ctx time remains.
		probeConf.Timeout = muxBwProbeTimeout
		if dl, ok := ctx.Deadline(); ok {
			if rem := time.Until(dl); rem < probeConf.Timeout {
				probeConf.Timeout = rem
			}
		}
		if probeConf.Timeout <= 0 {
			return false
		}
		sequence++
		latency, err := s.visor.PingOnce(probeConf)
		elapsed := time.Since(pumpStart)
		ev := &MuxRttProbe{
			Sequence:  sequence,
			ElapsedNs: elapsed.Nanoseconds(),
		}
		if err != nil {
			ev.Error = err.Error()
		} else {
			ev.LatencyNs = latency.Nanoseconds()
			probesMu.Lock()
			*probesOut = append(*probesOut, ev.LatencyNs)
			probesMu.Unlock()
		}
		emit(&MuxBandwidthEvent_RttProbe{RttProbe: ev})
		return true
	}

	// Probe once immediately. The ticker's first tick is a full ProbeInterval
	// in, so a measurement window shorter than (or close to) one interval — or
	// a jittery/loaded scheduler — could otherwise emit ZERO probes for the
	// whole phase (the flaky "expected at least one RTT probe event"). RTT is a
	// point-in-time reading, so an immediate first sample is correct and
	// guarantees the loaded/idle phase always yields at least one probe.
	if !probe() {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !probe() {
				return
			}
		}
	}
}

// muxBwSender drains the bounded event channel to stream.Send. One
// goroutine per RPC, so per-RPC isolation is automatic. Same shape
// as pingTreeSender — kept separate to avoid coupling the two
// handlers' event types.
func muxBwSender(
	stream PingService_StreamMuxBandwidthServer,
	eventCh <-chan *MuxBandwidthEvent,
	errCh chan<- error,
) {
	for ev := range eventCh {
		if err := stream.Send(ev); err != nil {
			errCh <- err
			for range eventCh {
				// Drain remaining events so the producer's send
				// doesn't block. Handler is tearing down.
			}
			return
		}
	}
	errCh <- nil
}

// sendMuxBwError emits a single Error event then exits the handler.
// Per the gRPC handler contract, returning a non-nil error from the
// handler closes the stream with that error visible to the client;
// we want the event IS the error report instead, so return nil.
// The return type is constrained by the handler signature.
//
//nolint:unparam // error is the handler-signature contract
func (s *PingServer) sendMuxBwError(
	stream PingService_StreamMuxBandwidthServer,
	code, msg string,
) error {
	_ = stream.Send(&MuxBandwidthEvent{ //nolint:errcheck
		TimestampNs: time.Now().UnixNano(),
		Payload: &MuxBandwidthEvent_Error{
			Error: &MuxBandwidthError{Code: code, Message: msg},
		},
	})
	return nil
}

// int64ToFloat converts []int64 nanosecond samples to []float64
// for aggregatePingSamples (which lives in server_ping_tree.go and
// operates on float64s for percentile math). Pulled into its own
// helper to keep the StreamMuxBandwidth Done construction readable.
func int64ToFloat(in []int64) []float64 {
	out := make([]float64, len(in))
	for i, v := range in {
		out[i] = float64(v)
	}
	return out
}

// quietCompilerUnused references math + sort to keep the imports
// active even if a future refactor removes the only direct usages;
// also documents that the file is the home for stats math. Kept as
// a build-time guard until the runtime tests assert all stats
// formulas — at which point this can be dropped.
var _ = math.Sqrt
var _ = sort.Float64s
