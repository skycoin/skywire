// Package rpcgrpc — pkg/visor/rpcgrpc/server_ping_tree.go: server-side
// implementation of the StreamPingTree gRPC RPC. Replaces the
// client-side BFS in cmd/skywire-cli/commands/visor/ping/tree.go,
// giving the visor direct access to per-transport latency data and
// the route-finder graph without paying for RPC roundtrips per BFS
// step.
//
// Wire shape: per ping.proto, one PingTreeEvent per discovered entry
// / ping result / level boundary / run terminus. Per-level
// concurrency, ctx.Done() observed at every BFS iteration, bounded
// event channel with drop-StatusUpdate-before-PingResult policy.
//
// Authorization: TODO — first cut runs unauthenticated for
// development; follow-up PR gates on the dmsgWL whitelist
// (hypervisors + own PK + Dmsgpty.Whitelist) before opening to the
// network. Local CLI use over the unix-socket RPC is implicitly
// trusted via filesystem permissions; the gate matters for the
// dmsg-port gRPC entry point.
package rpcgrpc

import (
	"context"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
)

// Defaults for unset PingTreeRequest fields. Kept in one place so
// changing a default doesn't require chasing magic numbers through
// the handler.
const (
	defaultPingTreeTries         = 1
	defaultPingTreePacketSizeKb  = 2
	defaultPingTreePingTimeout   = 30 * time.Second
	defaultPingTreeSetupTimeout  = 30 * time.Second
	defaultPingTreeConcurrency   = 16
	pingTreeEventChannelCapacity = 256
)

// pingTreeCfg is the normalized request — every "0 = default" field
// resolved, every duration in time.Duration, every PK as cipher.PubKey.
// Pass this around inside the handler instead of the raw proto.
type pingTreeCfg struct {
	MaxLevel            int
	Hops                int
	Tries               int
	PacketSizeKb        int
	PingTimeout         time.Duration
	SetupTimeout        time.Duration
	Concurrency         int
	OnlineOnly          bool
	MinVersion          string
	UseTransportLatency bool
	DmsgOnly            bool
	DmsgPreCheck        bool
	Retries             int
	DryRun              bool
}

func normalizePingTreeRequest(req *PingTreeRequest) pingTreeCfg {
	c := pingTreeCfg{
		MaxLevel:            int(req.MaxLevel),
		Hops:                int(req.Hops),
		Tries:               int(req.Tries),
		PacketSizeKb:        int(req.PacketSizeKb),
		PingTimeout:         time.Duration(req.PingTimeoutNs),
		SetupTimeout:        time.Duration(req.SetupTimeoutNs),
		Concurrency:         int(req.Concurrency),
		OnlineOnly:          req.OnlineOnly,
		MinVersion:          req.MinVersion,
		UseTransportLatency: req.UseTransportLatency,
		DmsgOnly:            req.DmsgOnly,
		DmsgPreCheck:        req.DmsgPreCheck,
		Retries:             int(req.Retries),
		DryRun:              req.DryRun,
	}
	if c.Tries <= 0 {
		c.Tries = defaultPingTreeTries
	}
	if c.PacketSizeKb <= 0 {
		c.PacketSizeKb = defaultPingTreePacketSizeKb
	}
	if c.PingTimeout <= 0 {
		c.PingTimeout = defaultPingTreePingTimeout
	}
	if c.SetupTimeout <= 0 {
		c.SetupTimeout = defaultPingTreeSetupTimeout
	}
	if c.Concurrency <= 0 {
		c.Concurrency = defaultPingTreeConcurrency
	}
	return c
}

// pingTreeCandidate is one (transport, peer) pair the BFS has
// discovered and may try to ping. Keyed in the visited set by
// remote PK (we don't try to ping the same peer twice via different
// transports — the route-finder chooses one).
type pingTreeCandidate struct {
	tpID     string
	tpType   string
	remotePK string
	parentPK string
	level    int
}

