// Package router pkg/router/transport_selector.go c2-net-routing
package router

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/transport"
)

// WeightMode controls how the transport selector distributes packets.
type WeightMode int

const (
	// WeightModeAuto uses latency-based weighting (faster transports get more packets).
	// Falls back to equal round-robin when no latency data is available.
	WeightModeAuto WeightMode = iota
	// WeightModeEqual distributes packets equally across all transports (round-robin).
	WeightModeEqual
	// WeightModeExplicit uses operator-supplied fractional
	// weights stored in explicitWeights. Normalized into an
	// integer schedule at Rebuild time. Set via the routing-
	// policy DSL's `distribution="weighted: f1, f2, ..."`.
	WeightModeExplicit
	// WeightModeSizeThreshold routes packets by payload size,
	// not by a pre-built schedule: > sizeThreshold goes to leg
	// 0, smaller packets RR across the rest. The Select()
	// fallback path returns 0 for callers that don't supply a
	// size (handshake / control packets); SelectForSize is the
	// primary entry point.
	WeightModeSizeThreshold
	// WeightModeSticky5Tuple hashes the IPv4 5-tuple of the
	// payload (src/dst IP + src/dst port + protocol) and picks
	// a leg by hash % live-leg-count. Same flow → same leg
	// always, deterministic. SelectForPayload is the entry
	// point; non-IPv4 payloads fall back to a payload-prefix
	// FNV hash.
	WeightModeSticky5Tuple
	// WeightModeLatencyAdaptive picks the live leg with the
	// lowest current GetLatency() per packet. Differs from
	// WeightModeAuto (latency-weighted schedule, rebuilt
	// periodically): adaptive evaluates per-packet, so a leg
	// whose RTT spikes mid-session loses traffic immediately.
	// Cost is one linear scan over the leg array per packet
	// (small constant for typical mux counts).
	WeightModeLatencyAdaptive
	// WeightModeDSCPPriority reads the IPv4 DSCP (upper 6 bits
	// of payload[1]) and routes packets >= threshold to leg 0,
	// others round-robin across the rest. Non-IPv4 payloads
	// fall back to round-robin.
	WeightModeDSCPPriority
	// WeightModeCapacity weights each leg by its recently-measured
	// throughput (capacityWeights, set by the mux from per-leg
	// byte deltas), with a per-leg exploration floor so every live
	// leg keeps a share. Bootstraps as equal round-robin until the
	// mux has throughput samples. Contrast WeightModeAuto, which
	// weights by inverse LATENCY and so starves a fat-but-slow leg;
	// capacity fills each leg toward its bandwidth — the mode that
	// actually aggregates throughput across a disjoint mux.
	WeightModeCapacity
	// WeightModeECF is a PREDICTIVE hold-back scheduler adapted from ECF
	// (Earliest Completion First, Lim et al., CoNEXT 2017). Capacity/auto
	// weighting keeps assigning a share of in-order frames to slower legs; in
	// a reorder buffer that must deliver strictly in order, every frame on a
	// slow leg is a head-of-line stall, so goodput/latency-proportional
	// spraying does NOT aggregate under path heterogeneity (~25% of ideal in
	// the MPTCP/MP-QUIC literature). ECF instead sends on the fastest leg
	// while it has send capacity, and spills onto a slower leg ONLY when the
	// slow leg would deliver its frame sooner than the fast leg can drain its
	// own backlog — otherwise it holds the frame on the fast leg. skywire has
	// no TCP cwnd, so the cwnd/"has capacity" test is adapted to a per-leg
	// bandwidth-delay-product (rate*RTT) and a selector-maintained in-flight
	// byte estimate; see ecfPick and SelectECF. This is the aggregation mode
	// that stops a heterogeneous mux from HoL-stalling on its slow legs.
	WeightModeECF
)

