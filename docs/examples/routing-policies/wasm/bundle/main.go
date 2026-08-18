// bundle.wasm — the COMBINED routing-policy bundle (#3942).
//
// A single TinyGo module that carries EVERY compiled preset and
// dispatches by name at call time, instead of shipping one
// multi-hundred-KB module per preset (each of which re-embeds the whole
// TinyGo runtime). The host stamps the active preset name into the
// decide/tick input wire (ctx.preset); this guest switches on it. The
// runtime is paid once; each extra preset adds only its own few KB of
// logic.
//
// This is the module embedded at pkg/router/policy/wasm/presets/
// bundle.wasm and selected by config as "preset:<name>" (see
// pkg/visor/policy_loader.go). The per-preset standalone examples next
// to this dir (app-mux/, rotating-bw/) stay as pedagogical single-preset
// modules; this one is their union.
//
// Build with TinyGo:
//
//	cd docs/examples/routing-policies/wasm/bundle
//	tinygo build -target=wasi -no-debug -opt=2 -o bundle.wasm .
//
// then copy it over pkg/router/policy/wasm/presets/bundle.wasm.
package main

import (
	"encoding/json"
	"sort"
	"unsafe"
)

// Wire types — kept in sync with pkg/router/policy/wasm/abi.go.

type candidateWire struct {
	Hops           []string `json:"hops"`
	HopsGeo        []string `json:"hops_geo"`
	EstLatencyMs   int      `json:"est_latency_ms"`
	TransportKinds []string `json:"transport_kinds"`
}

type routingContextWire struct {
	App               string            `json:"app"`
	PeerPK            string            `json:"peer_pk"`
	Port              uint16            `json:"port"`
	NowUnixNano       int64             `json:"now_unix_nano"`
	CLIOverrides      map[string]string `json:"cli_overrides"`
	IsDirectDial      bool              `json:"is_direct_dial"`
	TransportKind     string            `json:"transport_kind"`
	ReverseCandidates []candidateWire   `json:"reverse_candidates"`
}

// decideInputWire carries the Preset name the host stamped so this
// bundle can dispatch. A single-preset module would omit Preset; here
// it selects which preset's logic runs.
type decideInputWire struct {
	Ctx        routingContextWire `json:"ctx"`
	Candidates []candidateWire    `json:"candidates"`
	Preset     string             `json:"preset"`
}

type legInfoWire struct {
	Index       int      `json:"index"`
	Kind        string   `json:"kind"`
	TransportID string   `json:"transport_id"`
	LatencyMs   int      `json:"latency_ms"`
	Alive       bool     `json:"alive"`
	SentBytes   uint64   `json:"sent_bytes,omitempty"`
	RecvBytes   uint64   `json:"recv_bytes,omitempty"`
	Retransmits uint64   `json:"retransmits,omitempty"`
	Hops        []string `json:"hops,omitempty"`
}

type tickInputWire struct {
	Ctx    routingContextWire `json:"ctx"`
	Legs   []legInfoWire      `json:"legs"`
	Preset string             `json:"preset"`
}

type routeSpecWire struct {
	Chosen                  *candidateWire `json:"chosen,omitempty"`
	ReverseChosen           *candidateWire `json:"reverse_chosen,omitempty"`
	Mux                     int            `json:"mux,omitempty"`
	ForwardMux              int            `json:"forward_mux,omitempty"`
	ReverseMux              int            `json:"reverse_mux,omitempty"`
	MinHops                 int            `json:"min_hops,omitempty"`
	ForwardMinHops          int            `json:"forward_min_hops,omitempty"`
	ReverseMinHops          int            `json:"reverse_min_hops,omitempty"`
	Fallback                string         `json:"fallback,omitempty"`
	Distribution            string         `json:"distribution,omitempty"`
	RotationIntervalSeconds int            `json:"rotation_interval_seconds,omitempty"`
}

type rotationActionWire struct {
	DropLegs    []int    `json:"drop_legs,omitempty"`
	AddLeg      bool     `json:"add_leg,omitempty"`
	ExcludeHops []string `json:"exclude_hops,omitempty"`
}

// Required: host-driven memory management.

//export alloc
func alloc(size uint32) uint32 {
	buf := make([]byte, size)
	return uint32(uintptr(unsafe.Pointer(&buf[0]))) //nolint:gosec
}

//export free
func free(_, _ uint32) {}

// readInput reads `length` bytes from linear memory at `ptr`.
func readInput(ptr, length uint32) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), int(length))
}

// writeOutput allocates a buffer, copies data in, and returns the
// packed (ptr | len<<32) pair the host can decode.
func writeOutput(data []byte) uint64 {
	if len(data) == 0 {
		return 0
	}
	ptr := alloc(uint32(len(data)))
	dst := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), len(data))
	copy(dst, data)
	return uint64(ptr) | (uint64(len(data)) << 32)
}

//export decide_route
func decideRoute(inPtr, inLen uint32) uint64 {
	var input decideInputWire
	if err := json.Unmarshal(readInput(inPtr, inLen), &input); err != nil {
		return 0
	}
	var spec routeSpecWire
	switch input.Preset {
	case "rotating-bw":
		spec = decideRotatingBW(input.Ctx)
	case "latency-adaptive":
		spec = decideLatencyAdaptive(input.Ctx)
	case "elastic-mux":
		spec = decideElasticMux(input.Ctx)
	case "probe-and-prune":
		spec = decideProbeAndPrune(input.Ctx)
	case "adaptive":
		spec = decideAdaptive(input.Ctx)
	case "app-mux":
		spec = decideAppMux(input.Ctx)
	default:
		// Empty / unknown preset: fall back to the per-app app-mux
		// behavior so a bare bundle still does something sensible.
		spec = decideAppMux(input.Ctx)
	}
	out, err := json.Marshal(spec)
	if err != nil {
		return 0
	}
	return writeOutput(out)
}

