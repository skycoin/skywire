// Package setupmetrics pkg/router/setupmetrics/stats.go c2-net-routing
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

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
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
	ReasonInvalidRoute            FailureReason = "invalid_route"            // BidirectionalRoute.Check failed
	ReasonRuleGeneration          FailureReason = "rule_generation"          // GenerateRules returned ErrNoKey etc.
	ReasonIDReservation           FailureReason = "id_reservation"           // ReserveRouteIDs failed dialing the destination
	ReasonSourceUnreachable       FailureReason = "source_unreachable"       // ReserveRouteIDs could not reach the SOURCE visor (not the dst's fault)
	ReasonIntermediateUnreachable FailureReason = "intermediate_unreachable" // ReserveRouteIDs could not reach an INTERMEDIATE visor (not the dst's fault)
	ReasonIntermediaryRules       FailureReason = "intermediary_rules"       // BroadcastIntermediaryRules failed
	ReasonDestinationRules        FailureReason = "destination_rules"        // AddEdgeRules on destination router failed
	ReasonContextDeadline         FailureReason = "context_deadline"         // overall request timed out
	ReasonContextCanceled         FailureReason = "context_canceled"         // caller canceled before completion
	ReasonConcurrencyLimit        FailureReason = "concurrency_limit"        // dropped at the accept loop backpressure check
	ReasonCircuitOpen             FailureReason = "circuit_open"             // per-destination breaker short-circuited the setup
	ReasonUnknown                 FailureReason = "unknown"                  // failure classifier could not decide
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
// rate). Circuit state is the CircuitState (closed/open/half_open)
// as of the last update.
type DestStat struct {
	PK      string `json:"pk"`
	Total   uint64 `json:"total"`
	Failed  uint64 `json:"failed"`
	Circuit string `json:"circuit,omitempty"`
}

// CircuitState is the per-destination breaker state. "closed" = normal,
// "open" = fast-fail new setups, "half_open" = allow one probe to test
// recovery.
type CircuitState string

const (
	// CircuitClosed means setups to this destination proceed normally.
	CircuitClosed CircuitState = "closed"
	// CircuitOpen means setups to this destination are short-circuited.
	CircuitOpen CircuitState = "open"
	// CircuitHalfOpen means one probe setup is allowed to test recovery.
	CircuitHalfOpen CircuitState = "half_open"
)

// BreakerState is the JSON view of one circuit breaker that is not
// closed. Snapshot only emits non-closed entries, so an empty
// "breakers" object means route setup is short-circuiting nobody —
// and a lingering half_open entry with probe_in_flight=true is
// immediately visible to an operator instead of only showing up as
// "probe in flight" strings in the caller's error.
type BreakerState struct {
	State            CircuitState `json:"state"`
	OpenedAt         time.Time    `json:"opened_at"`
	ConsecutiveFails int          `json:"consecutive_fails"`
	ProbeInFlight    bool         `json:"probe_in_flight"`
}