// pingTreeTotals accumulates run-wide counters. Read by the final
// RunDone emission. All fields are int32 for direct proto field copy.
type pingTreeTotals struct {
	mu              sync.Mutex
	discovered      int32
	pinged          int32
	succeeded       int32
	failed          int32
	skippedCached   int32
	peakInFlight    int32
	currentInFlight int32 // tracked via atomic, snapshot to peak under mu
}

func (t *pingTreeTotals) bumpInFlight(delta int32) {
	cur := atomic.AddInt32(&t.currentInFlight, delta)
	if delta > 0 {
		t.mu.Lock()
		if cur > t.peakInFlight {
			t.peakInFlight = cur
		}
		t.mu.Unlock()
	}
}

// StreamPingTree walks the visor's neighborhood breadth-first and
// streams discovery + ping events. See ping.proto for the wire
// shape; this function is the canonical implementation.
//
// Implementation outline:
//  1. Fetch the global transport-discovery adjacency once
//     (FetchAllTransportEntries returns every entry in the system).
//  2. Spawn a sender goroutine that drains a bounded event channel
//     to stream.Send. The bounded channel + non-blocking send for
//     StatusUpdate gives backpressure visibility without dropping
//     measurement data.
//  3. BFS:
//     - Level 1: enumerate candidates from adjacency[localPK].
//     - For each level, emit Discovered, then ping with bounded
//     concurrency, then emit LevelDone.
//     - At each iteration check ctx.Done() so a client disconnect
//     tears the walk down within one in-flight ping.
//  4. Emit RunDone with termination_reason.
//
// Level-1 fast path via TransportSummary.LatencyMS is left as a
// TODO with the explicit hook so the follow-up PR can land it
// without restructuring (Beta's slice per role split).
func (s *PingServer) StreamPingTree(req *PingTreeRequest, stream PingService_StreamPingTreeServer) error {
	ctx := stream.Context()
	cfg := normalizePingTreeRequest(req)

	localPK := s.visor.LocalPK()
	localPKHex := localPK.String()

	// 1. Build adjacency from TPD's full transport entry set.
	entries, err := s.visor.FetchAllTransportEntries(ctx)
	if err != nil {
		return s.sendServerError(stream, "tpd_fetch_failed", err.Error())
	}
	adjacency := buildPingTreeAdjacency(entries)

	// 2. Set up sender goroutine + bounded event channel.
	eventCh := make(chan *PingTreeEvent, pingTreeEventChannelCapacity)
	sendErrCh := make(chan error, 1)
	go pingTreeSender(stream, eventCh, sendErrCh)

	// 3. BFS state.
	visited := map[string]bool{localPKHex: true}
	totals := &pingTreeTotals{}
	runStart := time.Now()
	terminationReason := "no_neighbors" // overwritten below as appropriate

	emit := func(payload isPingTreeEvent_Payload) {
		ev := &PingTreeEvent{
			TimestampNs: time.Now().UnixNano(),
			Payload:     payload,
		}
		// Discovered / PingResult / LevelDone / RunDone / ServerError
		// must NOT drop — they carry measurement data. StatusUpdate
		// may drop under pressure. The sender goroutine handles the
		// actual send to stream; this channel is purely an
		// ordering + buffering vehicle.
		select {
		case eventCh <- ev:
		case <-ctx.Done():
		}
	}
	emitStatus := func(phase string, inFlight, pending int32, msg string) {
		ev := &PingTreeEvent{
			TimestampNs: time.Now().UnixNano(),
			Payload: &PingTreeEvent_StatusUpdate{
				StatusUpdate: &PingTreeStatusUpdate{
					Phase:    phase,
					InFlight: inFlight,
					Pending:  pending,
					Message:  msg,
				},
			},
		}
		// Non-blocking: StatusUpdate is the drop-eligible event type.
		// If the channel is full, the consumer is already getting
		// data events; status text isn't worth blocking on.
		select {
		case eventCh <- ev:
		default:
		}
	}

	// Level 1: direct neighbors of localPK in the adjacency.
	emitStatus("discovering_level_1", 0, 0, "")
	level1 := candidatesFromAdjacency(adjacency, localPKHex, localPKHex, 1, visited)
	for _, c := range level1 {
		atomic.AddInt32(&totals.discovered, 1)
		emit(&PingTreeEvent_Discovered{
			Discovered: candidateToDiscovered(c),
		})
	}

	// Ping level 1.
	if !cfg.DryRun {
		s.pingTreePingLevel(ctx, &cfg, 1, level1, totals, emit, emitStatus)
	} else {
		// Dry-run: mark every level-1 entry as "skipped" so the
		// totals still tally.
		for _, c := range level1 {
			emit(&PingTreeEvent_PingResult{
				PingResult: &PingTreeResult{
					TpId:          c.tpID,
					TpType:        c.tpType,
					RemotePk:      c.remotePK,
					ParentPk:      c.parentPK,
					Level:         int32(c.level), //nolint:gosec // BFS level fits int32
					LatencySource: "skipped",
				},
			})
			atomic.AddInt32(&totals.skippedCached, 1)
		}
	}
	level1Done := levelDoneFor(level1, 1)
	emit(&PingTreeEvent_LevelDone{LevelDone: level1Done})

	// Check ctx between levels — client-disconnect tear-down.
	if ctx.Err() != nil {
		terminationReason = "context_cancel"
		goto FINALIZE
	}

	// Levels 2..N: expand from previously-discovered visors. We use
	// the adjacency we already have (no re-fetch — the snapshot is
	// per-call). Walk parents from visited set, but only those that
	// belong to the previous level.
	if cfg.MaxLevel == 0 || cfg.MaxLevel > 1 {
		parentsByLevel := map[int]map[string]bool{1: pksFromCandidates(level1)}
		for level := 2; ; level++ {
			if cfg.MaxLevel > 0 && level > cfg.MaxLevel {
				terminationReason = "max_level"
				break
			}
			if cfg.Hops > 0 && level > cfg.Hops {
				terminationReason = "hops_target"
				break
			}
			if ctx.Err() != nil {
				terminationReason = "context_cancel"
				break
			}

			parents := parentsByLevel[level-1]
			if len(parents) == 0 {
				terminationReason = "no_neighbors"
				break
			}

			emitStatus("discovering_level_"+itoa(level), 0, 0, "")
			var levelN []pingTreeCandidate
			for parentPK := range parents {
				for _, c := range candidatesFromAdjacency(adjacency, parentPK, parentPK, level, visited) {
					atomic.AddInt32(&totals.discovered, 1)
					levelN = append(levelN, c)
					emit(&PingTreeEvent_Discovered{Discovered: candidateToDiscovered(c)})
				}
			}
			if len(levelN) == 0 {
				terminationReason = "no_neighbors"
				break
			}

			// Decide whether THIS level pings: --hops means only ping
			// the target level; everything in between is discovered
			// for BFS continuation but not pinged.
			shouldPing := !cfg.DryRun && (cfg.Hops == 0 || cfg.Hops == level)
			if shouldPing {
				s.pingTreePingLevel(ctx, &cfg, level, levelN, totals, emit, emitStatus)
			} else {
				for _, c := range levelN {
					emit(&PingTreeEvent_PingResult{
						PingResult: &PingTreeResult{
							TpId:          c.tpID,
							TpType:        c.tpType,
							RemotePk:      c.remotePK,
							ParentPk:      c.parentPK,
							Level:         int32(c.level), //nolint:gosec // BFS level fits int32
							LatencySource: "skipped",
						},
					})
					atomic.AddInt32(&totals.skippedCached, 1)
				}
			}
			emit(&PingTreeEvent_LevelDone{LevelDone: levelDoneFor(levelN, level)})

			parentsByLevel[level] = pksFromCandidates(levelN)
			// Free the previous level's parent set; not needed again.
			delete(parentsByLevel, level-1)
		}
	} else {
		terminationReason = "max_level"
	}

FINALIZE:
	emit(&PingTreeEvent_RunDone{
		RunDone: &PingTreeRunDone{
			TotalDiscovered:    atomic.LoadInt32(&totals.discovered),
			TotalPinged:        atomic.LoadInt32(&totals.pinged),
			TotalSucceeded:     atomic.LoadInt32(&totals.succeeded),
			TotalFailed:        atomic.LoadInt32(&totals.failed),
			TotalSkippedCached: atomic.LoadInt32(&totals.skippedCached),
			WallTimeNs:         time.Since(runStart).Nanoseconds(),
			PeakInFlight:       totals.peakInFlight,
			TerminationReason:  terminationReason,
		},
	})

	close(eventCh)
	return <-sendErrCh
}