// decideAppMux is the verbatim app-mux preset logic: per-app static mux
// + min_hops; latency-sensitive apps stay single-route, bandwidth apps
// get parallel legs.
func decideAppMux(ctx routingContextWire) routeSpecWire {
	switch ctx.App {
	case "vpn-client":
		return routeSpecWire{Mux: 4, MinHops: 2}
	case "skychat":
		// Chat is latency-sensitive — single route, lowest mux.
		return routeSpecWire{Mux: 1}
	default:
		// Everything else: visor defaults (empty spec).
		return routeSpecWire{}
	}
}

// decideRotatingBW is the verbatim rotating-bw preset logic: mux=4 over
// multi-hop with a leg rotated every 90s for bandwidth-heavy apps.
func decideRotatingBW(ctx routingContextWire) routeSpecWire {
	switch ctx.App {
	case "vpn-client", "skysocks-client", "skynet-client":
		// min_hops=2 already says "no direct transport" — direct is 0
		// intermediates, so requiring at least 1 intermediate rules it
		// out. The visor's policy layer treats min_hops>=2 as an
		// implicit avoid_direct signal on the direct-dial bridge, so
		// the dial flows to the overlay path where rotation can act on
		// the resulting route group.
		return routeSpecWire{
			Mux:                     4,
			MinHops:                 2,
			RotationIntervalSeconds: 90,
			Distribution:            "weighted: 1, 1, 1, 1",
		}
	}
	return routeSpecWire{}
}

// decideLatencyAdaptive is the latency-adaptive preset's decide logic: an
// ASYMMETRIC spec for bandwidth/proxy apps — a single lean, direct-ok
// upstream leg (uploads are small) paired with a 4-way multi-hop
// downstream fan-out (bulk downloads). RotationIntervalSeconds=30 keeps
// on_tick firing so the downstream set can be re-evaluated and the
// slowest leg evicted; Distribution "auto" lets the host weight bytes
// toward the faster legs. Non-target apps get the empty spec (defaults).
func decideLatencyAdaptive(ctx routingContextWire) routeSpecWire {
	switch ctx.App {
	case "vpn-client", "skysocks-client", "skynet-client":
		// Symmetric mux=4: the on_tick evict-slowest logic acts on the
		// route group's forward legs (rg.tps) — the only legs the tick
		// hook sees (reverse-only legs live in rg.rvs and are invisible
		// to on_tick). An asymmetric 1-up/4-down shape would put the 4
		// adaptive legs on the reverse side where on_tick can't manage
		// them, making the eviction a no-op. Symmetric mux gives on_tick
		// 4 real legs to converge over. (The lean-upstream refinement
		// awaits a router change exposing reverse legs to the tick hook.)
		return routeSpecWire{
			Mux:                     4,
			MinHops:                 2,
			RotationIntervalSeconds: 30,
			Distribution:            "auto",
		}
	}
	return routeSpecWire{}
}

// decideElasticMux is the elastic-mux preset's decide logic. It starts
// deliberately MODEST — a 2-way mux over multi-hop — and lets the on_tick
// AIMD controller grow or shrink the leg count to match observed load.
// Unlike the static-mux presets (which pin a fixed width), the initial
// Mux here is only a seed: RotationIntervalSeconds=20 makes on_tick fire
// often enough to react to load swings, and Distribution "auto" lets the
// host weight bytes toward the faster legs while the count floats.
// Non-target apps get the empty spec (defaults).
func decideElasticMux(ctx routingContextWire) routeSpecWire {
	switch ctx.App {
	case "vpn-client", "skysocks-client", "skynet-client":
		return routeSpecWire{
			Mux:                     2,
			MinHops:                 2,
			RotationIntervalSeconds: 20,
			Distribution:            "auto",
		}
	}
	return routeSpecWire{}
}

// decideProbeAndPrune is the probe-and-prune preset's decide logic. It
// holds a steady 3-way mux over multi-hop as the "established" set that
// on_tick continuously refines: every 30s the tick controller adds one
// speculative leg over a fresh path, watches it for a few ticks, and
// keeps it only if it beats the current worst leg (else discards it), so
// the established set drifts toward lower latency without ever growing
// past its target. Distribution "auto" weights bytes toward the faster
// legs meanwhile. Non-target apps get the empty spec (defaults).
func decideProbeAndPrune(ctx routingContextWire) routeSpecWire {
	switch ctx.App {
	case "vpn-client", "skysocks-client", "skynet-client":
		return routeSpecWire{
			Mux:                     3,
			MinHops:                 2,
			RotationIntervalSeconds: 30,
			Distribution:            "auto",
		}
	}
	return routeSpecWire{}
}

// decideAdaptive is the COMPOSITE "adaptive" preset's decide logic — the
// intended converged default. It returns a sensible middle spec that the
// tickAdaptive controller then steers along all three performance
// dimensions at once: a 3-way disjoint multi-hop mux (min_hops=2), latency-
// weighted byte distribution ("auto"), re-evaluated every 20s so on_tick
// fires often enough to react to load swings, latency drift, and probe
// results. The Mux of 3 is only the STARTING width — tickAdaptive's AIMD
// dimension grows it toward the cap under load and shrinks it back toward
// the floor when idle. Non-target apps get the empty spec (defaults).
func decideAdaptive(ctx routingContextWire) routeSpecWire {
	switch ctx.App {
	case "vpn-client", "skysocks-client", "skynet-client":
		return routeSpecWire{
			Mux:                     adaptDecideMux,
			MinHops:                 2,
			RotationIntervalSeconds: 20,
			Distribution:            "auto",
		}
	}
	return routeSpecWire{}
}

// targetMux is the mux size the rotating-bw policy aims to maintain.
// Kept in sync with the Mux value returned from decideRotatingBW so
// on_tick can reason about "are we at target?"
const targetMux = 4