// Circuit breaker tuning. Kept as constants for simplicity — can be
// promoted to CollectorConfig if tests need to vary them.
const (
	// circuitFailureThreshold is the number of consecutive failures
	// to a destination that trips the breaker to OPEN.
	//
	// Lowered from 5 → 3 after the 1.3.59-era observation that
	// dmsg-error-202 (intermediate's stale dmsg session) consistently
	// produces 3+ failures back-to-back; tolerating 5 wastes the next
	// two setup attempts on doomed routes. With the open duration
	// tuned to dmsg-session-refresh cadence (below), tripping earlier
	// is the right balance.
	circuitFailureThreshold = 3
	// circuitOpenDuration is how long the breaker stays OPEN before
	// transitioning to HALF_OPEN to allow a probe setup.
	//
	// Bumped from 60s → 5min to align with dmsg's own session-refresh
	// cadence (~5min). dmsg-error-202 means a visor is registered in
	// dmsg-disc but its actual delegated-server session is stale; the
	// session re-publishes itself on a ~5min cycle, so retrying inside
	// that window is doomed to hit the same stale state. 60s let
	// half-open probes burn through the same failure on every cycle.
	circuitOpenDuration = 5 * time.Minute
	// circuitMaxOpenDuration is the maximum time a breaker can stay
	// in the open/half_open cycle before being force-reset to closed.
	// This prevents permanent lockout when the RSN's own DMSG sessions
	// are stale but the destination is actually alive — fresh traffic
	// will establish new DMSG paths.
	//
	// Bumped from 10min → 30min in concert with the longer open
	// duration; with 5min between half-open probes, 10min is only two
	// retry windows which is tight for a genuinely-down peer waiting
	// to come back. 30min gives ~6 half-open probes before force-reset.
	circuitMaxOpenDuration = 30 * time.Minute
	// circuitFailureWindow bounds how long consecutive failures must
	// occur within to count toward the threshold. Failures older than
	// this window are considered stale and the consecutive counter is
	// reset on the next record.
	circuitFailureWindow = 5 * time.Minute
)

// circuitBreaker tracks per-destination consecutive-failure state
// separately from DestStat counters so the breaker's view isn't
// perturbed by Reset().
type circuitBreaker struct {
	consecutiveFails int
	firstFailAt      time.Time
	state            CircuitState
	openedAt         time.Time // last open/re-open time (reset on each half_open→open transition)
	firstOpenedAt    time.Time // first time the breaker tripped; used for circuitMaxOpenDuration
	probeInFlight    bool      // a single half-open probe is in flight; gate concurrent probes
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

	// RequestsByKind / RoutesByKind split the request count by the setup PATH
	// it took — single, batch, cascade_sign (see kinds.go). Aggregate, so both
	// are part of the PUBLIC view: they name no visor.
	RequestsByKind map[SetupKind]uint64 `json:"requests_by_kind"`
	RoutesByKind   map[SetupKind]uint64 `json:"routes_by_kind"`

	// Batch is the batched-path summary: sizes seen and per-hop RPCs the
	// coalescing saved. Also aggregate, also public.
	Batch BatchStats `json:"batch"`

	// Top destinations sorted by total request count (descending).
	// Capped at the collector's top-N config.
	TopDestinations []DestStat `json:"top_destinations"`

	// Destinations ranked by failure count (descending). Same cap.
	TopFailedDestinations []DestStat `json:"top_failed_destinations"`

	// Most recent N failures, newest first.
	RecentFailures []FailureEvent `json:"recent_failures"`

	// Breakers holds every circuit breaker that is not closed, keyed
	// by PK — destinations and intermediates alike. Omitted when all
	// breakers are closed.
	Breakers map[string]BreakerState `json:"breakers,omitempty"`

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

	// Per-(source, destination) counters, so a non-whitelisted caller can be
	// served its OWN rows instead of an empty table (own_view.go). Shares
	// destCapacity; cleared with dests by Reset.
	pairs map[string]*DestStat

	// Per-destination circuit breaker state. Shares the dests cap —
	// when a new destination is added to dests we also add a breaker
	// entry; Reset() clears both maps atomically.
	breakers map[string]*circuitBreaker

	// Ring buffer of recent failures. Oldest at index failureRingIdx,
	// newest at (failureRingIdx-1) mod len.
	failureRing    []FailureEvent
	failureRingIdx int
	failureRingLen int

	lastSuccessAt time.Time
	lastFailureAt time.Time

	// Which setup PATH each request took (see kinds.go). Lazily allocated so
	// a collector that never sees a request carries no maps.
	requestsByKind map[SetupKind]uint64
	routesByKind   map[SetupKind]uint64

	// Batched-path counters (see kinds.go).
	batches         uint64
	batchRoutes     uint64
	batchInstalled  uint64
	routesPerBatch  map[int]uint64
	perHopRPCsSaved uint64
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
		breakers:      make(map[string]*circuitBreaker),
		failureRing:   make([]FailureEvent, cfg.FailureRingSize),
	}
}