// pingTreeSender drains eventCh to stream.Send. Runs until the
// channel closes; any Send error closes the run with that error.
// One goroutine per RPC, so per-RPC isolation is automatic.
func pingTreeSender(stream PingService_StreamPingTreeServer, eventCh <-chan *PingTreeEvent, errCh chan<- error) {
	for ev := range eventCh {
		if err := stream.Send(ev); err != nil {
			errCh <- err
			// Drain remaining events so the producer's send-to-channel
			// doesn't block forever. The handler is in tear-down at
			// this point — events are best-effort.
			for range eventCh {
			}
			return
		}
	}
	errCh <- nil
}

// sendServerError emits a ServerError event and closes the stream.
// Used for unrecoverable conditions detected before the BFS starts.
func (s *PingServer) sendServerError(stream PingService_StreamPingTreeServer, code, msg string) error {
	_ = stream.Send(&PingTreeEvent{ //nolint:errcheck // best-effort terminal event; we're closing anyway
		TimestampNs: time.Now().UnixNano(),
		Payload: &PingTreeEvent_ServerError{
			ServerError: &PingTreeServerError{Code: code, Message: msg},
		},
	})
	return nil
}

// pingTreePingLevel pings every candidate at one BFS level with
// bounded per-level concurrency. Blocks until all candidates
// complete (success, failure, or ctx-cancel). Updates totals
// atomically; emits one PingResult per candidate.
//
// When UseTransportLatency is set and level == 1, candidates whose
// remote PK has a local managed transport with a non-zero smoothed
// RTT are short-circuited: a PingResult with
// LatencySource="transport_summary" is emitted without sending a
// live ping. Saves a full setup+ping cycle on direct neighbors the
// visor is already measuring. Candidates without a known transport
// latency fall through to the live-ping path.
func (s *PingServer) pingTreePingLevel(
	ctx context.Context,
	cfg *pingTreeCfg,
	level int,
	candidates []pingTreeCandidate,
	totals *pingTreeTotals,
	emit func(isPingTreeEvent_Payload),
	emitStatus func(string, int32, int32, string),
) {
	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup

	var pending = int32(len(candidates)) //nolint:gosec // candidate count is bounded by BFS frontier, fits int32
	emitStatus("pinging_level_"+itoa(level), 0, pending, "")

	for _, c := range candidates {
		if ctx.Err() != nil {
			break
		}

		// UseTransportLatency fast-path: only at level-1 (direct
		// neighbors). Beyond level-1 there's no local transport to
		// the candidate — must live-ping through intermediates.
		if level == 1 && cfg.UseTransportLatency {
			if result, hit := s.tryTransportLatencyFastPath(c); hit {
				emit(&PingTreeEvent_PingResult{PingResult: result})
				atomic.AddInt32(&totals.skippedCached, 1)
				atomic.AddInt32(&pending, -1)
				emitStatus(
					"pinging_level_"+itoa(level),
					atomic.LoadInt32(&totals.currentInFlight),
					atomic.LoadInt32(&pending),
					"",
				)
				continue
			}
		}

		sem <- struct{}{}
		wg.Add(1)
		atomic.AddInt32(&pending, -1)
		totals.bumpInFlight(1)
		go func(cand pingTreeCandidate) {
			defer func() {
				<-sem
				totals.bumpInFlight(-1)
				wg.Done()
				emitStatus(
					"pinging_level_"+itoa(level),
					atomic.LoadInt32(&totals.currentInFlight),
					atomic.LoadInt32(&pending),
					"",
				)
			}()
			result := s.pingOneTransport(ctx, cfg, cand)
			emit(&PingTreeEvent_PingResult{PingResult: result})
			atomic.AddInt32(&totals.pinged, 1)
			if result.Failed {
				atomic.AddInt32(&totals.failed, 1)
			} else {
				atomic.AddInt32(&totals.succeeded, 1)
			}
		}(c)
	}

	wg.Wait()
}