//export on_tick
func onTick(inPtr, inLen uint32) uint64 {
	var input tickInputWire
	if err := json.Unmarshal(readInput(inPtr, inLen), &input); err != nil {
		return 0
	}
	// rotating-bw, latency-adaptive, elastic-mux, probe-and-prune and the
	// composite adaptive preset have tick logic; app-mux and unknown
	// presets are static, so they take no rotation action.
	switch input.Preset {
	case "rotating-bw":
		return tickRotatingBW(input)
	case "latency-adaptive":
		return tickLatencyAdaptive(input)
	case "elastic-mux":
		return tickElasticMux(input)
	case "probe-and-prune":
		return tickProbeAndPrune(input)
	case "adaptive":
		return tickAdaptive(input)
	default:
		return 0
	}
}

// tickRotatingBW is the verbatim rotating-bw rotation logic.
//
//	alive_count >= target_mux  → drop oldest + add  (steady-state
//	                             rotation: one in, one out, leg set
//	                             drifts across the eligible-peer set)
//	alive_count <  target_mux  → add only           (recover toward
//	                             target without shrinking further)
//	alive_count == 0           → no-op              (defensive; nothing
//	                             to rotate yet)
//
// Gating drop on alive_count >= target_mux means failed adds just delay
// rotation; the group never shrinks below the target.
func tickRotatingBW(input tickInputWire) uint64 {
	aliveCount := 0
	oldestAliveIdx := -1
	for _, l := range input.Legs {
		if l.Alive {
			aliveCount++
			if oldestAliveIdx == -1 || l.Index < oldestAliveIdx {
				oldestAliveIdx = l.Index
			}
		}
	}
	if aliveCount == 0 {
		return 0
	}
	var action rotationActionWire
	if aliveCount >= targetMux {
		action = rotationActionWire{
			DropLegs: []int{oldestAliveIdx},
			AddLeg:   true,
		}
	} else {
		action = rotationActionWire{
			AddLeg: true,
		}
	}
	out, err := json.Marshal(action)
	if err != nil {
		return 0
	}
	return writeOutput(out)
}

// latAdaptEWMA holds the exponentially-weighted moving average of each
// leg's latency_ms, keyed by the leg's STABLE transport_id (not its
// index, which shifts as the tps[] slice compacts on drop/add). This is
// the per-leg state that lets tickLatencyAdaptive act on a smoothed
// signal rather than the raw instantaneous latency — on the live mesh a
// single leg's latency_ms jumps around wildly tick-to-tick (e.g.
// 75→245→13000→180), and evicting on the raw value chases that noise.
// Keys are pruned each tick to the current alive-or-present leg set so a
// dropped transport's entry doesn't linger.
var latAdaptEWMA = map[string]float64{}

// latAdaptSeen is a scratch set reused each tick to record which
// transport_ids appear in the current leg snapshot, so stale keys can be
// pruned from latAdaptEWMA. Package-global (rather than tick-local) only
// to avoid a per-tick allocation; it is cleared at the top of every tick.
var latAdaptSeen = map[string]bool{}

// latAdaptAlpha is the EWMA smoothing factor. 0.3 weights the newest
// sample at 30% and the running average at 70% — enough to track a
// genuine sustained latency shift within a few ticks while damping a
// lone transient spike to a fraction of its raw magnitude.
const latAdaptAlpha = 0.3

// tickLatencyAdaptive is the latency-adaptive rotation logic. It differs
// from rotating-bw in what it evicts and WHEN:
//
//	rotating-bw drops the OLDEST alive leg every single tick — an
//	unconditional churn that keeps the set drifting for
//	traffic-analysis resistance.
//
//	latency-adaptive instead drops the SLOWEST alive leg, and ONLY when
//	that leg is a clear outlier — its SMOOTHED latency is at least 1.5x
//	the median of the alive legs' smoothed latencies. This hysteresis
//	band means: once the set has converged to a cluster of comparably-
//	fast legs (worst < 1.5x median), NO leg qualifies and the policy
//	holds — no churn. It converges toward a low-latency disjoint set and
//	then stops thrashing, the opposite of rotating-bw's every-tick
//	rotation.
//
// The eviction decision runs on an EWMA of each leg's latency, keyed by
// the leg's stable transport_id, rather than on the raw per-tick
// latency_ms. Raw latency on the live mesh is noisy — one leg can read
// 75ms one tick and 13000ms the next with no real change in its quality —
// and evicting on the instantaneous value chases that noise, tearing down
// a perfectly good leg on a transient spike. Smoothing filters those
// spikes so eviction fires only on a PERSISTENT latency difference. The
// stable transport_id key is what makes a per-leg average meaningful at
// all: the leg's index shifts when the slice compacts, so index-keyed
// state would smear one leg's history onto another after any drop.
//
//	alive_count == 0            → no-op (nothing measured yet)
//	alive_count <  target_mux   → add only (recover toward target; never
//	                              shrink below it)
//	alive_count >= target_mux &&
//	  smoothed_worst > 0 &&
//	  smoothed_worst >=
//	    1.5 * smoothed_median   → drop slowest + add + exclude its hops
//	                              (evict the outlier; exclude its
//	                              intermediates so the replacement differs)
//	otherwise (converged)       → no-op (hold; do not churn)
func tickLatencyAdaptive(input tickInputWire) uint64 {
	const targetMux = 4

	// Update the per-leg EWMA and record which transport_ids are present
	// this tick so stale keys can be pruned afterward.
	for k := range latAdaptSeen {
		delete(latAdaptSeen, k)
	}
	for _, l := range input.Legs {
		tid := l.TransportID
		if tid == "" {
			continue
		}
		latAdaptSeen[tid] = true
		// Only fold in a real measurement; latency_ms==0 means "unknown"
		// and must not drag the average toward zero.
		if l.Alive && l.LatencyMs > 0 {
			sample := float64(l.LatencyMs)
			if prev, ok := latAdaptEWMA[tid]; ok {
				latAdaptEWMA[tid] = latAdaptAlpha*sample + (1-latAdaptAlpha)*prev
			} else {
				// Seed with the first sample so the average starts on the
				// leg's own latency rather than climbing from zero.
				latAdaptEWMA[tid] = sample
			}
		}
	}
	// Prune EWMA keys for transport_ids no longer in the leg set so a
	// dropped leg's history can't resurface if its index is later reused.
	for tid := range latAdaptEWMA {
		if !latAdaptSeen[tid] {
			delete(latAdaptEWMA, tid)
		}
	}

	// Evict-slowest-outlier logic, run on the SMOOTHED latencies. Only
	// alive legs with a known smoothed latency participate.
	aliveCount := 0
	worstIdx := -1
	worstSmoothed := -1.0
	var smoothed []float64
	var worstHops []string
	for _, l := range input.Legs {
		if !l.Alive {
			continue
		}
		aliveCount++
		sm, ok := latAdaptEWMA[l.TransportID]
		if !ok {
			// No smoothed value yet (leg never reported a latency) — it
			// can't be judged an outlier this tick.
			continue
		}
		smoothed = append(smoothed, sm)
		if sm > worstSmoothed {
			worstSmoothed = sm
			worstIdx = l.Index
			worstHops = l.Hops
		}
	}
	if aliveCount == 0 {
		return 0
	}
	if aliveCount < targetMux {
		return writeAction(rotationActionWire{AddLeg: true})
	}
	// Need at least a couple of smoothed samples to compute a meaningful
	// median; otherwise hold.
	if len(smoothed) < 2 || worstIdx < 0 || worstSmoothed <= 0 {
		return 0
	}

	// Median of the alive legs' smoothed latencies.
	sort.Float64s(smoothed)
	median := medianSorted(smoothed)

	// Hysteresis: only evict a clear outlier (smoothed worst >= 1.5x
	// smoothed median). Once the set has converged this is false for every
	// leg, so we hold and stop churning.
	if median > 0 && worstSmoothed >= 1.5*median {
		return writeAction(rotationActionWire{
			DropLegs:    []int{worstIdx},
			AddLeg:      true,
			ExcludeHops: worstHops,
		})
	}
	// Converged: all alive legs comparably fast — hold.
	return 0
}

