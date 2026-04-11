// Package setupmetrics — in-process aggregated stats for route setup
// requests. Complements victoria_metrics.go (which publishes Prometheus
// gauges for external scrape) with a structured snapshot the visor can
// expose over RPC and the CLI can pretty-print for quick triage.
//
// The collector is safe for concurrent use. It categorizes each failure
// into a small set of well-known reasons so operators can answer "which
// part of the setup path is broken?" without correlating raw log lines.
package setupmetrics

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

// FailureReason is a short stable identifier for why a route setup
// attempt failed. Keep values lowercase-snake so they render cleanly in
// JSON / CLI tables.
type FailureReason string

// Failure reason categories. The set intentionally stays small so
// operators can reason about them; unknown / unclassified failures fall
// through to ReasonUnknown and the raw error message is still captured
// in the RecentFailures ring buffer for investigation.
const (
	ReasonInvalidRoute      FailureReason = "invalid_route"      // BidirectionalRoute.Check failed
	ReasonRuleGeneration    FailureReason = "rule_generation"    // GenerateRules returned ErrNoKey etc.
	ReasonIDReservation     FailureReason = "id_reservation"     // ReserveRouteIDs failed (could not dial a hop, or a hop refused)
	ReasonIntermediaryRules FailureReason = "intermediary_rules" // BroadcastIntermediaryRules failed
	ReasonDestinationRules  FailureReason = "destination_rules"  // AddEdgeRules on destination router failed
	ReasonContextDeadline   FailureReason = "context_deadline"   // overall request timed out
	ReasonContextCanceled   FailureReason = "context_canceled"   // caller canceled before completion
	ReasonConcurrencyLimit  FailureReason = "concurrency_limit"  // dropped at the accept loop backpressure check
	ReasonUnknown           FailureReason = "unknown"            // failure classifier could not decide
)

// FailureEvent captures a single failed setup attempt with enough
// context to start an investigation without a log trawl.
type FailureEvent struct {
	Timestamp  time.Time     `json:"timestamp"`
	SrcPK      string        `json:"src_pk"`
	DstPK      string        `json:"dst_pk"`
	HopCount   int           `json:"hop_count"`
	Reason     FailureReason `json:"reason"`
	Error      string        `json:"error"`
	DurationMs int64         `json:"duration_ms"`
}

// LatencyStats are percentile summaries (milliseconds) computed over the
// collector's latency ring on demand.
type LatencyStats struct {
	Count uint64 `json:"count"`
	Min   int64  `json:"min_ms"`
	Max   int64  `json:"max_ms"`
	Mean  int64  `json:"mean_ms"`
	P50   int64  `json:"p50_ms"`
	P95   int64  `json:"p95_ms"`
	P99   int64  `json:"p99_ms"`
}

// DestStat is a per-destination counter used to surface "hot" visors
// (targets of many requests) and "troublesome" visors (high failure
// rate).
type DestStat struct {
	PK     string `json:"pk"`
	Total  uint64 `json:"total"`
	Failed uint64 `json:"failed"`
}

// StatsSnapshot is the public, JSON-friendly view exposed over RPC/CLI.
// Every field is a point-in-time copy — the underlying collector holds
// its own mutex during Snapshot so the struct is safe to marshal and
// return.
type StatsSnapshot struct {
	StartedAt time.Time `json:"started_at"`
	UptimeSec int64     `json:"uptime_sec"`

	TotalRequests    uint64 `json:"total_requests"`
	Successful       uint64 `json:"successful"`
	Failed           uint64 `json:"failed"`
	ConcurrencyDrops uint64 `json:"concurrency_drops"`
	ActiveRequests   int    `json:"active_requests"`

	SuccessRatePct float64 `json:"success_rate_pct"`

	FailuresByReason map[FailureReason]uint64 `json:"failures_by_reason"`

	LatencyMs LatencyStats `json:"latency_ms"`

	// RouteLengthHist maps hop count → number of successful setups.
	RouteLengthHist map[int]uint64 `json:"route_length_hist"`

	// Top destinations sorted by total request count (descending).
	// Capped at the collector's top-N config.
	TopDestinations []DestStat `json:"top_destinations"`

	// Destinations ranked by failure count (descending). Same cap.
	TopFailedDestinations []DestStat `json:"top_failed_destinations"`

	// Most recent N failures, newest first.
	RecentFailures []FailureEvent `json:"recent_failures"`

	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	LastFailureAt *time.Time `json:"last_failure_at,omitempty"`
}