// tryTransportLatencyFastPath returns (result, true) when the
// candidate's remote PK has a local managed transport with a
// non-zero smoothed RTT; the result is a PingTreeResult with
// LatencySource="transport_summary" suitable for emission. Returns
// (nil, false) when no such transport exists, the PK is malformed,
// or the transport's latency has not yet been sampled — caller
// should fall through to live-ping.
func (s *PingServer) tryTransportLatencyFastPath(cand pingTreeCandidate) (*PingTreeResult, bool) {
	var pk cipher.PubKey
	if err := pk.Set(cand.remotePK); err != nil {
		return nil, false
	}
	latencyMS := s.visor.GetTransportLatencyByRemotePK(pk)
	return transportLatencyResult(cand, latencyMS)
}

// transportLatencyResult is the pure-data half of the fast path:
// given a candidate and a smoothed RTT in milliseconds, return the
// PingTreeResult to emit, or (nil, false) when the latency value is
// not usable. Split out from tryTransportLatencyFastPath so the
// numeric conversion + skip-condition logic is unit-testable
// without constructing a full VisorAPI mock.
func transportLatencyResult(cand pingTreeCandidate, latencyMS float64) (*PingTreeResult, bool) {
	if latencyMS <= 0 {
		return nil, false
	}
	pingNs := int64(latencyMS * float64(time.Millisecond))
	return &PingTreeResult{
		TpId:          cand.tpID,
		TpType:        cand.tpType,
		RemotePk:      cand.remotePK,
		ParentPk:      cand.parentPK,
		Level:         int32(cand.level), //nolint:gosec // BFS level fits int32
		LatencySource: "transport_summary",
		PingAvgNs:     pingNs,
		PingP50Ns:     pingNs,
		PingP99Ns:     pingNs,
		SampleCount:   1,
	}, true
}