// ECF (WeightModeECF) tuning constants. Starting values — expect a live-tuning
// pass. See ecfPick for how each is used.
const (
	// ecfBeta is the ECF hysteresis constant. Once the scheduler decides to
	// hold a frame on the fast leg (waiting latched), it inflates the slow
	// leg's delivery estimate by (1+ecfBeta) on the next pick, so a marginal
	// leg does not flap in and out of the spill set packet-to-packet.
	ecfBeta = 0.25
	// ecfDefaultFrameBytes is the in-flight increment charged to a leg for a
	// frame whose size the caller did not supply (control/handshake frames
	// never reach the ECF pick, so this is only a defensive fallback).
	ecfDefaultFrameBytes = 1024
	// ecfRttAlpha / ecfJitterAlpha weight the newest sample in the per-leg
	// mean-RTT and jitter (sigma) EWMAs the mux maintains for ECF (see
	// route_mux.go rebuildWeights). Jitter is the ECF sigma margin.
	ecfRttAlpha    = 0.3
	ecfJitterAlpha = 0.3
	// ecfColdBootstrapBytes bounds how much a leg of unknown capacity (no rate
	// sample yet, cwndBytes==0) may carry before ecfSaturated forces a spill.
	// Without it a cold leg is "unlimited", so at download start — when every
	// leg is cold — ECF returns the single lowest-RTT leg for every frame and
	// dumps the whole stream on it until the first ~5s rate refresh; that leg
	// then congests and stalls the global reorder frontier. A bounded probe
	// budget makes cold start fan out across the ready legs (measuring each)
	// instead of hammering one. ~1 BDP of a 250ms/2Mbps leg.
	ecfColdBootstrapBytes = 64 * 1024
	// ecfCongestRttFactor marks a leg saturated (shed load off it) once its
	// current mean RTT has ballooned past this multiple of its own baseline
	// (minimum observed) RTT — the queue-buildup signature of a bandwidth-
	// congested leg. Guards against the BDP trap: cwnd = rate*RTT grows with
	// RTT, so an inflating RTT would otherwise raise a stalling leg's apparent
	// capacity and make ECF feed it more, not less.
	ecfCongestRttFactor = 4.0
	// ecfRttMinCreep is the per-refresh fraction of the (mean-baseline) RTT gap
	// by which a leg's baseline RTT creeps upward when no lower sample is seen.
	// Keeps the baseline a true floor against transient congestion while still
	// tracking a leg whose genuine latency has risen for good. Applied on the
	// ~5s rebuildWeights cadence, so ~0.02 ≈ a minutes-scale adaptation.
	ecfRttMinCreep = 0.02
)

// ecfLegState is the per-leg snapshot the ECF scheduler reasons over — one
// entry per leg index (parallel to the route group's tps[]). The rate/RTT/
// jitter/ready fields are refreshed by the mux via SetECFState on the
// rebuildWeights cadence; inflightBytes is maintained by the selector itself
// between refreshes (incremented per selected frame, drained by rate over
// wall-clock), so it survives a refresh (SetECFState carries it forward).
type ecfLegState struct {
	// rttMs is the leg's mean round-trip latency estimate in ms (EWMA of
	// tp.GetLatency()); 0 = unknown (deprioritized in the fast-leg pick).
	rttMs float64
	// rttMinMs is the leg's baseline (minimum observed) RTT in ms — the
	// uncongested latency. Used two ways: as the stable BDP latency for
	// cwndBytes (so congestion can't inflate a stalling leg's capacity) and,
	// against the live rttMs, as the congestion signal in ecfSaturated. 0 =
	// unknown (no congestion check, cwnd falls back to the live RTT).
	rttMinMs float64
	// jitterMs is the ECF sigma: an EWMA of |sample-mean| RTT deviation, used
	// as the inter-leg jitter margin `d` in the hold-back predicate.
	jitterMs float64
	// rateBps is the leg's recent send goodput in bytes/sec (from the mux's
	// per-leg sent-byte delta over the refresh window); 0 = unknown.
	rateBps float64
	// cwndBytes is the ECF cwnd substitute: the leg's bandwidth-delay product
	// (rateBps * rttMs/1000) — the bytes it can hold in flight in one RTT.
	// 0 when rate or RTT is unknown, which ecfSaturated treats as "unlimited"
	// so a cold leg is used (and thus measured) rather than assumed full.
	cwndBytes float64
	// ready is true when the leg may be selected for sending (alive, its rule
	// confirmed, not a warm standby). selectTransport re-validates readiness
	// after the pick, so this is an optimization, not the safety gate.
	ready bool
	// inflightBytes is the selector's estimate of bytes sent-but-not-yet-
	// delivered on this leg. NOT set by SetECFState — carried across refreshes.
	inflightBytes float64
}