// ProbeHolder records which PKs a single request was admitted through
// as the half-open probe, so finish() can resolve every one of them
// when that request ends. Admission and resolution MUST be symmetric:
// a probe left in flight past the end of the request that took it
// locks the breaker's half-open slot until circuitMaxOpenDuration
// force-resets it (30 minutes of refusing everybody), which is exactly
// the failure observed live on 2026-09-16 for an intermediate that was
// perfectly reachable.
//
// The zero value is ready to use. Create one per request, pass it to
// every Allow* call that request makes, and hand the same holder to
// RecordRouteContextProbes.
type ProbeHolder struct {
	mu  sync.Mutex
	pks []string
}

// hold records that this request took pk's half-open probe slot.
func (p *ProbeHolder) hold(pk string) {
	if p == nil || pk == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, held := range p.pks {
		if held == pk {
			return
		}
	}
	p.pks = append(p.pks, pk)
}

// take returns the held PKs and empties the holder, so a probe can
// never be resolved twice.
func (p *ProbeHolder) take() []string {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	pks := p.pks
	p.pks = nil
	return pks
}

// AllowDestination reports whether a new route setup to dst should
// proceed. Returns (true, "") when the breaker is closed or
// half-open, (false, reason) when it is open. Call this from the
// request handler BEFORE doing any dial work — rejecting early is
// the whole point.
//
// The half-open transition is driven here: when we find an open
// breaker that has been open for circuitOpenDuration, we flip it to
// half-open and allow the current probe through. A subsequent success
// will close the breaker (via finish); a failure re-opens it and
// resets the per-cycle timer.
//
// held records the probe against the calling request; pass the same
// *ProbeHolder that was given to RecordRouteContextProbes so finish()
// can release the slot however the request ends. A nil holder is
// accepted for callers that only inspect the breaker and never run a
// request through finish() (tests, diagnostics) — such a caller never
// takes the probe slot.
//
// AllowIntermediate is the per-intermediate sibling — see below.
func (c *Collector) AllowDestination(dstPK cipher.PubKey, held *ProbeHolder) (bool, string) {
	return c.allowPK(dstPK, held)
}

// AllowIntermediate reports whether a route candidate whose path
// traverses interPK should be allowed to proceed. Intermediate
// breakers are populated by id_reservation failures attributed to
// the intermediate (see the default branch in finish()). Callers
// that have already computed a candidate route's intermediates
// (typically the route-setup-node, or a router that wants to
// pre-filter route-finder output) can call this for each hop and
// reject the route early if any intermediate's breaker is open —
// avoiding a ~10s id-reservation timeout per known-bad hop.
//
// Uses the same allowPK helper as AllowDestination; intermediate
// breakers live in the same map as destination breakers (keyed by
// PK string), which keeps the bookkeeping uniform — including the
// half-open probe slot, which is why held matters here just as much
// as it does for the destination.
func (c *Collector) AllowIntermediate(interPK cipher.PubKey, held *ProbeHolder) (bool, string) {
	return c.allowPK(interPK, held)
}