// pingOneTransport runs a single transport's worth of pings (tries
// many, computes p50/p99/jitter) and returns the proto PingTreeResult.
// On ctx-cancel mid-flight the result carries Canceled=true so
// consumers can distinguish from genuine failures.
func (s *PingServer) pingOneTransport(
	ctx context.Context,
	cfg *pingTreeCfg,
	cand pingTreeCandidate,
) *PingTreeResult {
	result := &PingTreeResult{
		TpId:          cand.tpID,
		TpType:        cand.tpType,
		RemotePk:      cand.remotePK,
		ParentPk:      cand.parentPK,
		Level:         int32(cand.level), //nolint:gosec // BFS level fits int32
		LatencySource: "live_ping",
	}

	var pk cipher.PubKey
	if err := pk.Set(cand.remotePK); err != nil {
		result.Failed = true
		result.SetupErr = "invalid remote pk: " + err.Error()
		return result
	}

	pingConf := PingConf{
		PK:       pk,
		Tries:    cfg.Tries,
		PcktSize: cfg.PacketSizeKb,
	}

	// Setup phase: route setup OR dmsg dial. Timeout per setup.
	setupCtx, setupCancel := context.WithTimeout(ctx, cfg.SetupTimeout)
	defer setupCancel()
	setupStart := time.Now()

	dialDone := make(chan error, 1)
	go func() {
		if cfg.DmsgOnly {
			dialDone <- s.visor.DialDmsgPing(pk)
		} else {
			dialDone <- s.visor.DialPing(pingConf)
		}
	}()

	select {
	case err := <-dialDone:
		result.SetupLatencyNs = time.Since(setupStart).Nanoseconds()
		if err != nil {
			result.Failed = true
			result.SetupErr = err.Error()
			if ctx.Err() != nil {
				result.Canceled = true
			}
			return result
		}
	case <-setupCtx.Done():
		result.SetupLatencyNs = time.Since(setupStart).Nanoseconds()
		result.Failed = true
		if ctx.Err() != nil {
			result.SetupErr = "context canceled during setup"
			result.Canceled = true
		} else {
			result.SetupErr = "setup timeout after " + cfg.SetupTimeout.String()
		}
		return result
	}

	// Ping phase: run cfg.Tries pings; aggregate stats.
	samples := make([]float64, 0, cfg.Tries)
	for i := 0; i < cfg.Tries; i++ {
		if ctx.Err() != nil {
			result.Canceled = true
			break
		}
		pingCtx, pingCancel := context.WithTimeout(ctx, cfg.PingTimeout)
		latencyCh := make(chan time.Duration, 1)
		errPingCh := make(chan error, 1)
		go func() {
			var (
				latency time.Duration
				err     error
			)
			if cfg.DmsgOnly {
				latency, err = s.visor.DmsgPingOnce(pingConf)
			} else {
				latency, err = s.visor.PingOnce(pingConf)
			}
			if err != nil {
				errPingCh <- err
				return
			}
			latencyCh <- latency
		}()
		select {
		case latency := <-latencyCh:
			samples = append(samples, float64(latency.Nanoseconds()))
		case err := <-errPingCh:
			if result.PingErr == "" {
				result.PingErr = err.Error()
			}
		case <-pingCtx.Done():
			if result.PingErr == "" {
				result.PingErr = "ping timeout after " + cfg.PingTimeout.String()
			}
		}
		pingCancel()
	}

	// Tear down the persistent route/dmsg connection used by this run.
	// Tear-down errors are non-actionable at the BFS level — the
	// underlying conn is going to be GC'd either way, and surfacing
	// them would split the result emission across success+stop-error
	// paths.
	if cfg.DmsgOnly {
		_ = s.visor.StopDmsgPing(pk) //nolint:errcheck
	} else {
		_ = s.visor.StopPing(pk) //nolint:errcheck
	}

	result.SampleCount = int32(len(samples)) //nolint:gosec // sample count <= cfg.Tries (int32 input field)
	if len(samples) == 0 {
		result.Failed = true
		if result.PingErr == "" {
			result.PingErr = "no successful pings"
		}
		return result
	}
	result.PingAvgNs, result.PingP50Ns, result.PingP99Ns, result.JitterNs = aggregatePingSamples(samples)
	return result
}