// transportSelector implements weighted transport selection based on latency.
// Faster transports (lower latency) get proportionally more packets.
// Falls back to equal-weight round-robin when latency data is unavailable.
type transportSelector struct {
	mu       sync.RWMutex
	schedule []int // pre-computed selection sequence of transport indices
	counter  uint32
	mode     WeightMode

	// Mode-specific config. Set via SetExplicitWeights or
	// SetSizeThreshold; the next Rebuild picks them up.
	explicitWeights []float64
	sizeThreshold   int
	dscpThreshold   int
	// smallLegSchedule holds the round-robin schedule across the
	// non-leg-0 transports used by WeightModeSizeThreshold,
	// WeightModeDSCPPriority, and as the fallback for size-aware
	// modes when the packet is at or under the threshold. Built
	// at Rebuild time so per-packet selection stays lock-free.
	smallLegSchedule []int
	smallLegCounter  uint32
	// liveLegs holds the indexes of currently-serving transports,
	// used by WeightModeSticky5Tuple (hash %% len) and
	// WeightModeLatencyAdaptive (linear scan). Built at Rebuild
	// time.
	liveLegs []int
	// liveTps holds pointers to the same transports as liveLegs,
	// for adaptive's GetLatency lookups — avoids reaching back
	// through the route group's tps[] under a lock per packet.
	liveTps []*transport.ManagedTransport
	// capacityWeights holds per-leg recent throughput (bytes moved
	// since the last rebuild), one entry per leg index, used by
	// WeightModeCapacity. Set by the mux via SetCapacityWeights
	// before each Rebuild. Empty / all-zero → equal bootstrap.
	capacityWeights []float64
	// ecfLegs holds the per-leg ECF state used by WeightModeECF, one entry
	// per leg index. Refreshed by the mux via SetECFState; the inflightBytes
	// field is maintained by SelectECF between refreshes. All ECF state is
	// guarded by ts.mu — SelectECF takes the write lock because it mutates
	// inflightBytes (per-packet picks are already serialized per route group
	// by the caller's rg.mu, so the lock is uncontended except vs the ~5s
	// SetECFState refresh).
	ecfLegs []ecfLegState
	// ecfWaiting latches ECF's hold-back hysteresis (the `waiting` state in
	// the paper): true after a pick chose to hold on the fast leg, so the
	// next pick inflates the slow-leg delivery estimate by (1+ecfBeta).
	ecfWaiting bool
	// ecfLastNano is the wall-clock (UnixNano) of the previous SelectECF call,
	// used to drain each leg's inflightBytes by its rate over the elapsed gap.
	ecfLastNano int64
}

func newTransportSelector() *transportSelector {
	return &transportSelector{mode: WeightModeAuto}
}

// SetExplicitWeights stores operator-supplied fractional weights
// used by WeightModeExplicit. Caller must call Rebuild afterwards
// for them to take effect. Passing a non-empty slice does NOT
// implicitly switch mode — call SetMode(WeightModeExplicit)
// explicitly when the operator's intent is to use them.
func (ts *transportSelector) SetExplicitWeights(w []float64) {
	ts.mu.Lock()
	ts.explicitWeights = append([]float64(nil), w...)
	ts.mu.Unlock()
}

// SetCapacityWeights stores per-leg recent-throughput weights used
// by WeightModeCapacity. Caller (the mux) recomputes these from
// per-leg byte deltas and calls Rebuild afterwards. A copy is kept
// so the caller may reuse its slice.
func (ts *transportSelector) SetCapacityWeights(w []float64) {
	ts.mu.Lock()
	ts.capacityWeights = append([]float64(nil), w...)
	ts.mu.Unlock()
}

// SetSizeThreshold stores the payload-size boundary used by
// WeightModeSizeThreshold. Caller must call Rebuild afterwards
// (or SetMode + Rebuild) for it to take effect.
func (ts *transportSelector) SetSizeThreshold(n int) {
	ts.mu.Lock()
	ts.sizeThreshold = n
	ts.mu.Unlock()
}

// SetDSCPThreshold stores the IPv4 DSCP boundary used by
// WeightModeDSCPPriority. Caller must call Rebuild afterwards.
func (ts *transportSelector) SetDSCPThreshold(n int) {
	ts.mu.Lock()
	ts.dscpThreshold = n
	ts.mu.Unlock()
}

// SetMode changes the weight mode and returns the previous mode.
func (ts *transportSelector) SetMode(mode WeightMode) WeightMode {
	ts.mu.Lock()
	prev := ts.mode
	ts.mode = mode
	ts.mu.Unlock()
	return prev
}

// Mode returns the current weight mode.
func (ts *transportSelector) Mode() WeightMode {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.mode
}

// String renders the weight mode as the same lowercase token the routing-
// policy DSL's DistributionMode uses, so an operator reading `visor state`
// sees exactly the value they would set via a policy's `distribution=`.
func (m WeightMode) String() string {
	switch m {
	case WeightModeAuto:
		return "auto"
	case WeightModeEqual:
		return "round-robin"
	case WeightModeExplicit:
		return "weighted"
	case WeightModeSizeThreshold:
		return "size-threshold"
	case WeightModeSticky5Tuple:
		return "sticky:5tuple"
	case WeightModeLatencyAdaptive:
		return "latency-adaptive"
	case WeightModeDSCPPriority:
		return "dscp-priority"
	case WeightModeCapacity:
		return "capacity"
	case WeightModeECF:
		return "ecf"
	}
	return "unknown"
}