// --- elastic-mux: AIMD scaling of the mux leg count to load ---
//
// elastic-mux treats the mux width as a control variable, not a constant.
// Its on_tick runs a classic AIMD (additive-increase / multiplicative-…
// here just single-step-decrease) controller over the group's aggregate
// received throughput:
//
//	SATURATED (throughput near the observed peak) → ADD one leg. Under
//	real load more disjoint paths raise the aggregate the group can pull,
//	so we grow — additively, one leg per tick — up to a hard cap.
//	IDLE (throughput far below peak, for two consecutive ticks) → DROP one
//	leg. The width bought under load is now wasted transport state and
//	fleet bandwidth, so we release it — one leg at a time, down to a floor.
//	Otherwise → hold.
//
// The controller drives its decisions off an EWMA of the per-tick
// received-byte DELTA (recv_bytes is a monotonic counter; the increment
// since last tick is this interval's throughput), NOT the raw delta.
// Raw per-tick byte deltas on the live mesh are extremely spiky — a bulk
// download arrives in bursts, so one tick reads near-zero and the next
// reads a full window — and an AIMD loop driven by the raw value would
// oscillate (grow, shrink, grow) chasing that jitter. Smoothing the
// delta with an EWMA (α=0.3) gives a stable load signal the controller
// can compare against a slowly-decaying running peak without thrashing.

// elasticPrevRecv holds each leg's last-seen recv_bytes counter, keyed by
// the STABLE transport_id (index shifts as legs drop/add), so the next
// tick can compute that leg's byte delta. Stale keys are pruned each tick.
var elasticPrevRecv = map[string]uint64{}

// elasticSeen is a scratch set (reused, cleared each tick) recording which
// transport_ids appear this tick, so stale elasticPrevRecv keys can be
// pruned. Package-global only to avoid a per-tick allocation.
var elasticSeen = map[string]bool{}

// elasticThroughputEWMA is the smoothed aggregate received throughput
// (sum of alive legs' per-tick recv_bytes deltas), the load signal the
// AIMD controller acts on. elasticPeak is the running high-water mark of
// that smoothed signal, decayed a little each tick so a transient burst
// long past stops holding the bar artificially high. elasticIdleCount is
// the consecutive-idle tick counter (so a single quiet tick can't shrink
// the group). elasticSeeded guards the one-time seed on the first tick
// that observes non-zero throughput.
var (
	elasticThroughputEWMA float64
	elasticPeak           float64
	elasticIdleCount      int
	elasticSeeded         bool
)

const (
	// elasticFloor / elasticCap bound the leg count AIMD may reach.
	elasticFloor = 2
	elasticCap   = 6
	// elasticAlpha is the EWMA smoothing factor for the throughput signal
	// (newest delta weighted 30%, running average 70%).
	elasticAlpha = 0.3
	// elasticPeakDecay bleeds the running peak down ~2%/tick so the
	// saturation bar tracks a sustained drop in achievable throughput
	// instead of being pinned forever by one historical burst.
	elasticPeakDecay = 0.98
)