// Collector is a thread-safe aggregator of route-setup attempt metadata.
// It implements Metrics so it can be wired anywhere the existing
// Victoria Metrics struct is used (the two are independent — wiring the
// Collector does not remove Prometheus output).
type Collector struct {
	mu sync.Mutex

	startedAt time.Time

	total, success, failed uint64
	concurrencyDrops       uint64
	active                 int

	failsByReason map[FailureReason]uint64

	// Successful-setup latency ring, used to compute percentiles on
	// Snapshot. Fixed size to bound memory.
	latencyRing    []int64
	latencyRingIdx int
	latencyRingLen int

	routeLenHist map[int]uint64

	// Per-destination counters. Capped at destCapacity entries; when
	// full, an LRU-ish eviction would be nice but this is diagnostic
	// data so we settle for "remember first N destinations seen" and
	// reset the whole map when it fills. In practice visor workloads
	// have a fairly small working set of destinations.
	dests        map[string]*DestStat
	destCapacity int

	// Ring buffer of recent failures. Oldest at index failureRingIdx,
	// newest at (failureRingIdx-1) mod len.
	failureRing    []FailureEvent
	failureRingIdx int
	failureRingLen int

	lastSuccessAt time.Time
	lastFailureAt time.Time
}

// CollectorConfig configures the ring buffer / destination cap sizes.
// Zero values mean "use the default".
type CollectorConfig struct {
	LatencyRingSize int // number of recent successful latencies to keep for percentile calc
	FailureRingSize int // number of recent failures to keep in RecentFailures
	DestCapacity    int // max number of distinct destinations to track
}

// Defaults for CollectorConfig.
const (
	defaultLatencyRingSize = 1024
	defaultFailureRingSize = 128
	defaultDestCapacity    = 512
)

// NewCollector returns a ready-to-use Collector.
func NewCollector(cfg CollectorConfig) *Collector {
	if cfg.LatencyRingSize <= 0 {
		cfg.LatencyRingSize = defaultLatencyRingSize
	}
	if cfg.FailureRingSize <= 0 {
		cfg.FailureRingSize = defaultFailureRingSize
	}
	if cfg.DestCapacity <= 0 {
		cfg.DestCapacity = defaultDestCapacity
	}
	return &Collector{
		startedAt:     time.Now(),
		failsByReason: make(map[FailureReason]uint64),
		latencyRing:   make([]int64, cfg.LatencyRingSize),
		routeLenHist:  make(map[int]uint64),
		dests:         make(map[string]*DestStat),
		destCapacity:  cfg.DestCapacity,
		failureRing:   make([]FailureEvent, cfg.FailureRingSize),
	}
}

// RecordRequest satisfies setupmetrics.Metrics — it bumps the active
// request counter up-front and decrements it when the deferred closure
// runs. Per-request success/failure counts are deliberately kept in
// RecordRoute so the two don't double-count when both are called (which
// is what the standalone setup node does today).
func (c *Collector) RecordRequest() func(*routing.EdgeRules, *error) {
	c.mu.Lock()
	c.active++
	c.mu.Unlock()
	return func(_ *routing.EdgeRules, _ *error) {
		c.mu.Lock()
		if c.active > 0 {
			c.active--
		}
		c.mu.Unlock()
	}
}

// RecordRoute satisfies setupmetrics.Metrics. When the collector is
// wired through the normal path this records the attempt under its
// "unknown" labels — callers who want richer data should use
// RecordRouteContext instead.
func (c *Collector) RecordRoute() func(*error) {
	return c.RecordRouteContext(context.Background(), cipher.PubKey{}, cipher.PubKey{}, 0)
}

// RecordRouteContext is the richer variant of RecordRoute that captures
// enough context to fill a FailureEvent. Call it once per route attempt
// at the top of the setup handler and defer the returned closure.
//
//	defer collector.RecordRouteContext(ctx, src, dst, hopCount)(&err)
func (c *Collector) RecordRouteContext(ctx context.Context, srcPK, dstPK cipher.PubKey, hopCount int) func(*error) {
	start := time.Now()
	return func(errp *error) {
		c.finish(ctx, srcPK, dstPK, hopCount, start, errp)
	}
}

// RecordConcurrencyDrop is called when a request is refused at the
// accept-loop backpressure check (never reaches the handler).
func (c *Collector) RecordConcurrencyDrop() {
	c.mu.Lock()
	c.concurrencyDrops++
	c.mu.Unlock()
}