// SetECFState stores the per-leg ECF state used by WeightModeECF. Caller (the
// mux) recomputes rate/RTT/jitter/ready each refresh and calls Rebuild
// afterwards. The selector-maintained inflightBytes estimate is carried
// forward per leg index across refreshes so a refresh does not reset the
// in-flight accounting. A copy is kept so the caller may reuse its slice.
func (ts *transportSelector) SetECFState(states []ecfLegState) {
	ts.mu.Lock()
	ns := make([]ecfLegState, len(states))
	copy(ns, states)
	for i := range ns {
		if i < len(ts.ecfLegs) {
			ns[i].inflightBytes = ts.ecfLegs[i].inflightBytes
		}
	}
	ts.ecfLegs = ns
	ts.mu.Unlock()
}

// Rebuild recomputes the selection schedule from the current transport latencies.
// Called periodically (e.g., every keep-alive cycle) and when transports change.
// tps must not be modified concurrently (caller holds RouteGroup.mu or equivalent).
func (ts *transportSelector) Rebuild(tps []*transport.ManagedTransport) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	n := len(tps)
	if n == 0 {
		ts.schedule = nil
		return
	}

	if n == 1 {
		ts.schedule = []int{0}
		return
	}

	// Equal mode: always round-robin regardless of latency
	if ts.mode == WeightModeEqual {
		schedule := make([]int, 0, n)
		for i, tp := range tps {
			if tp != nil && !tp.IsClosed() {
				schedule = append(schedule, i)
			}
		}
		if len(schedule) == 0 {
			schedule = []int{0}
		}
		ts.schedule = schedule
		return
	}

	// Capacity mode: weight each live leg by its recently-measured
	// throughput (capacityWeights), with a floor of 1 slot per live leg
	// so no leg is starved (and so keeps being measured). Until there IS
	// throughput to weigh by (bootstrap, or a fully-quiet flow) this
	// deliberately falls through to the latency-weighted Auto schedule
	// below rather than spraying equally — an equal bootstrap would put
	// 1/N of the very first packets down a slow aux leg and head-of-line
	// stall a heterogeneous mux before any capacity is known. Latency-
	// weighting is the safe cold-start; once bytes flow, the per-leg
	// deltas arrive and the schedule shifts to true capacity weighting.
	if ts.mode == WeightModeCapacity {
		type liveLeg struct {
			idx int
			w   float64
		}
		live := make([]liveLeg, 0, n)
		maxW := 0.0
		for i, tp := range tps {
			if tp == nil || tp.IsClosed() {
				continue
			}
			w := 0.0
			if i < len(ts.capacityWeights) && ts.capacityWeights[i] > 0 {
				w = ts.capacityWeights[i]
			}
			if w > maxW {
				maxW = w
			}
			live = append(live, liveLeg{idx: i, w: w})
		}
		// maxW == 0 → no throughput samples yet: fall through to Auto
		// (latency-weighted) for a safe cold-start. len(live)==0 can't
		// happen here (n>1 and closed legs are skipped, but at least one
		// is live in a real rebuild); guard anyway.
		if len(live) > 0 && maxW > 0 {
			// capacityBias caps how many extra slots the fattest leg
			// gets over the 1-slot floor. 7 → up to 8:1, enough to
			// steer bulk toward capacity without ever fully starving a
			// thin leg (its floor of 1 keeps it live and measured).
			const capacityBias = 7
			schedule := make([]int, 0, len(live)*(capacityBias+1))
			for _, l := range live {
				count := 1 + int(float64(capacityBias)*(l.w/maxW)+0.5)
				for j := 0; j < count; j++ {
					schedule = append(schedule, l.idx)
				}
			}
			ts.schedule = schedule
			return
		}
		// else fall through to Auto (latency-weighted) cold-start.
	}

	// Explicit mode: operator-supplied fractional weights, one
	// per leg. Normalize to integer multiples by dividing each by
	// the smallest positive weight (clamped to 1 for the leg to
	// appear at all), then build the schedule the same way Auto
	// does. Rounding (not truncation) so 0.3/0.1 normalizes to 3
	// instead of 2 — float-precision loss on division would
	// otherwise distort the operator's stated ratios.
	if ts.mode == WeightModeExplicit {
		if len(ts.explicitWeights) == 0 {
			// Misconfigured — fall back to equal.
			schedule := make([]int, 0, n)
			for i, tp := range tps {
				if tp != nil && !tp.IsClosed() {
					schedule = append(schedule, i)
				}
			}
			if len(schedule) == 0 {
				schedule = []int{0}
			}
			ts.schedule = schedule
			return
		}
		minW := 0.0
		for _, w := range ts.explicitWeights {
			if w > 0 && (minW == 0 || w < minW) {
				minW = w
			}
		}
		if minW == 0 {
			minW = 1
		}
		schedule := make([]int, 0, 16)
		for i, tp := range tps {
			if tp == nil || tp.IsClosed() {
				continue
			}
			w := 1.0
			if i < len(ts.explicitWeights) && ts.explicitWeights[i] > 0 {
				w = ts.explicitWeights[i]
			}
			count := int(w/minW + 0.5)
			if count < 1 {
				count = 1
			}
			for j := 0; j < count; j++ {
				schedule = append(schedule, i)
			}
		}
		if len(schedule) == 0 {
			schedule = []int{0}
		}
		ts.schedule = schedule
		return
	}

	// SizeThreshold mode: keep the primary schedule as just leg
	// 0 (the wide-pipe leg for large packets), and build a
	// separate round-robin over the remaining legs for small
	// packets. SelectForSize picks between them at write time;
	// Select() without a size returns leg 0 (the safer choice
	// for control packets whose layer doesn't know their size).
	if ts.mode == WeightModeSizeThreshold {
		ts.schedule = []int{0}
		smallSched := make([]int, 0, n-1)
		for i, tp := range tps {
			if i == 0 {
				continue
			}
			if tp != nil && !tp.IsClosed() {
				smallSched = append(smallSched, i)
			}
		}
		if len(smallSched) == 0 {
			// Only one leg active — small packets ride leg 0 too.
			smallSched = []int{0}
		}
		ts.smallLegSchedule = smallSched
		return
	}

	// DSCPPriority: same primary/small split as SizeThreshold,
	// but the gating predicate at write time reads the IP DSCP
	// instead of the payload length.
	if ts.mode == WeightModeDSCPPriority {
		ts.schedule = []int{0}
		smallSched := make([]int, 0, n-1)
		for i, tp := range tps {
			if i == 0 {
				continue
			}
			if tp != nil && !tp.IsClosed() {
				smallSched = append(smallSched, i)
			}
		}
		if len(smallSched) == 0 {
			smallSched = []int{0}
		}
		ts.smallLegSchedule = smallSched
		return
	}

	// Sticky5Tuple and LatencyAdaptive: build the live-leg
	// arrays. Sticky hashes mod len; adaptive scans for lowest
	// GetLatency. The primary schedule is also populated so a
	// caller using Select() (no payload) still gets a reasonable
	// leg.
	// ECF also builds the live-leg arrays: SelectECF reasons over ecfLegs
	// (set separately via SetECFState) but the mirrored schedule is the
	// Select() fallback for control/handshake frames that carry no payload.
	if ts.mode == WeightModeSticky5Tuple || ts.mode == WeightModeLatencyAdaptive || ts.mode == WeightModeECF {
		live := make([]int, 0, n)
		liveTps := make([]*transport.ManagedTransport, 0, n)
		for i, tp := range tps {
			if tp != nil && !tp.IsClosed() {
				live = append(live, i)
				liveTps = append(liveTps, tp)
			}
		}
		if len(live) == 0 {
			live = []int{0}
			liveTps = []*transport.ManagedTransport{tps[0]}
		}
		ts.liveLegs = live
		ts.liveTps = liveTps
		// Mirror in primary schedule as the Select() fallback.
		ts.schedule = append(ts.schedule[:0], live...)
		return
	}

	// Auto mode: weight by inverse latency
	weights := make([]int, n)
	maxLatency := 0.0
	hasLatency := false

	for i, tp := range tps {
		if tp == nil {
			weights[i] = 0
			continue
		}
		lat := tp.GetLatency()
		if lat > maxLatency {
			maxLatency = lat
		}
		if lat > 0 {
			hasLatency = true
		}
	}

	if !hasLatency {
		// No latency data: equal weights (pure round-robin)
		schedule := make([]int, 0, n)
		for i, tp := range tps {
			if tp != nil && !tp.IsClosed() {
				schedule = append(schedule, i)
			}
		}
		if len(schedule) == 0 {
			schedule = []int{0}
		}
		ts.schedule = schedule
		return
	}

	// Compute weights as integer multiples
	for i, tp := range tps {
		if tp == nil || tp.IsClosed() {
			weights[i] = 0
			continue
		}
		lat := tp.GetLatency()
		if lat <= 0 {
			// Unknown latency: assign median weight
			weights[i] = 1
		} else {
			// Weight = max_latency / this_latency, minimum 1
			w := int(maxLatency / lat)
			if w < 1 {
				w = 1
			}
			weights[i] = w
		}
	}

	// Build selection schedule: transport i appears weights[i] times
	totalWeight := 0
	for _, w := range weights {
		totalWeight += w
	}

	if totalWeight == 0 {
		ts.schedule = []int{0}
		return
	}

	schedule := make([]int, 0, totalWeight)
	for i, w := range weights {
		for j := 0; j < w; j++ {
			schedule = append(schedule, i)
		}
	}

	ts.schedule = schedule
}