// tickElasticMux is the elastic-mux AIMD controller. See the block comment
// above for the load model and why the signal is a smoothed byte-delta.
//
//	alive_count == 0                         → no-op (nothing measured)
//	smoothed >= 0.80*peak && alive < cap     → add one leg (grow to load)
//	smoothed <  0.25*peak for >=2 ticks
//	  && alive > floor                       → drop oldest leg (release)
//	otherwise                                → hold
func tickElasticMux(input tickInputWire) uint64 {
	// Compute this interval's aggregate received byte-delta over alive
	// legs, refreshing each leg's prev-counter, and record presence for
	// stale-key pruning. Also find the oldest (lowest-index) alive leg to
	// release when shrinking, and count alive legs.
	for k := range elasticSeen {
		delete(elasticSeen, k)
	}
	var rawTotal float64
	aliveCount := 0
	oldestAliveIdx := -1
	for _, l := range input.Legs {
		if !l.Alive {
			continue
		}
		aliveCount++
		if oldestAliveIdx == -1 || l.Index < oldestAliveIdx {
			oldestAliveIdx = l.Index
		}
		tid := l.TransportID
		if tid == "" {
			continue
		}
		elasticSeen[tid] = true
		if prev, ok := elasticPrevRecv[tid]; ok && l.RecvBytes >= prev {
			// Only fold in a non-negative delta; a counter that went
			// backwards means a reset/new leg, not real throughput.
			rawTotal += float64(l.RecvBytes - prev)
		}
		elasticPrevRecv[tid] = l.RecvBytes
	}
	// Prune prev-counters for transport_ids no longer present.
	for tid := range elasticPrevRecv {
		if !elasticSeen[tid] {
			delete(elasticPrevRecv, tid)
		}
	}
	if aliveCount == 0 {
		return 0
	}

	// Fold the raw delta into the smoothed signal and maintain the peak.
	// Seed both on the first tick that sees real throughput so the signal
	// starts on a genuine value rather than ramping up from zero.
	if !elasticSeeded {
		if rawTotal > 0 {
			elasticThroughputEWMA = rawTotal
			elasticPeak = rawTotal
			elasticSeeded = true
		}
		// No load signal yet — hold and keep measuring.
		return 0
	}
	elasticThroughputEWMA = elasticAlpha*rawTotal + (1-elasticAlpha)*elasticThroughputEWMA
	elasticPeak *= elasticPeakDecay
	if elasticThroughputEWMA > elasticPeak {
		elasticPeak = elasticThroughputEWMA
	}
	if elasticPeak <= 0 {
		return 0
	}

	smoothed := elasticThroughputEWMA
	// Track consecutive idle ticks independently of whether we can act on
	// them, so the "two quiet ticks" requirement is about load, not width.
	if smoothed < 0.25*elasticPeak {
		elasticIdleCount++
	} else {
		elasticIdleCount = 0
	}

	// Additive increase: saturated and below the cap → grow one leg.
	if smoothed >= 0.80*elasticPeak && aliveCount < elasticCap {
		return writeAction(rotationActionWire{AddLeg: true})
	}
	// Decrease: sustained idle and above the floor → release one leg.
	// Reset the idle counter after acting so we step down gracefully
	// (one leg every two idle ticks) rather than collapsing to the floor.
	if elasticIdleCount >= 2 && aliveCount > elasticFloor {
		elasticIdleCount = 0
		return writeAction(rotationActionWire{DropLegs: []int{oldestAliveIdx}})
	}
	// Hold.
	return 0
}

// --- probe-and-prune: continuous explore/exploit over a fresh path ---
//
// probe-and-prune keeps a fixed-width "established" set (probeTarget legs)
// but never stops looking for a better path than the ones it holds. Its
// on_tick is a small state machine:
//
//	EXPLORE: when idle and exactly at target width, add ONE speculative
//	leg over a fresh path (the host picks the candidate; on_tick can only
//	request an add). The next tick identifies that new leg by diffing the
//	current transport_ids against the pre-add "known" set, and puts it on
//	probation (probeTID / probeAge).
//	OBSERVE: let the probe run probeObserveTicks ticks so its EWMA latency
//	(same stable-transport_id-keyed smoothing latency-adaptive uses) is a
//	real measurement, not first-packet noise.
//	EXPLOIT: compare the probe's smoothed latency to the WORST (highest-
//	EWMA) established leg. If the probe is better, it GRADUATES — drop the
//	worst established leg, and the probe takes its place. If not, the
//	experiment FAILED — drop the probe itself. Either way width returns to
//	target and the cycle repeats.
//
// Net effect: the established set ratchets toward lower latency one path
// at a time, spending at most one extra leg's worth of transport state
// while a probe is in flight, and never growing past target permanently.

// probeEWMA is the per-leg smoothed latency, keyed by stable transport_id
// (same rationale as latAdaptEWMA). probeSeen is the reused prune scratch
// set; probeAliveIdx maps transport_id → current leg index each tick so a
// DropLegs decision made about a transport can be translated to the index
// the host expects. prevKnownTIDs snapshots the established set just before
// an add so the following tick can diff out the newly-added probe leg.
var (
	probeEWMA     = map[string]float64{}
	probeSeen     = map[string]bool{}
	probeAliveIdx = map[string]int{}
	prevKnownTIDs = map[string]bool{}
)

// probeTID is the transport_id of the leg currently on probation ("" =
// none). probeAge counts ticks since the probe was adopted. probePending
// is set the tick we request the add, so the next tick knows to adopt the
// newly-appeared leg as the probe.
var (
	probeTID     string
	probeAge     int
	probePending bool
)

const (
	// probeTarget is the established-set width probe-and-prune maintains
	// (matches decideProbeAndPrune's Mux). A probe transiently makes it
	// target+1.
	probeTarget = 3
	// probeObserveTicks is how many ticks a probe runs before it is judged,
	// so its EWMA latency reflects steady behavior, not startup noise.
	probeObserveTicks = 3
	// probeAlpha is the latency EWMA smoothing factor (as in latency-…).
	probeAlpha = 0.3
)