func (c *Collector) finish(ctx context.Context, srcPK, dstPK cipher.PubKey, hopCount int, start time.Time, errp *error) {
	duration := time.Since(start)
	durMs := duration.Milliseconds()

	var err error
	if errp != nil {
		err = *errp
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.total++
	dstStr := dstPK.String()
	destStat := c.touchDest(dstStr)

	if err == nil {
		c.success++
		c.lastSuccessAt = time.Now()
		if destStat != nil {
			destStat.Total++
		}
		// Record latency and hop count for successful attempts.
		c.latencyRing[c.latencyRingIdx] = durMs
		c.latencyRingIdx = (c.latencyRingIdx + 1) % len(c.latencyRing)
		if c.latencyRingLen < len(c.latencyRing) {
			c.latencyRingLen++
		}
		if hopCount > 0 {
			c.routeLenHist[hopCount]++
		}
		return
	}

	// Failure path.
	c.failed++
	c.lastFailureAt = time.Now()
	if destStat != nil {
		destStat.Total++
		destStat.Failed++
	}

	reason := classifyError(ctx, err)
	c.failsByReason[reason]++

	// Truncate error strings so a giant rpc payload doesn't blow up
	// the ring buffer.
	errStr := err.Error()
	if len(errStr) > 512 {
		errStr = errStr[:509] + "..."
	}

	event := FailureEvent{
		Timestamp:  time.Now().UTC(),
		SrcPK:      srcPK.String(),
		DstPK:      dstStr,
		HopCount:   hopCount,
		Reason:     reason,
		Error:      errStr,
		DurationMs: durMs,
	}
	c.failureRing[c.failureRingIdx] = event
	c.failureRingIdx = (c.failureRingIdx + 1) % len(c.failureRing)
	if c.failureRingLen < len(c.failureRing) {
		c.failureRingLen++
	}
}

// touchDest returns (creating if necessary) the DestStat entry for the
// given destination PK. Returns nil if the destCapacity has been
// reached and the key is new — the caller should treat that as "don't
// track", not "error".
func (c *Collector) touchDest(pk string) *DestStat {
	if pk == "" {
		return nil
	}
	if d, ok := c.dests[pk]; ok {
		return d
	}
	if len(c.dests) >= c.destCapacity {
		return nil
	}
	d := &DestStat{PK: pk}
	c.dests[pk] = d
	return d
}

// classifyError maps an error into one of the well-known FailureReason
// values. Uses errors.Is for sentinel checks and substring matching on
// the wrapped message for the rest (the existing setup-node error
// strings are stable enough to match on).
func classifyError(ctx context.Context, err error) FailureReason {
	if err == nil {
		return ""
	}
	// Context errors first — a deadline or cancel often masks the
	// "real" inner failure but for operators the most actionable
	// signal is "the deadline ran out", not "the inner error was X".
	if errors.Is(err, context.DeadlineExceeded) {
		return ReasonContextDeadline
	}
	if errors.Is(err, context.Canceled) {
		return ReasonContextCanceled
	}
	// Sometimes a wrapped ctx err is only visible through the ctx
	// itself. Check as a fallback.
	if ctx != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return ReasonContextDeadline
		}
		if ctx.Err() == context.Canceled {
			return ReasonContextCanceled
		}
	}

	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "broadcast rules to destination"):
		return ReasonDestinationRules
	case strings.Contains(s, "broadcast intermediary rules"),
		strings.Contains(s, "intermediary rules"):
		return ReasonIntermediaryRules
	case strings.Contains(s, "reserve route id"),
		strings.Contains(s, "id reserver"),
		strings.Contains(s, "no client available"):
		return ReasonIDReservation
	case strings.Contains(s, "generate rules"),
		strings.Contains(s, "no key for"):
		return ReasonRuleGeneration
	case strings.Contains(s, "invalid route"),
		strings.Contains(s, "bidirectional route"),
		strings.Contains(s, "null dst"),
		strings.Contains(s, "null src"):
		return ReasonInvalidRoute
	default:
		return ReasonUnknown
	}
}