// Select returns the next transport index based on the weighted schedule.
// Thread-safe via atomic counter.
func (ts *transportSelector) Select() int {
	ts.mu.RLock()
	sched := ts.schedule
	ts.mu.RUnlock()

	if len(sched) == 0 {
		return 0
	}

	idx := atomic.AddUint32(&ts.counter, 1) - 1
	return sched[idx%uint32(len(sched))] //nolint:gosec
}

// Len returns the schedule length (total weight).
func (ts *transportSelector) Len() int {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return len(ts.schedule)
}

// SelectForSize returns the leg index for a packet of the given
// payload size. Only meaningful for WeightModeSizeThreshold —
// other modes ignore the size argument and fall back to Select().
//
// Decision: payloads strictly greater than sizeThreshold go to
// leg 0 (the wide pipe — first leg the route-finder returned,
// ranked by latency). Payloads at or below the threshold
// round-robin across the remaining legs via smallLegSchedule.
// Single-leg routes always return 0.
func (ts *transportSelector) SelectForSize(size int) int {
	ts.mu.RLock()
	mode := ts.mode
	threshold := ts.sizeThreshold
	smallSched := ts.smallLegSchedule
	ts.mu.RUnlock()

	if mode != WeightModeSizeThreshold {
		return ts.Select()
	}
	if size > threshold {
		return 0
	}
	if len(smallSched) == 0 {
		return 0
	}
	idx := atomic.AddUint32(&ts.smallLegCounter, 1) - 1
	return smallSched[idx%uint32(len(smallSched))] //nolint:gosec
}