// aggregatePingSamples computes avg / p50 / p99 / population stddev
// (jitter) over the sample series. All inputs and outputs are in
// nanoseconds. Returns zeros when len(samples) == 0; only the
// stddev is zero when len < 2 (no variance defined).
func aggregatePingSamples(samples []float64) (avg, p50, p99, jitter int64) {
	if len(samples) == 0 {
		return 0, 0, 0, 0
	}
	sum := 0.0
	for _, s := range samples {
		sum += s
	}
	avgF := sum / float64(len(samples))
	avg = int64(avgF)

	// Percentiles via sorted copy. cfg.Tries is small (1-100 typical),
	// so the alloc + sort is negligible compared to the network I/O
	// the ping itself did.
	sorted := make([]float64, len(samples))
	copy(sorted, samples)
	sort.Float64s(sorted)
	p50 = int64(sorted[len(sorted)/2])
	// p99 over a tiny sample is effectively max; that's the right
	// behavior — synth's directive cares about tail latency.
	p99idx := (len(sorted) * 99) / 100
	if p99idx >= len(sorted) {
		p99idx = len(sorted) - 1
	}
	p99 = int64(sorted[p99idx])

	if len(samples) < 2 {
		return avg, p50, p99, 0
	}
	var sq float64
	for _, s := range samples {
		d := s - avgF
		sq += d * d
	}
	jitter = int64(math.Sqrt(sq / float64(len(samples))))
	return avg, p50, p99, jitter
}