// Snapshot returns a deep copy of the current state. Safe to marshal.
func (c *Collector) Snapshot() StatsSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Copy counters.
	snap := StatsSnapshot{
		StartedAt:        c.startedAt,
		UptimeSec:        int64(time.Since(c.startedAt).Seconds()),
		TotalRequests:    c.total,
		Successful:       c.success,
		Failed:           c.failed,
		ConcurrencyDrops: c.concurrencyDrops,
		ActiveRequests:   c.active,
		FailuresByReason: make(map[FailureReason]uint64, len(c.failsByReason)),
		RouteLengthHist:  make(map[int]uint64, len(c.routeLenHist)),
	}
	for k, v := range c.failsByReason {
		snap.FailuresByReason[k] = v
	}
	for k, v := range c.routeLenHist {
		snap.RouteLengthHist[k] = v
	}
	if c.total > 0 {
		snap.SuccessRatePct = float64(c.success) / float64(c.total) * 100
	}
	snap.LatencyMs = c.latencyStatsLocked()

	// Top destinations (by total) and top failed destinations.
	snap.TopDestinations, snap.TopFailedDestinations = c.topDestsLocked(10)

	// Recent failures, newest first.
	snap.RecentFailures = c.recentFailuresLocked()

	if !c.lastSuccessAt.IsZero() {
		t := c.lastSuccessAt
		snap.LastSuccessAt = &t
	}
	if !c.lastFailureAt.IsZero() {
		t := c.lastFailureAt
		snap.LastFailureAt = &t
	}

	return snap
}

// Reset clears all counters and ring buffers. Useful before starting a
// diagnostic capture window.
func (c *Collector) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.startedAt = time.Now()
	c.total, c.success, c.failed = 0, 0, 0
	c.concurrencyDrops = 0
	// active deliberately not touched — there may be in-flight requests.
	c.failsByReason = make(map[FailureReason]uint64)
	for i := range c.latencyRing {
		c.latencyRing[i] = 0
	}
	c.latencyRingIdx = 0
	c.latencyRingLen = 0
	c.routeLenHist = make(map[int]uint64)
	c.dests = make(map[string]*DestStat)
	for i := range c.failureRing {
		c.failureRing[i] = FailureEvent{}
	}
	c.failureRingIdx = 0
	c.failureRingLen = 0
	c.lastSuccessAt = time.Time{}
	c.lastFailureAt = time.Time{}
}

func (c *Collector) latencyStatsLocked() LatencyStats {
	if c.latencyRingLen == 0 {
		return LatencyStats{}
	}
	// Copy active part of ring and sort for percentiles.
	sorted := make([]int64, c.latencyRingLen)
	copy(sorted, c.latencyRing[:c.latencyRingLen])
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	var sum int64
	for _, v := range sorted {
		sum += v
	}
	count := c.latencyRingLen
	if count < 0 {
		count = 0
	}
	return LatencyStats{
		Count: uint64(count), //nolint:gosec // count guarded above
		Min:   sorted[0],
		Max:   sorted[len(sorted)-1],
		Mean:  sum / int64(len(sorted)),
		P50:   percentile(sorted, 0.50),
		P95:   percentile(sorted, 0.95),
		P99:   percentile(sorted, 0.99),
	}
}

func percentile(sorted []int64, p float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func (c *Collector) topDestsLocked(n int) (byTotal, byFailed []DestStat) {
	all := make([]DestStat, 0, len(c.dests))
	for _, d := range c.dests {
		all = append(all, *d)
	}
	if len(all) == 0 {
		return nil, nil
	}

	// Sort-by-total copy.
	byTotal = make([]DestStat, len(all))
	copy(byTotal, all)
	sort.Slice(byTotal, func(i, j int) bool {
		if byTotal[i].Total != byTotal[j].Total {
			return byTotal[i].Total > byTotal[j].Total
		}
		return byTotal[i].PK < byTotal[j].PK
	})
	if len(byTotal) > n {
		byTotal = byTotal[:n]
	}

	// Sort-by-failed copy, skipping any with zero failures.
	byFailed = make([]DestStat, 0, len(all))
	for _, d := range all {
		if d.Failed > 0 {
			byFailed = append(byFailed, d)
		}
	}
	sort.Slice(byFailed, func(i, j int) bool {
		if byFailed[i].Failed != byFailed[j].Failed {
			return byFailed[i].Failed > byFailed[j].Failed
		}
		return byFailed[i].PK < byFailed[j].PK
	})
	if len(byFailed) > n {
		byFailed = byFailed[:n]
	}
	return byTotal, byFailed
}

func (c *Collector) recentFailuresLocked() []FailureEvent {
	if c.failureRingLen == 0 {
		return nil
	}
	out := make([]FailureEvent, 0, c.failureRingLen)
	// Walk newest → oldest.
	for i := 0; i < c.failureRingLen; i++ {
		idx := (c.failureRingIdx - 1 - i + len(c.failureRing)) % len(c.failureRing)
		out = append(out, c.failureRing[idx])
	}
	return out
}
