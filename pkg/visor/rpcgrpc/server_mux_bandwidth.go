// Package rpcgrpc — pkg/visor/rpcgrpc/server_mux_bandwidth.go:
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

	// Phase 1: dial all routes in parallel. Each goroutine emits a
	// RouteEstablished event when its setup completes (success or
	// failure). Routes that fail setup are excluded from the pump
	// phase; the pump_time clock starts after ALL setup goroutines
	// have either succeeded or returned setup errors.
	routes := make([]*muxBwRouteState, cfg.Routes)
	for i := 0; i < cfg.Routes; i++ {
		routes[i] = &muxBwRouteState{index: i}
	}

	setupWg := &sync.WaitGroup{}
	setupStart := time.Now()
	for i := 0; i < cfg.Routes; i++ {
		setupWg.Add(1)
		go func(rs *muxBwRouteState) {
			defer setupWg.Done()
			s.muxBwSetupRoute(ctx, &cfg, rs, emit)
		}(routes[i])
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

	// Phase 1.5: optional idle-baseline probe. Runs the probe loop
	// only (no pump goroutines, no sampler) for IdleBaselineDuration.
	// Captures a clean RTT distribution on the same routes the pump
	// will load — letting the caller compute queueing delay as
	// (loaded_pXX - idle_pXX) over identical paths. We bookend it
	// inside a small WaitGroup so the probe goroutine exits cleanly
	// before the pump phase starts.
	idleProbeSamples := make([]int64, 0)
	var idleProbesMu sync.Mutex
	if cfg.IdleBaselineDuration > 0 && cfg.ProbeRTT {
		idleCtx, idleCancel := context.WithTimeout(ctx, cfg.IdleBaselineDuration)
		idleStart := time.Now()
		idleWg := &sync.WaitGroup{}
		idleWg.Add(1)
		go func() {
			defer idleWg.Done()
			// Reuse the existing probe loop; it doesn't care whether
			// a pump is concurrently running — passing only the idle
			// samples slice means accumulator-direction is correct.
			s.muxBwProbeLoop(idleCtx, &cfg, idleStart, &idleProbesMu, &idleProbeSamples, emit)
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
			s.muxBwPumpRoute(pumpCtx, &cfg, rs)
		}(r)
	}

	// Sampler.
	pumpWg.Add(1)
	go func() {
		defer pumpWg.Done()
		s.muxBwSamplerLoop(pumpCtx, &cfg, routes, pumpStart, &peakSend, &peakRecv, emit)
	}()

	// Probe (optional).
	if cfg.ProbeRTT {
		// Find first established route for the probe.
		var probeTarget *muxBwRouteState
		for _, r := range routes {
			if r.established.Load() {
				probeTarget = r
				break
			}
		}
		if probeTarget != nil {
			pumpWg.Add(1)
			go func() {
				defer pumpWg.Done()
				s.muxBwProbeLoop(pumpCtx, &cfg, pumpStart, &probesMu, &probeSamples, emit)
			}()
		}
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
		for _, h := range s.visor.GetPingRouteDetailsAt(ref) {
			ev.Hops = append(ev.Hops, &RouteHop{
				TpId:   h.TpID,
				From:   h.From,
				To:     h.To,
				TpType: h.TpType,
			})
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
			// other routes can keep pumping. Log via the visor's
			// own logger (handler is server-side; no per-route
			// emit channel to surface this without adding event
			// noise). A future enhancement could emit a
			// route-level error event.
			s.log.Debugf("StreamMuxBandwidth: route %d pump error: %v", rs.index, err)
			return
		}
		rs.bytesSent.Add(bytesSent)
		rs.bytesRecv.Add(bytesRecvd)
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
			for _, r := range routes {
				totalSent += r.bytesSent.Load()
				totalRecv += r.bytesRecv.Load()
				if r.activeFlag.Load() {
					activeRoutes++
				}
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
	}
	var sequence int64

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
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