// buildPingTreeAdjacency is the same shape as the client-side BFS
// used: PK -> [neighbor info]. Each transport produces two edges
// (one per direction) so the BFS can walk in either direction.
type pingTreeAdjacencyEntry struct {
	tpID     string
	tpType   string
	remotePK string
}

func buildPingTreeAdjacency(entries []*transport.Entry) map[string][]pingTreeAdjacencyEntry {
	adj := make(map[string][]pingTreeAdjacencyEntry, len(entries)/2)
	for _, e := range entries {
		if e == nil {
			continue
		}
		a, b := e.Edges[0].String(), e.Edges[1].String()
		adj[a] = append(adj[a], pingTreeAdjacencyEntry{
			tpID:     e.ID.String(),
			tpType:   string(e.Type),
			remotePK: b,
		})
		if a != b {
			adj[b] = append(adj[b], pingTreeAdjacencyEntry{
				tpID:     e.ID.String(),
				tpType:   string(e.Type),
				remotePK: a,
			})
		}
	}
	return adj
}

// candidatesFromAdjacency emits the next-level candidates for one
// parent PK, marking each emitted remote PK as visited so siblings
// at the same level don't re-emit it. parentPK and parentLevel are
// recorded on each candidate for tree-rendering on the consumer.
func candidatesFromAdjacency(
	adj map[string][]pingTreeAdjacencyEntry,
	pivotPK, parentPK string,
	level int,
	visited map[string]bool,
) []pingTreeCandidate {
	neighbors := adj[pivotPK]
	out := make([]pingTreeCandidate, 0, len(neighbors))
	for _, n := range neighbors {
		if visited[n.remotePK] {
			continue
		}
		visited[n.remotePK] = true
		out = append(out, pingTreeCandidate{
			tpID:     n.tpID,
			tpType:   n.tpType,
			remotePK: n.remotePK,
			parentPK: parentPK,
			level:    level,
		})
	}
	return out
}

func candidateToDiscovered(c pingTreeCandidate) *PingTreeDiscovered {
	return &PingTreeDiscovered{
		TpId:     c.tpID,
		TpType:   c.tpType,
		RemotePk: c.remotePK,
		ParentPk: c.parentPK,
		Level:    int32(c.level), //nolint:gosec // BFS level fits int32
	}
}

func pksFromCandidates(cs []pingTreeCandidate) map[string]bool {
	out := make(map[string]bool, len(cs))
	for _, c := range cs {
		out[c.remotePK] = true
	}
	return out
}

func levelDoneFor(candidates []pingTreeCandidate, level int) *PingTreeLevelDone {
	// Note: at the moment we don't track per-level success/failure on
	// totals (only run-wide). For LevelDone we compute the
	// per-candidate-count as proxy for "attempted" — the per-level
	// success/failed breakout would require an additional
	// per-level totals struct. Acceptable initial fidelity; harness
	// consumers can derive the same values by counting PingResult
	// events between LevelDone boundaries.
	return &PingTreeLevelDone{
		Level:     int32(level),           //nolint:gosec // BFS level int fits int32
		Attempted: int32(len(candidates)), //nolint:gosec // candidate count fits int32
		// TODO: tally per-level Succeeded/Failed/SkippedCached when
		// the use_transport_latency fast-path lands and the
		// per-level distinction matters for that PR's tests.
	}
}

// itoa is a 1-allocation int-to-string used in StatusUpdate phase
// strings. Avoids pulling strconv just for this.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