// tickProbeAndPrune is the probe-and-prune explore/exploit controller. See
// the block comment above for the state machine.
func tickProbeAndPrune(input tickInputWire) uint64 {
	// Update per-leg latency EWMA, map transport_id → index, count alive,
	// and record presence for stale-key pruning.
	for k := range probeSeen {
		delete(probeSeen, k)
	}
	for k := range probeAliveIdx {
		delete(probeAliveIdx, k)
	}
	aliveCount := 0
	for _, l := range input.Legs {
		tid := l.TransportID
		if tid == "" {
			continue
		}
		probeSeen[tid] = true
		if !l.Alive {
			continue
		}
		aliveCount++
		probeAliveIdx[tid] = l.Index
		// Fold in only real measurements; latency_ms==0 means "unknown".
		if l.LatencyMs > 0 {
			sample := float64(l.LatencyMs)
			if prev, ok := probeEWMA[tid]; ok {
				probeEWMA[tid] = probeAlpha*sample + (1-probeAlpha)*prev
			} else {
				probeEWMA[tid] = sample
			}
		}
	}
	// Prune EWMA keys for transport_ids no longer present.
	for tid := range probeEWMA {
		if !probeSeen[tid] {
			delete(probeEWMA, tid)
		}
	}

	// Adopt: if we requested an add last tick, the new leg should now be
	// present — it is the alive transport_id absent from the pre-add known
	// set. Put it on probation.
	if probePending {
		probePending = false
		for tid := range probeAliveIdx {
			if !prevKnownTIDs[tid] {
				probeTID = tid
				probeAge = 0
				break
			}
		}
	}

	// Active probe: observe, then graduate-or-discard.
	if probeTID != "" {
		idx, alive := probeAliveIdx[probeTID]
		if !alive {
			// Probe died on its own — abandon the experiment.
			probeTID = ""
			probeAge = 0
			return 0
		}
		probeAge++
		if probeAge < probeObserveTicks {
			return 0
		}
		// Judge: probe's smoothed latency vs the worst established leg.
		probeSm, okProbe := probeEWMA[probeTID]
		worstIdx := -1
		worstSm := -1.0
		for tid, i := range probeAliveIdx {
			if tid == probeTID {
				continue
			}
			sm, ok := probeEWMA[tid]
			if !ok {
				continue
			}
			if sm > worstSm {
				worstSm = sm
				worstIdx = i
			}
		}
		if !okProbe || worstIdx < 0 {
			// Not enough latency signal to judge yet — keep observing.
			return 0
		}
		probeTID = ""
		if probeSm < worstSm {
			// Probe graduates: prune the worst established leg; the probe
			// stays and becomes part of the established set next tick.
			return writeAction(rotationActionWire{DropLegs: []int{worstIdx}})
		}
		// Failed experiment: prune the probe itself, keep the incumbents.
		return writeAction(rotationActionWire{DropLegs: []int{idx}})
	}

	// Explore: no probe in flight and exactly at target width → snapshot
	// the known set and request one speculative leg over a fresh path.
	if !probePending && aliveCount == probeTarget {
		for k := range prevKnownTIDs {
			delete(prevKnownTIDs, k)
		}
		for tid := range probeAliveIdx {
			prevKnownTIDs[tid] = true
		}
		probePending = true
		return writeAction(rotationActionWire{AddLeg: true})
	}
	return 0
}