// allowPK is the shared half-open transition machinery for
// destinations and intermediates.
func (c *Collector) allowPK(pubKey cipher.PubKey, held *ProbeHolder) (bool, string) {
	pk := pubKey.String()
	if pk == "" {
		return true, ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	br, ok := c.breakers[pk]
	if !ok || br.state == CircuitClosed {
		return true, ""
	}
	// Force-reset after circuitMaxOpenDuration — the destination may
	// be alive but the RSN's own DMSG sessions are stale. Letting
	// fresh traffic through will establish new DMSG paths.
	if !br.firstOpenedAt.IsZero() && time.Since(br.firstOpenedAt) >= circuitMaxOpenDuration {
		br.state = CircuitClosed
		br.openedAt = time.Time{}
		br.firstOpenedAt = time.Time{}
		br.consecutiveFails = 0
		br.firstFailAt = time.Time{}
		br.probeInFlight = false
		if d, ok := c.dests[pk]; ok {
			d.Circuit = string(CircuitClosed)
		}
		return true, ""
	}
	if br.state == CircuitOpen {
		if time.Since(br.openedAt) < circuitOpenDuration {
			return false, "circuit open: " + pk + " unreachable"
		}
		// Time's up — transition to half-open and let THIS caller be the
		// single probe. An observer (nil holder) is allowed through but
		// does not consume the probe slot, since nothing will release it.
		if held == nil {
			return true, ""
		}
		br.state = CircuitHalfOpen
		br.probeInFlight = true
		held.hold(pk)
		if d, ok := c.dests[pk]; ok {
			d.Circuit = string(CircuitHalfOpen)
		}
		return true, ""
	}
	// Already HalfOpen: admit only ONE probe at a time. Concurrent callers are
	// rejected until finish() resolves the probe (Closed on success / Open on
	// failure / released back to Open when the request died for a reason that
	// says nothing about this PK — all three clear probeInFlight). Without
	// this, every caller arriving during the half-open window stampedes the
	// recovering destination — a thundering herd against exactly the node that
	// just came back, which can re-trip the breaker it was meant to test.
	if br.probeInFlight {
		return false, "circuit half-open: probe in flight for " + pk
	}
	if held == nil {
		return true, ""
	}
	br.probeInFlight = true
	held.hold(pk)
	return true, ""
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
	return c.RecordRouteContextProbes(ctx, srcPK, dstPK, hopCount, nil)
}

// RecordRouteContextProbes is RecordRouteContext plus the half-open
// probe bookkeeping: held is the same *ProbeHolder the request passes
// to AllowDestination / AllowIntermediate, and every probe it holds is
// resolved when the returned closure runs. Callers that consult the
// breakers MUST use this variant — a probe taken by a request that
// never releases it blocks the breaker's only recovery slot until the
// 30-minute force-reset.
//
//	var probes setupmetrics.ProbeHolder
//	defer collector.RecordRouteContextProbes(ctx, src, dst, hopCount, &probes)(&err)
func (c *Collector) RecordRouteContextProbes(ctx context.Context, srcPK, dstPK cipher.PubKey, hopCount int, held *ProbeHolder) func(*error) {
	start := time.Now()
	return func(errp *error) {
		c.finish(ctx, srcPK, dstPK, hopCount, start, errp, held)
	}
}

// RecordConcurrencyDrop is called when a request is refused at the
// accept-loop backpressure check (never reaches the handler).
func (c *Collector) RecordConcurrencyDrop() {
	c.mu.Lock()
	c.concurrencyDrops++
	c.mu.Unlock()
}

func (c *Collector) finish(ctx context.Context, srcPK, dstPK cipher.PubKey, hopCount int, start time.Time, errp *error, held *ProbeHolder) {
	duration := time.Since(start)
	durMs := duration.Milliseconds()

	var err error
	if errp != nil {
		err = *errp
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Every half-open probe this request took is resolved before we
	// return, whichever branch below runs — see resolveProbesLocked.
	probes := held.take()

	c.total++
	dstStr := dstPK.String()
	destStat := c.touchDest(dstStr)
	// The same counters keyed by (source, destination), so a caller that is not
	// on the survey whitelist can still be served its OWN rows (own_view.go).
	pairStat := c.touchPair(srcPK.String(), dstStr)

	if err == nil {
		c.success++
		c.lastSuccessAt = time.Now()
		if destStat != nil {
			destStat.Total++
		}
		if pairStat != nil {
			pairStat.Total++
		}
		// Any success closes an open/half-open breaker and resets
		// the consecutive failure counter — for the destination and
		// for every intermediate this request probed, since the route
		// that just succeeded ran through all of them.
		c.recordCircuitSuccessLocked(dstStr)
		c.resolveProbesLocked(probes, "", true)
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
	if pairStat != nil {
		pairStat.Total++
	}
	if destStat != nil {
		destStat.Total++
		// destStat.Failed is incremented below, but only when the
		// failure is actually attributable to the destination —
		// see the switch on failedPK.
	}

	// Update the circuit breaker for this destination. Only id_reservation
	// failures (dial-path failures) drive the breaker — other failure
	// modes like rule_generation reflect local config problems that
	// won't self-heal by waiting, so they shouldn't trip the breaker.
	//
	// Critically: id_reservation can fail because the SOURCE visor or
	// an INTERMEDIATE visor is unreachable (MakeMap dials every hop —
	// src, dst, intermediaries). A flaky source/intermediate visor
	// would otherwise poison the destination's breaker, blocking all
	// requests to a healthy destination just because some other visor
	// in the path was unreachable. We extract the PK that actually
	// failed to dial (from router.DialError) and only trip the
	// destination's breaker when it matches the destination.
	//
	// Source-side failures are reclassified as source_unreachable.
	// Intermediate-side failures are reclassified as
	// intermediate_unreachable; the intermediate's own breaker is
	// tripped so the next route-finder pick that traverses this
	// intermediate can be short-circuited via AllowIntermediate.
	// In both cases the destination's Failed counter is NOT
	// incremented so the Top-Failed-Destinations table stops
	// fingering innocent dsts.
	//
	// The mux-disjoint case (N routes through N different
	// intermediates) is the motivating scenario: under the old
	// behavior, even one bad intermediate per route would accumulate
	// circuitFailureThreshold hits on the dst and lock all parallel
	// attempts out — including ones through healthy intermediates.
	reason := classifyError(ctx, err)
	blameDst := true
	// blamedPK is the breaker this failure was actually recorded
	// against, if any. Probes held on any OTHER PK are released
	// un-penalized below: this request learned nothing about them.
	var blamedPK string
	if reason == ReasonIDReservation {
		failedPK, ok := failedDialPK(err)
		switch {
		case !ok:
			// Failed PK not identifiable (pre-DialError wrapping or a
			// non-dial id_reservation failure such as the RPC call
			// itself returning an error). Fall back to the legacy
			// behavior of blaming the destination.
			c.recordCircuitFailureLocked(dstStr)
			blamedPK = dstStr
		case failedPK == dstPK:
			// Destination was the dial target that failed — legit.
			c.recordCircuitFailureLocked(dstStr)
			blamedPK = dstStr
		case failedPK == srcPK:
			// Source visor was unreachable. Do NOT blame the dst.
			reason = ReasonSourceUnreachable
			blameDst = false
		default:
			// Intermediate hop failed. Do NOT blame the dst — a
			// disjoint-mux scenario can have N parallel routes through
			// N different intermediates, and one bad intermediate
			// shouldn't lock the dst out for the other (N-1) good
			// ones. Track failures against the intermediate's own
			// breaker so future setups can short-circuit on a known-
			// bad intermediate.
			reason = ReasonIntermediateUnreachable
			blameDst = false
			c.touchDest(failedPK.String()) // ensure breaker entry exists
			c.recordCircuitFailureLocked(failedPK.String())
			blamedPK = failedPK.String()
		}
	}
	// Release every probe this request held that was NOT the one we
	// just blamed. The request failed for a reason that says nothing
	// about those PKs (the destination was unreachable, biRt.Check
	// rejected the route, rule generation failed, another hop's
	// breaker short-circuited us), so they go back to Open with
	// probeInFlight cleared and openedAt untouched — the next caller
	// gets to be the probe instead of everybody being refused until
	// the 30-minute force-reset.
	c.resolveProbesLocked(probes, blamedPK, false)
	if blameDst && destStat != nil {
		destStat.Failed++
	}
	if blameDst && pairStat != nil {
		pairStat.Failed++
	}
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
	d := &DestStat{PK: pk, Circuit: string(CircuitClosed)}
	c.dests[pk] = d
	c.breakers[pk] = &circuitBreaker{state: CircuitClosed}
	return d
}

// resolveProbesLocked settles every half-open probe a finishing
// request held. success closes the breakers (the route proved the PK
// reachable); otherwise each probe except blamedPK — whose breaker the
// failure path already updated — is released back to Open without a
// penalty. Must be called with c.mu held.
//
// The invariant this enforces: probeInFlight is never true after the
// request that set it has ended. AllowDestination / AllowIntermediate
// admit exactly one probe per PK and finish() is the only place that
// releases it, so the two have to cover every exit path of the
// request, not just the dial-failed one.
func (c *Collector) resolveProbesLocked(probes []string, blamedPK string, success bool) {
	for _, pk := range probes {
		if pk == "" || pk == blamedPK {
			continue
		}
		if success {
			c.recordCircuitSuccessLocked(pk)
			continue
		}
		c.releaseProbeLocked(pk)
	}
}

// releaseProbeLocked returns a half-open breaker to Open with the
// probe slot free and openedAt unchanged, so the next request through
// this PK becomes the probe. Consecutive-failure state is left alone:
// the request that held this probe failed for reasons unrelated to pk,
// so it is not evidence against pk. Must be called with c.mu held.
func (c *Collector) releaseProbeLocked(pk string) {
	br, ok := c.breakers[pk]
	if !ok || !br.probeInFlight {
		return
	}
	br.probeInFlight = false
	// A concurrent success may already have closed the breaker; don't
	// drag it back open.
	if br.state != CircuitHalfOpen {
		return
	}
	br.state = CircuitOpen
	if d, ok := c.dests[pk]; ok {
		d.Circuit = string(CircuitOpen)
	}
}

// recordCircuitFailureLocked updates the breaker state for pk after a
// failed id_reservation. Must be called with c.mu held.
func (c *Collector) recordCircuitFailureLocked(pk string) {
	if pk == "" {
		return
	}
	br, ok := c.breakers[pk]
	if !ok {
		// Destination cap was reached and touchDest returned nil;
		// without a DestStat we also have no breaker to update.
		return
	}
	now := time.Now()
	// Reset the consecutive counter if the oldest failure is outside
	// the window — breaker only fires on a burst, not on a slow trickle.
	if br.consecutiveFails > 0 && now.Sub(br.firstFailAt) > circuitFailureWindow {
		br.consecutiveFails = 0
	}
	if br.consecutiveFails == 0 {
		br.firstFailAt = now
	}
	br.consecutiveFails++

	// Half-open → failure re-opens the breaker and resets the per-cycle timer.
	// firstOpenedAt is NOT reset — it tracks the total time since the
	// breaker first tripped so circuitMaxOpenDuration can force-close it.
	if br.state == CircuitHalfOpen {
		br.state = CircuitOpen
		br.openedAt = now
		br.probeInFlight = false
		if d, ok := c.dests[pk]; ok {
			d.Circuit = string(CircuitOpen)
		}
		return
	}
	// Closed → trip to open once the threshold is reached.
	if br.state == CircuitClosed && br.consecutiveFails >= circuitFailureThreshold {
		br.state = CircuitOpen
		br.openedAt = now
		br.firstOpenedAt = now
		if d, ok := c.dests[pk]; ok {
			d.Circuit = string(CircuitOpen)
		}
	}
}

// recordCircuitSuccessLocked transitions the breaker to Closed on any
// success. Must be called with c.mu held.
func (c *Collector) recordCircuitSuccessLocked(pk string) {
	if pk == "" {
		return
	}
	br, ok := c.breakers[pk]
	if !ok {
		return
	}
	br.consecutiveFails = 0
	br.firstFailAt = time.Time{}
	br.probeInFlight = false
	if br.state != CircuitClosed {
		br.state = CircuitClosed
		br.openedAt = time.Time{}
		br.firstOpenedAt = time.Time{}
		if d, ok := c.dests[pk]; ok {
			d.Circuit = string(CircuitClosed)
		}
	}
}

// failedDialPK walks the error chain looking for a router.DialError
// (matched via an anonymous interface to avoid importing router here,
// which would create an import cycle). Returns the PK that could not
// be dialed, or the zero value + false if no such error is in the
// chain. See the comment in finish() for why the caller cares.
func failedDialPK(err error) (cipher.PubKey, bool) {
	type dialFailed interface {
		DialFailedPK() cipher.PubKey
	}
	for err != nil {
		if d, ok := err.(dialFailed); ok {
			return d.DialFailedPK(), true
		}
		err = errors.Unwrap(err)
	}
	return cipher.PubKey{}, false
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
	case strings.Contains(s, "destination circuit breaker open"),
		strings.Contains(s, "circuit open"):
		return ReasonCircuitOpen
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

	// Which setup path each request took, and what batching bought.
	snap.RequestsByKind, snap.RoutesByKind = c.kindCountsLocked()
	snap.Batch = c.batchStatsLocked()

	// Top destinations (by total) and top failed destinations.
	snap.TopDestinations, snap.TopFailedDestinations = c.topDestsLocked(10)

	// Recent failures, newest first.
	snap.RecentFailures = c.recentFailuresLocked()

	// Non-closed breakers, so an operator can see what route setup is
	// refusing and why without reading the caller's error strings.
	snap.Breakers = c.breakerStatesLocked()

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
	c.pairs = nil
	c.breakers = make(map[string]*circuitBreaker)
	for i := range c.failureRing {
		c.failureRing[i] = FailureEvent{}
	}
	c.failureRingIdx = 0
	c.failureRingLen = 0
	c.lastSuccessAt = time.Time{}
	c.lastFailureAt = time.Time{}
	c.requestsByKind = nil
	c.routesByKind = nil
	c.batches, c.batchRoutes, c.batchInstalled, c.perHopRPCsSaved = 0, 0, 0, 0
	c.routesPerBatch = nil
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
	byTotal = sortDestStats(byTotal, false, n)

	// Sort-by-failed copy, skipping any with zero failures.
	byFailed = make([]DestStat, 0, len(all))
	for _, d := range all {
		if d.Failed > 0 {
			byFailed = append(byFailed, d)
		}
	}
	byFailed = sortDestStats(byFailed, true, n)
	return byTotal, byFailed
}

// sortDestStats orders rows by total (or by failure count when byFailed) and
// truncates to n, breaking ties on the key so the output is deterministic.
// Shared by the whitelisted table and the caller-scoped one in own_view.go.
func sortDestStats(rows []DestStat, byFailed bool, n int) []DestStat {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i].Total, rows[j].Total
		if byFailed {
			a, b = rows[i].Failed, rows[j].Failed
		}
		if a != b {
			return a > b
		}
		return rows[i].PK < rows[j].PK
	})
	if n > 0 && len(rows) > n {
		rows = rows[:n]
	}
	if len(rows) == 0 {
		return nil
	}
	return rows
}

// breakerStatesLocked copies the non-closed breakers out for
// Snapshot. Returns nil when every breaker is closed so the field
// stays out of the JSON entirely.
func (c *Collector) breakerStatesLocked() map[string]BreakerState {
	var out map[string]BreakerState
	for pk, br := range c.breakers {
		if br.state == CircuitClosed {
			continue
		}
		if out == nil {
			out = make(map[string]BreakerState)
		}
		out[pk] = BreakerState{
			State:            br.state,
			OpenedAt:         br.openedAt.UTC(),
			ConsecutiveFails: br.consecutiveFails,
			ProbeInFlight:    br.probeInFlight,
		}
	}
	return out
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