// SelectForPayload returns the leg index for a packet whose
// payload is `p`. Used by modes that inspect packet contents:
//   - WeightModeSticky5Tuple: hash the IPv4 5-tuple, mod live
//     leg count. Same flow always picks the same leg.
//   - WeightModeDSCPPriority: read DSCP byte; >= threshold → leg
//     0, else round-robin across the rest.
//   - WeightModeLatencyAdaptive: ignore p, scan live legs for
//     lowest GetLatency.
//
// Other modes ignore p and fall back to SelectForSize(len(p)) or
// Select(). Callers that don't have payload bytes (handshake,
// retx) should pass nil — the selector treats nil as "no
// information" and reverts to the schedule-based pick.
func (ts *transportSelector) SelectForPayload(p []byte) int {
	ts.mu.RLock()
	mode := ts.mode
	live := ts.liveLegs
	liveTps := ts.liveTps
	dscpThreshold := ts.dscpThreshold
	smallSched := ts.smallLegSchedule
	ts.mu.RUnlock()

	switch mode {
	case WeightModeSticky5Tuple:
		if len(live) == 0 {
			return 0
		}
		h := fiveTupleHash(p)
		return live[h%uint32(len(live))] //nolint:gosec
	case WeightModeLatencyAdaptive:
		return pickLowestLatency(live, liveTps)
	case WeightModeECF:
		return ts.SelectECF(len(p))
	case WeightModeDSCPPriority:
		if isIPv4DSCPGE(p, dscpThreshold) {
			return 0
		}
		if len(smallSched) == 0 {
			return 0
		}
		idx := atomic.AddUint32(&ts.smallLegCounter, 1) - 1
		return smallSched[idx%uint32(len(smallSched))] //nolint:gosec
	case WeightModeSizeThreshold:
		return ts.SelectForSize(len(p))
	}
	return ts.Select()
}

// fiveTupleHash returns an FNV-1a 32-bit hash over the IPv4
// 5-tuple bytes when p is a valid IPv4 packet (version nibble
// == 4, length >= 24). Falls back to hashing the first 24 bytes
// of the payload when it isn't IPv4 — still deterministic, just
// without flow semantics. Empty payloads hash to 0.
func fiveTupleHash(p []byte) uint32 {
	const fnvOffset = 2166136261
	const fnvPrime = 16777619
	h := uint32(fnvOffset)
	if len(p) >= 24 && p[0]>>4 == 4 {
		// IPv4: src IP (12-15), dst IP (16-19), src port (20-21),
		// dst port (22-23), protocol (9). Hash that exact slice.
		// Order is fixed so packets in either direction of the
		// same flow hash equally (we hash src-then-dst; same
		// flow's reverse direction has src/dst swapped and hashes
		// differently — operator should accept that as "leg per
		// half-flow," matching the typical TCP behavior).
		for _, b := range p[12:24] {
			h ^= uint32(b)
			h *= fnvPrime
		}
		h ^= uint32(p[9])
		h *= fnvPrime
		return h
	}
	// Fallback: hash the first 24 bytes (or fewer).
	n := len(p)
	if n > 24 {
		n = 24
	}
	for i := 0; i < n; i++ {
		h ^= uint32(p[i])
		h *= fnvPrime
	}
	return h
}