// medianSorted returns the median of an already-sorted float slice (mean of
// the two middle elements for an even count). Shared by the latency-adaptive
// and adaptive controllers so the outlier test uses one definition.
func medianSorted(s []float64) float64 {
	n := len(s)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// --- adaptive: the COMPOSITE performance default (size+membership+explore) ---
//
// adaptive is the intended converged default. It runs the three standalone
// performance controllers — elastic-mux (SIZE: AIMD the leg count to load),
// latency-adaptive (MEMBERSHIP: evict the slowest leg toward a low-latency
// set), and probe-and-prune (EXPLORE: speculatively try a fresh path and keep
// it only if it is better) — under ONE arbitrated on_tick that performs AT
// MOST ONE structural action (an add OR a drop) per tick.
//
// Why one action per tick: each sub-controller can independently want to add
// or drop a leg on the same tick (load says "grow", latency says "evict the
// outlier", the explorer says "probe"). Letting them all act would churn the
// route group — multiple simultaneous adds/drops tear legs up and down faster
// than the host can build them and faster than the EWMA signals can settle,
// which is exactly the thrash the smoothing was added to avoid. Arbitrating
// to a single action per tick lets each dimension make steady, observable
// progress: the group takes one deliberate step, the next tick re-measures on
// the settled state, and the highest-priority need is served first.
//
// Arbitration priority each tick (first match fires, then return):
//
//	1. aliveCount == 0            → hold (nothing measured yet).
//	2. RECOVER  (aliveCount < floor)                → add one leg. Safety
//	   first: a group starved below the floor is refilled before anything
//	   else is considered.
//	3. MEMBERSHIP (latency): worst alive leg's EWMA latency is a >=1.5x
//	   outlier over the alive-leg median → drop it + add + exclude its hops.
//	   Correctness of the set (drop a genuinely bad path) outranks sizing.
//	4. GROW (load): smoothed throughput >= 0.80*peak and aliveCount < cap →
//	   add one leg. Under real saturation more disjoint paths raise the
//	   aggregate the group can pull.
//	5. SHRINK (idle): smoothed throughput < 0.25*peak for >=2 consecutive
//	   ticks and aliveCount > floor → drop the oldest leg (release wasted
//	   width). Reset the idle counter so we step down one leg at a time.
//	6. EXPLORE: otherwise, when the set is stable (no probe in flight,
//	   aliveCount == target), every exploreEvery ticks start a probe — add a
//	   speculative leg, observe its EWMA latency adaptObserve ticks, then keep
//	   it (drop the worst established leg) only if it beats that worst, else
//	   prune the probe. This is probe-and-prune's explore/exploit loop.
//	7. else → hold.
//
// Rules 3/4/5 are gated on "no probe in flight": while an experiment is mid-
// flight (the group is transiently target+1 wide) a membership/grow/shrink
// action would corrupt the probe's accounting and defeat the one-step-at-a-
// time discipline, so structural changes pause until the probe graduates or
// is pruned. The RECOVER rule is never gated — a starved group is refilled
// regardless. The size target starts at the decide Mux (3) and grow/shrink
// move it within [floor, cap] so EXPLORE always probes at the current settled
// width.
//
// All three dimensions share ONE set of per-transport_id state, refreshed at
// the top of EVERY tick before arbitration: a latency EWMA (adaptLatEWMA), a
// received-byte-delta throughput EWMA + decaying peak (adaptThroughputEWMA /
// adaptPeak), the consecutive-idle counter (adaptIdleCount), and the probe
// state machine (adaptProbeTID / adaptProbeAge / adaptProbePending /
// adaptPrevKnown). Keys are pruned to the present leg set each tick so a
// dropped transport's history can't linger or smear onto a reused index.

var (
	// adaptLatEWMA is the per-leg smoothed latency, keyed by stable
	// transport_id (same rationale as latAdaptEWMA/probeEWMA).
	adaptLatEWMA = map[string]float64{}
	// adaptPrevRecv is each leg's last-seen recv_bytes counter, keyed by
	// transport_id, so the next tick can compute its throughput delta.
	adaptPrevRecv = map[string]uint64{}
	// adaptSeen is the reused scratch set of transport_ids present this tick
	// (for stale-key pruning); adaptAliveIdx maps transport_id → current leg
	// index for alive legs so a DropLegs decision can be expressed as the
	// index the host expects. adaptPrevKnown snapshots the established set
	// just before a probe add so the next tick can diff out the new leg.
	adaptSeen      = map[string]bool{}
	adaptAliveIdx  = map[string]int{}
	adaptPrevKnown = map[string]bool{}
)

var (
	// adaptThroughputEWMA / adaptPeak are the smoothed aggregate received
	// throughput and its slowly-decaying high-water mark (the AIMD load
	// signal). adaptSeeded guards the one-time seed on the first tick with
	// real throughput. adaptIdleCount is the consecutive-idle tick counter.
	adaptThroughputEWMA float64
	adaptPeak           float64
	adaptSeeded         bool
	adaptIdleCount      int
	// adaptTick counts ticks (drives the explore cadence). adaptTarget is the
	// current steady width EXPLORE probes at; it starts at the decide Mux and
	// grow/shrink move it within [floor, cap].
	adaptTick   int
	adaptTarget = adaptDecideMux
	// Probe state machine (see probe-and-prune): adaptProbeTID is the leg on
	// probation ("" = none), adaptProbeAge counts ticks since adoption,
	// adaptProbePending is set the tick we request the speculative add.
	adaptProbeTID     string
	adaptProbeAge     int
	adaptProbePending bool
)

const (
	// adaptDecideMux is the starting mux width decideAdaptive returns and the
	// initial size target.
	adaptDecideMux = 3
	// adaptFloor / adaptCap bound the leg count the size dimension may reach.
	adaptFloor = 2
	adaptCap   = 6
	// adaptAlpha is the EWMA smoothing factor for both the latency and the
	// throughput signals (newest sample weighted 30%).
	adaptAlpha = 0.3
	// adaptPeakDecay bleeds the throughput peak down ~2%/tick so the
	// saturation bar tracks a sustained drop rather than one historical burst.
	adaptPeakDecay = 0.98
	// adaptExploreEvery is the explore cadence: start a probe every N ticks
	// when the set is stable.
	adaptExploreEvery = 6
	// adaptObserve is how many ticks a probe runs before it is judged.
	adaptObserve = 3
)

// tickAdaptive is the composite arbitrated controller. See the block comment
// above for the load/latency/explore model and the priority ordering.
func tickAdaptive(input tickInputWire) uint64 {
	adaptTick++

	// --- Phase 1: refresh the shared per-transport_id state (every tick,
	// before arbitration). One pass computes the latency EWMA, the aggregate
	// received byte-delta, the alive count / oldest-alive index / alive
	// index map, and records presence for stale-key pruning.
	for k := range adaptSeen {
		delete(adaptSeen, k)
	}
	for k := range adaptAliveIdx {
		delete(adaptAliveIdx, k)
	}
	var rawTotal float64
	aliveCount := 0
	oldestAliveIdx := -1
	for _, l := range input.Legs {
		tid := l.TransportID
		if tid != "" {
			adaptSeen[tid] = true
		}
		if !l.Alive {
			continue
		}
		aliveCount++
		if oldestAliveIdx == -1 || l.Index < oldestAliveIdx {
			oldestAliveIdx = l.Index
		}
		if tid == "" {
			continue
		}
		adaptAliveIdx[tid] = l.Index
		// Latency EWMA — fold in only real measurements (0 == unknown).
		if l.LatencyMs > 0 {
			sample := float64(l.LatencyMs)
			if prev, ok := adaptLatEWMA[tid]; ok {
				adaptLatEWMA[tid] = adaptAlpha*sample + (1-adaptAlpha)*prev
			} else {
				adaptLatEWMA[tid] = sample
			}
		}
		// Throughput: accumulate this interval's received byte-delta. Only a
		// non-negative delta is real; a counter that went backwards means a
		// reset/new leg, not throughput.
		if prev, ok := adaptPrevRecv[tid]; ok && l.RecvBytes >= prev {
			rawTotal += float64(l.RecvBytes - prev)
		}
		adaptPrevRecv[tid] = l.RecvBytes
	}
	// Prune state for transport_ids no longer present.
	for tid := range adaptLatEWMA {
		if !adaptSeen[tid] {
			delete(adaptLatEWMA, tid)
		}
	}
	for tid := range adaptPrevRecv {
		if !adaptSeen[tid] {
			delete(adaptPrevRecv, tid)
		}
	}

	// Fold the raw delta into the smoothed throughput signal and maintain the
	// decaying peak. Seed on the first tick with real throughput so the signal
	// starts on a genuine value rather than ramping from zero. saturated/idle
	// stay false until seeded, so the size dimension holds until load appears.
	saturated, idle := false, false
	if !adaptSeeded {
		if rawTotal > 0 {
			adaptThroughputEWMA = rawTotal
			adaptPeak = rawTotal
			adaptSeeded = true
		}
	} else {
		adaptThroughputEWMA = adaptAlpha*rawTotal + (1-adaptAlpha)*adaptThroughputEWMA
		adaptPeak *= adaptPeakDecay
		if adaptThroughputEWMA > adaptPeak {
			adaptPeak = adaptThroughputEWMA
		}
		if adaptPeak > 0 {
			saturated = adaptThroughputEWMA >= 0.80*adaptPeak
			idle = adaptThroughputEWMA < 0.25*adaptPeak
		}
	}
	// Track consecutive idle ticks independently of whether we can act on them
	// (the "two quiet ticks" requirement is about load, not width).
	if idle {
		adaptIdleCount++
	} else {
		adaptIdleCount = 0
	}

	// --- Phase 2: arbitration — at most ONE structural action, by priority.

	// 1. Nothing alive — nothing to act on.
	if aliveCount == 0 {
		return 0
	}
	// 2. RECOVER: starved below the floor — refill (never gated on a probe).
	if aliveCount < adaptFloor {
		return writeAction(rotationActionWire{AddLeg: true})
	}

	// A probe transiently makes the group target+1 wide; while it is in flight
	// the size/membership dimensions pause so they can't corrupt its accounting.
	probeInFlight := adaptProbePending || adaptProbeTID != ""
	if !probeInFlight {
		// 3. MEMBERSHIP: evict the slowest-EWMA leg iff it is a >=1.5x-median
		// outlier, and replace it (excluding its hops so the new leg differs).
		if idx, hops, ok := adaptWorstOutlier(input.Legs); ok {
			return writeAction(rotationActionWire{
				DropLegs:    []int{idx},
				AddLeg:      true,
				ExcludeHops: hops,
			})
		}
		// 4. GROW: saturated and below the cap — add one leg to load.
		if saturated && aliveCount < adaptCap {
			adaptTarget = aliveCount + 1
			if adaptTarget > adaptCap {
				adaptTarget = adaptCap
			}
			return writeAction(rotationActionWire{AddLeg: true})
		}
		// 5. SHRINK: sustained idle and above the floor — release the oldest
		// leg. Reset the idle counter so we step down one leg per two idle
		// ticks rather than collapsing to the floor at once.
		if adaptIdleCount >= 2 && aliveCount > adaptFloor {
			adaptIdleCount = 0
			adaptTarget = aliveCount - 1
			if adaptTarget < adaptFloor {
				adaptTarget = adaptFloor
			}
			return writeAction(rotationActionWire{DropLegs: []int{oldestAliveIdx}})
		}
	}

	// 6/7. EXPLORE (probe/exploit machine) or hold.
	return adaptExplore(aliveCount)
}

// adaptWorstOutlier reports the index and hops of the slowest alive leg when
// its smoothed latency is a >=1.5x-median outlier over the alive legs'
// smoothed latencies (needs >=2 smoothed samples), else ok=false. This is
// latency-adaptive's eviction test, run over the shared adaptLatEWMA.
func adaptWorstOutlier(legs []legInfoWire) (int, []string, bool) {
	worstIdx := -1
	worstSm := -1.0
	var worstHops []string
	var smoothed []float64
	for _, l := range legs {
		if !l.Alive {
			continue
		}
		sm, ok := adaptLatEWMA[l.TransportID]
		if !ok {
			continue
		}
		smoothed = append(smoothed, sm)
		if sm > worstSm {
			worstSm = sm
			worstIdx = l.Index
			worstHops = l.Hops
		}
	}
	if len(smoothed) < 2 || worstIdx < 0 || worstSm <= 0 {
		return -1, nil, false
	}
	sort.Float64s(smoothed)
	median := medianSorted(smoothed)
	if median > 0 && worstSm >= 1.5*median {
		return worstIdx, worstHops, true
	}
	return -1, nil, false
}

// adaptExplore is the probe-and-prune explore/exploit machine over the shared
// probe state. It adopts a pending probe, observes it adaptObserve ticks, then
// keeps it (dropping the worst established leg) only if it beats that worst —
// else prunes the probe. When no probe is in flight and the set is stable at
// target, it starts a fresh probe on the explore cadence.
func adaptExplore(aliveCount int) uint64 {
	// Adopt: a probe add requested last tick should now be present — the alive
	// transport_id absent from the pre-add known set. Put it on probation.
	if adaptProbePending {
		adaptProbePending = false
		for tid := range adaptAliveIdx {
			if !adaptPrevKnown[tid] {
				adaptProbeTID = tid
				adaptProbeAge = 0
				break
			}
		}
	}

	// Active probe: observe, then graduate-or-discard.
	if adaptProbeTID != "" {
		idx, alive := adaptAliveIdx[adaptProbeTID]
		if !alive {
			// Probe died on its own — abandon the experiment.
			adaptProbeTID = ""
			adaptProbeAge = 0
			return 0
		}
		adaptProbeAge++
		if adaptProbeAge < adaptObserve {
			return 0
		}
		// Judge: probe's smoothed latency vs the worst established leg.
		probeSm, okProbe := adaptLatEWMA[adaptProbeTID]
		worstIdx := -1
		worstSm := -1.0
		for tid, i := range adaptAliveIdx {
			if tid == adaptProbeTID {
				continue
			}
			sm, ok := adaptLatEWMA[tid]
			if !ok {
				continue
			}
			if sm > worstSm {
				worstSm = sm
				worstIdx = i
			}
		}
		if !okProbe || worstIdx < 0 {
			// Not enough latency signal to judge yet — keep observing.
			return 0
		}
		adaptProbeTID = ""
		if probeSm < worstSm {
			// Probe graduates: drop the worst established leg; the probe stays
			// and joins the established set next tick (width returns to target).
			return writeAction(rotationActionWire{DropLegs: []int{worstIdx}})
		}
		// Failed experiment: prune the probe itself, keep the incumbents.
		return writeAction(rotationActionWire{DropLegs: []int{idx}})
	}

	// Explore: on the cadence, when stable at the target width, snapshot the
	// known set and request one speculative leg over a fresh path.
	if adaptTick%adaptExploreEvery == 0 && aliveCount == adaptTarget {
		for k := range adaptPrevKnown {
			delete(adaptPrevKnown, k)
		}
		for tid := range adaptAliveIdx {
			adaptPrevKnown[tid] = true
		}
		adaptProbePending = true
		return writeAction(rotationActionWire{AddLeg: true})
	}
	// Hold.
	return 0
}

// writeAction marshals a rotation action and packs it for the host.
func writeAction(action rotationActionWire) uint64 {
	out, err := json.Marshal(action)
	if err != nil {
		return 0
	}
	return writeOutput(out)
}

// main is required by the WASI target but isn't called by the host at
// decide-time. It runs once at module instantiation.
func main() {}