// isIPv4DSCPGE reports whether p is an IPv4 packet whose DSCP
// (upper 6 bits of the ToS byte at offset 1) is >= threshold.
// Non-IPv4 or short payloads return false (caller treats as
// "below threshold").
func isIPv4DSCPGE(p []byte, threshold int) bool {
	if len(p) < 2 || p[0]>>4 != 4 {
		return false
	}
	dscp := int(p[1] >> 2)
	return dscp >= threshold
}

// SelectECF returns the leg index for the next DATA frame of the given size
// under the ECF predictive hold-back scheduler (WeightModeECF). It drains each
// leg's in-flight estimate for the time elapsed since the previous pick, runs
// the ecfPick decision, and charges the chosen leg with this frame's size.
// Takes the write lock because it mutates per-leg inflight state; per-packet
// picks for a given route group are already serialized by the caller's rg.mu,
// so the only contention is against the periodic SetECFState refresh.
//
// Falls back to the schedule-based pick (mirrored live legs) when there is no
// ECF state yet (bootstrap) or no leg is ready.
func (ts *transportSelector) SelectECF(size int) int {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if len(ts.ecfLegs) == 0 {
		return ts.scheduleIndexLocked()
	}

	// Drain inflight by rate*elapsed since the last pick. Because the leg
	// transports are reliable, a frame put on leg i is delivered ~rttᵢ later;
	// draining at the leg's send rate approximates that clearance without a
	// per-frame ack signal (skywire has no per-leg ack attribution).
	now := time.Now().UnixNano()
	if ts.ecfLastNano != 0 {
		dt := float64(now-ts.ecfLastNano) / float64(time.Second)
		if dt > 0 {
			for i := range ts.ecfLegs {
				ts.ecfLegs[i].inflightBytes -= ts.ecfLegs[i].rateBps * dt
				if ts.ecfLegs[i].inflightBytes < 0 {
					ts.ecfLegs[i].inflightBytes = 0
				}
			}
		}
	}
	ts.ecfLastNano = now

	idx := ecfPick(ts.ecfLegs, ts.ecfWaiting, &ts.ecfWaiting)
	if idx < 0 {
		return ts.scheduleIndexLocked()
	}
	if size <= 0 {
		size = ecfDefaultFrameBytes
	}
	ts.ecfLegs[idx].inflightBytes += float64(size)
	return idx
}

// scheduleIndexLocked returns the next schedule-based leg index. Caller holds
// ts.mu (the atomic counter is still used so it stays consistent with Select()).
func (ts *transportSelector) scheduleIndexLocked() int {
	if len(ts.schedule) == 0 {
		return 0
	}
	idx := atomic.AddUint32(&ts.counter, 1) - 1
	return ts.schedule[idx%uint32(len(ts.schedule))] //nolint:gosec
}

// ecfPick is the pure ECF (Earliest Completion First) decision, in FILTER form.
// Given per-leg state and the latched `waiting` hysteresis flag, it returns the
// leg index to send the next frame on, or -1 when no leg is ready (caller falls
// back to the schedule). waitOut, if non-nil, receives the updated `waiting`
// latch.
//
// Adaptation of ECF to skywire's no-cwnd model (every substitution called out):
//   - "xf has send capacity now"  →  inflightBytes < cwndBytes (ecfSaturated).
//   - CWND_f (bytes/RTT)          →  cwndBytes, the leg's bandwidth-delay
//     product rate*RTT. Unknown capacity (cold leg) is treated as unlimited so
//     the leg is used and thus measured, never assumed full.
//   - k (backlog not yet drained) →  inflightBytes on the fast leg xf.
//   - sigma_f / sigma_s (RTT σ)   →  per-leg jitterMs (EWMA of |sample-mean|).
//
// The governing rule is the paper's: if the fast leg can drain its whole
// backlog before the slow leg delivers even one frame, do not use the slow leg.
// This is the FILTER variant — where full ECF would return NO-LEG and idle
// briefly waiting for xf, this instead returns xf (send on the fast leg now).
// The frame then queues on xf's reliable transport rather than being held by
// the scheduler, which keeps the send path non-blocking. The full wait/idle
// variant is a follow-up (it needs the send path to handle a "hold this frame"
// return without dropping or busy-spinning).
func ecfPick(legs []ecfLegState, waiting bool, waitOut *bool) int {
	setWait := func(v bool) {
		if waitOut != nil {
			*waitOut = v
		}
	}

	// xf = ready leg with the smallest (positive) RTT. Ties and unknown-RTT
	// legs fall back to lowest index (leg 0 is the primary/fastest leg).
	xf := -1
	for i := range legs {
		if !legs[i].ready {
			continue
		}
		if xf < 0 || ecfBetterRTT(legs[i], legs[xf]) {
			xf = i
		}
	}
	if xf < 0 {
		return -1
	}

	// Fast leg still has send capacity (or unknown capacity → cold start):
	// send on it. This is ECF's first branch and the common case — as long as
	// the fastest leg is not saturated, nothing spills to a slower leg.
	if !ecfSaturated(legs[xf]) {
		setWait(false)
		return xf
	}

	// Fast leg saturated: find the next-best ready leg that still has capacity
	// (xs). If none can take more, stay on xf (its transport queues the frame).
	xs := -1
	for i := range legs {
		if i == xf || !legs[i].ready || ecfSaturated(legs[i]) {
			continue
		}
		if xs < 0 || ecfBetterRTT(legs[i], legs[xs]) {
			xs = i
		}
	}
	if xs < 0 {
		setWait(false)
		return xf
	}

	// ECF hold-back predicate. n = how many xf-RTTs to drain the fast leg's
	// backlog; if xf clears that backlog before xs delivers even one frame
	// (its RTT plus the jitter margin d), hold on xf instead of spilling.
	rttF, rttS := legs[xf].rttMs, legs[xs].rttMs
	// n = how many fast-leg RTTs to drain its current backlog. The drain
	// denominator is the fast leg's cwnd; for a cold leg (cwnd unknown) fall
	// back to the same bounded probe budget ecfSaturated uses, so the backlog
	// registers in the hold-back decision instead of n being pinned at 1 (which
	// would hold every cold-start frame on the single fastest leg).
	drain := legs[xf].cwndBytes
	if drain <= 0 {
		drain = ecfColdBootstrapBytes
	}
	n := 1.0
	if drain > 0 {
		n = 1 + legs[xf].inflightBytes/drain
	}
	d := legs[xf].jitterMs
	if legs[xs].jitterMs > d {
		d = legs[xs].jitterMs
	}
	hyst := 1.0
	if waiting {
		hyst = 1 + ecfBeta
	}
	if n*rttF < hyst*(rttS+d) {
		// Fast leg wins the race: hold the frame on xf (filter variant — the
		// paper would idle here; latch waiting for hysteresis on the next pick).
		setWait(true)
		return xf
	}
	setWait(false)
	return xs
}

// ecfSaturated reports whether a leg is carrying a full bandwidth-delay product
// of un-delivered bytes (no send capacity right now). A leg whose capacity is
// unknown (no rate/RTT sample yet) is never saturated, so a cold leg is used
// and measured instead of being assumed full.
func ecfSaturated(l ecfLegState) bool {
	// Congestion shed: a leg whose live RTT has ballooned well past its own
	// uncongested baseline is queue-building — treat it as full so ECF spills
	// to a healthier leg. This must come before the cwnd check: cwnd grows
	// with RTT, so without this a congesting leg's rising RTT would raise its
	// apparent capacity and ECF would feed it more, stalling the reorder
	// frontier (the observed HoL collapse).
	if l.rttMinMs > 0 && l.rttMs > ecfCongestRttFactor*l.rttMinMs {
		return true
	}
	if l.cwndBytes <= 0 {
		// Unmeasured leg: allow only a bounded probe budget so cold start fans
		// out across the ready legs instead of dumping the whole stream on the
		// single lowest-RTT leg until the first rate refresh.
		return l.inflightBytes >= ecfColdBootstrapBytes
	}
	return l.inflightBytes >= l.cwndBytes
}

// ecfBetterRTT reports whether leg a is a better (lower-RTT) fast-leg candidate
// than leg b. A leg with unknown RTT (0) is the worst candidate — it only wins
// when the incumbent is also unknown, which the caller's index order breaks.
func ecfBetterRTT(a, b ecfLegState) bool {
	if a.rttMs <= 0 {
		return false
	}
	if b.rttMs <= 0 {
		return true
	}
	return a.rttMs < b.rttMs
}

// pickLowestLatency returns the live-leg index with the smallest
// GetLatency() value. Legs reporting 0 (unknown) are deprioritized
// — they only win when no leg has a measurement. Linear scan;
// fine for the small mux counts skywire actually uses (typically
// 2-4).
func pickLowestLatency(live []int, liveTps []*transport.ManagedTransport) int {
	if len(live) == 0 {
		return 0
	}
	bestIdx := live[0]
	bestLat := -1.0
	for i, tp := range liveTps {
		if tp == nil {
			continue
		}
		lat := tp.GetLatency()
		if lat <= 0 {
			continue
		}
		if bestLat < 0 || lat < bestLat {
			bestLat = lat
			bestIdx = live[i]
		}
	}
	return bestIdx
}
