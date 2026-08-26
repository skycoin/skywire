// Package preset pkg/router/policy/preset/tick.go c2-net-routing
// carries the stateful on_tick controllers for the adaptive
// presets. In the wasm bundle these controllers keep their state
// in package globals (one module instance per policy load); here
// the same state lives on an Engine value so the native path gets
// one independent controller per evaluator instance. A single
// package-global Engine in the bundle shim reproduces the bundle's
// original global-state behavior exactly.
package preset

import "sort"

// Engine holds the per-transport_id smoothing state and probe/AIMD
// bookkeeping the adaptive tick controllers accumulate across
// ticks. Construct with New; each Engine is independent, so one
// route group's controller state never smears onto another's.
//
// Engine is NOT safe for concurrent use — the router serializes
// on_tick per route group, mirroring the single-threaded wazero
// module the bundle compiles to.
type Engine struct {
	// latency-adaptive
	latAdaptEWMA map[string]float64
	latAdaptSeen map[string]bool

	// elastic-mux
	elasticPrevRecv       map[string]uint64
	elasticSeen           map[string]bool
	elasticThroughputEWMA float64
	elasticPeak           float64
	elasticIdleCount      int
	elasticSeeded         bool

	// probe-and-prune
	probeEWMA     map[string]float64
	probeSeen     map[string]bool
	probeAliveIdx map[string]int
	prevKnownTIDs map[string]bool
	probeTID      string
	probeAge      int
	probePending  bool

	// adaptive (composite)
	adaptLatEWMA        map[string]float64
	adaptPrevRecv       map[string]uint64
	adaptSeen           map[string]bool
	adaptAliveIdx       map[string]int
	adaptThroughputEWMA float64
	adaptPeak           float64
	adaptSeeded         bool
	adaptIdleCount      int
	adaptTarget         int
	// forward (upload / SentBytes) sizing — the exact mirror of the reverse
	// (RecvBytes) machine above, driven by SentBytes deltas. adaptFwdTarget is
	// the forward active width (seeded to adaptFwdActive); an upload-heavy flow
	// widens it under sustained SENT saturation and collapses it back when the
	// upload goes idle, using the SAME EWMA/peak/hysteresis/cooldown constants
	// as the reverse side. When SentBytes never advances the whole forward
	// machine stays dormant (adaptFwdSeeded never trips), so a download-only or
	// idle flow behaves byte-identically to the reverse-only controller.
	adaptPrevSent          map[string]uint64
	adaptFwdThroughputEWMA float64
	adaptFwdPeak           float64
	adaptFwdSeeded         bool
	adaptFwdIdleCount      int
	adaptFwdTarget         int
	adaptFwdSatTicks       int
	// health + anti-churn state
	adaptCooldown  int                // ticks to hold the active set steady after a reshape
	adaptSatTicks  int                // consecutive saturated ticks (grow only on a sustained signal)
	adaptUnhealthy map[string]int     // per-active-leg consecutive gross-outlier-latency ticks
	adaptStall     map[string]int     // per-active-leg consecutive low/no-throughput ticks under load
	adaptRecvRate  map[string]float64 // per-active-leg EWMA recv byte-rate (throughput signal)

	// ledbat (delay-based scavenger)
	ledbatEWMA map[string]float64 // per-leg EWMA-smoothed one-way-ish delay (ms)
	ledbatBase map[string]float64 // per-leg base (running-min smoothed) delay (ms)
	ledbatSeen map[string]bool    // transport_ids present this tick (for GC)

	// coupled (MPTCP-style coupled congestion control)
	coupledLatEWMA         map[string]float64 // per-leg smoothed latency (worst-leg tiebreak)
	coupledPrevRetransmits map[string]uint64  // per-leg last-tick retransmit counter (loss delta)
	coupledPrevSent        map[string]uint64  // per-leg last-tick sent bytes (packet-normalize loss)
	coupledSeen            map[string]bool    // transport_ids present this tick (stale-state GC)
	coupledCooldown        int                // ticks held steady after a grow/shed (anti-churn)
}

// New returns an Engine with initialized state maps and the
// adaptive steady active target seeded to adaptRevActive (the reverse
// active width decideAdaptive requests, minus its warm-standby reserve),
// matching the bundle's package-global initializers.
func New() *Engine {
	return &Engine{
		latAdaptEWMA:    map[string]float64{},
		latAdaptSeen:    map[string]bool{},
		elasticPrevRecv: map[string]uint64{},
		elasticSeen:     map[string]bool{},
		probeEWMA:       map[string]float64{},
		probeSeen:       map[string]bool{},
		probeAliveIdx:   map[string]int{},
		prevKnownTIDs:   map[string]bool{},
		adaptLatEWMA:    map[string]float64{},
		adaptPrevRecv:   map[string]uint64{},
		adaptPrevSent:   map[string]uint64{},
		adaptSeen:       map[string]bool{},
		adaptAliveIdx:   map[string]int{},
		adaptUnhealthy:  map[string]int{},
		adaptStall:      map[string]int{},
		adaptRecvRate:   map[string]float64{},
		adaptTarget:     adaptRevActive,
		adaptFwdTarget:  adaptFwdActive,
		ledbatEWMA:      map[string]float64{},
		ledbatBase:      map[string]float64{},
		ledbatSeen:      map[string]bool{},

		coupledLatEWMA:         map[string]float64{},
		coupledPrevRetransmits: map[string]uint64{},
		coupledPrevSent:        map[string]uint64{},
		coupledSeen:            map[string]bool{},
	}
}

// OnTick dispatches to the named preset's tick controller. Presets
// without tick logic (app-mux, the conditional presets, unknown)
// return the zero RotationAction (no-op) — identical to the
// bundle's on_tick default case.
func (e *Engine) OnTick(name string, legs []LegInfo) RotationAction {
	switch name {
	case "rotating-bw":
		return e.tickRotatingBW(legs)
	case "latency-adaptive":
		return e.tickLatencyAdaptive(legs)
	case "elastic-mux":
		return e.tickElasticMux(legs)
	case "probe-and-prune":
		return e.tickProbeAndPrune(legs)
	case "coupled":
		return e.tickCoupled(legs)
	case "adaptive":
		return e.tickAdaptive(legs)
	case "ledbat":
		return e.tickLedbat(legs)
	default:
		return RotationAction{}
	}
}

// --- shared no-dip controller helpers ---

// nodipStandbyMax caps the warm-standby pool a shed/park may hold before it
// falls back to a real teardown.
const nodipStandbyMax = 2

// legSets is the per-tick active/standby partition of a route group's legs.
type legSets struct {
	activeCount  int
	standbyCount int
	oldestActive int
	promotable   int
}

// classifyLegs partitions the leg snapshot into active/standby counts and the
// two indices the controllers act on.
func classifyLegs(legs []LegInfo) legSets {
	s := legSets{oldestActive: -1, promotable: -1}
	for _, l := range legs {
		if !l.Alive {
			continue
		}
		if l.Standby {
			s.standbyCount++
			if s.promotable == -1 || l.Index < s.promotable {
				s.promotable = l.Index
			}
			continue
		}
		s.activeCount++
		if s.oldestActive == -1 || l.Index < s.oldestActive {
			s.oldestActive = l.Index
		}
	}
	return s
}

// growActive raises the active width by one with no setup dip when a warm
// spare is parked; otherwise requests a genuinely fresh leg.
func growActive(s legSets) RotationAction {
	if s.promotable >= 0 {
		return RotationAction{PromoteFromStandby: []int{s.promotable}}
	}
	return RotationAction{AddLeg: true}
}

// swapActive evicts leg idx and fills its slot from the warm reserve in the
// same tick (hot-swap); parks idx alone when no spare is available.
func swapActive(s legSets, idx int) RotationAction {
	if s.promotable >= 0 {
		return RotationAction{
			PromoteFromStandby: []int{s.promotable},
			DemoteToStandby:    []int{idx},
		}
	}
	return RotationAction{DemoteToStandby: []int{idx}}
}

// shedActive removes leg idx from the active set with no capacity dip while the
// pool has room, else tears it down.
func shedActive(s legSets, idx int) RotationAction {
	if s.standbyCount < nodipStandbyMax {
		return RotationAction{DemoteToStandby: []int{idx}}
	}
	return RotationAction{DropLegs: []int{idx}}
}

// --- rotating-bw ---

func (e *Engine) tickRotatingBW(legs []LegInfo) RotationAction {
	var relAct, relSb, fragAct, fragSb []int
	for _, l := range legs {
		if !l.Alive {
			continue
		}
		rel := reliableMuxKind(l.Kind)
		switch {
		case l.Standby && rel:
			relSb = append(relSb, l.Index)
		case l.Standby:
			fragSb = append(fragSb, l.Index)
		case rel:
			relAct = append(relAct, l.Index)
		default:
			fragAct = append(fragAct, l.Index)
		}
	}
	active := len(relAct) + len(fragAct)
	alive := active + len(relSb) + len(fragSb)
	if alive == 0 {
		return RotationAction{}
	}
	lo := func(s []int) int {
		m := s[0]
		for _, v := range s {
			if v < m {
				m = v
			}
		}
		return m
	}
	hi := func(s []int) int {
		m := s[0]
		for _, v := range s {
			if v > m {
				m = v
			}
		}
		return m
	}

	switch {
	case len(relAct) >= 1 && len(fragAct) > 0:
		return RotationAction{DemoteToStandby: []int{hi(fragAct)}}
	case len(fragAct) > 0 && len(relSb) > 0:
		return RotationAction{PromoteFromStandby: []int{lo(relSb)}, DemoteToStandby: []int{hi(fragAct)}}
	case len(relAct) < targetMux && len(relSb) > 0:
		return RotationAction{PromoteFromStandby: []int{lo(relSb)}}
	case len(relAct) == 0 && len(fragAct) == 0 && len(fragSb) > 0:
		return RotationAction{PromoteFromStandby: []int{lo(fragSb)}}
	case len(relAct) > targetMux:
		return RotationAction{DemoteToStandby: []int{hi(relAct)}}
	case len(fragAct) == 0 && len(relSb) > 0 && len(relAct) > 0:
		return RotationAction{PromoteFromStandby: []int{lo(relSb)}, DemoteToStandby: []int{lo(relAct)}}
	default:
		return RotationAction{}
	}
}

// --- latency-adaptive ---

const latAdaptAlpha = 0.3

func (e *Engine) tickLatencyAdaptive(legs []LegInfo) RotationAction {
	const targetMux = 4

	for k := range e.latAdaptSeen {
		delete(e.latAdaptSeen, k)
	}
	for _, l := range legs {
		tid := l.TransportID
		if tid == "" {
			continue
		}
		e.latAdaptSeen[tid] = true
		if l.Alive && l.LatencyMs > 0 {
			sample := float64(l.LatencyMs)
			if prev, ok := e.latAdaptEWMA[tid]; ok {
				e.latAdaptEWMA[tid] = latAdaptAlpha*sample + (1-latAdaptAlpha)*prev
			} else {
				e.latAdaptEWMA[tid] = sample
			}
		}
	}
	for tid := range e.latAdaptEWMA {
		if !e.latAdaptSeen[tid] {
			delete(e.latAdaptEWMA, tid)
		}
	}

	sets := classifyLegs(legs)
	worstIdx := -1
	worstSmoothed := -1.0
	var smoothed []float64
	for _, l := range legs {
		if !l.Alive || l.Standby {
			continue
		}
		sm, ok := e.latAdaptEWMA[l.TransportID]
		if !ok {
			continue
		}
		smoothed = append(smoothed, sm)
		if sm > worstSmoothed {
			worstSmoothed = sm
			worstIdx = l.Index
		}
	}
	if sets.activeCount < targetMux {
		return growActive(sets)
	}
	if sets.activeCount > targetMux {
		return shedActive(sets, sets.oldestActive)
	}
	if len(smoothed) < 2 || worstIdx < 0 || worstSmoothed <= 0 {
		return RotationAction{}
	}

	sort.Float64s(smoothed)
	median := medianSorted(smoothed)

	if median > 0 && worstSmoothed >= 1.5*median {
		return swapActive(sets, worstIdx)
	}
	return RotationAction{}
}

// --- elastic-mux ---

const (
	elasticFloor     = 2
	elasticCap       = 6
	elasticAlpha     = 0.3
	elasticPeakDecay = 0.98
)

func (e *Engine) tickElasticMux(legs []LegInfo) RotationAction {
	for k := range e.elasticSeen {
		delete(e.elasticSeen, k)
	}
	var rawTotal float64
	sets := classifyLegs(legs)
	for _, l := range legs {
		if !l.Alive {
			continue
		}
		tid := l.TransportID
		if tid == "" {
			continue
		}
		e.elasticSeen[tid] = true
		if prev, ok := e.elasticPrevRecv[tid]; ok && l.RecvBytes >= prev && !l.Standby {
			rawTotal += float64(l.RecvBytes - prev)
		}
		e.elasticPrevRecv[tid] = l.RecvBytes
	}
	for tid := range e.elasticPrevRecv {
		if !e.elasticSeen[tid] {
			delete(e.elasticPrevRecv, tid)
		}
	}
	if sets.activeCount == 0 {
		if sets.promotable >= 0 {
			return growActive(sets)
		}
		return RotationAction{}
	}

	if !e.elasticSeeded {
		if rawTotal > 0 {
			e.elasticThroughputEWMA = rawTotal
			e.elasticPeak = rawTotal
			e.elasticSeeded = true
		}
		return RotationAction{}
	}
	e.elasticThroughputEWMA = elasticAlpha*rawTotal + (1-elasticAlpha)*e.elasticThroughputEWMA
	e.elasticPeak *= elasticPeakDecay
	if e.elasticThroughputEWMA > e.elasticPeak {
		e.elasticPeak = e.elasticThroughputEWMA
	}
	if e.elasticPeak <= 0 {
		return RotationAction{}
	}

	smoothed := e.elasticThroughputEWMA
	if smoothed < 0.25*e.elasticPeak {
		e.elasticIdleCount++
	} else {
		e.elasticIdleCount = 0
	}

	if smoothed >= 0.80*e.elasticPeak && sets.activeCount < elasticCap {
		return growActive(sets)
	}
	if e.elasticIdleCount >= 2 && sets.activeCount > elasticFloor {
		e.elasticIdleCount = 0
		return shedActive(sets, sets.oldestActive)
	}
	return RotationAction{}
}

// --- probe-and-prune ---

const (
	probeTarget       = 3
	probeObserveTicks = 3
	probeAlpha        = 0.3
)

func (e *Engine) tickProbeAndPrune(legs []LegInfo) RotationAction {
	for k := range e.probeSeen {
		delete(e.probeSeen, k)
	}
	for k := range e.probeAliveIdx {
		delete(e.probeAliveIdx, k)
	}
	sets := classifyLegs(legs)
	activeCount := 0
	for _, l := range legs {
		tid := l.TransportID
		if tid == "" {
			continue
		}
		e.probeSeen[tid] = true
		if !l.Alive {
			continue
		}
		if l.LatencyMs > 0 {
			sample := float64(l.LatencyMs)
			if prev, ok := e.probeEWMA[tid]; ok {
				e.probeEWMA[tid] = probeAlpha*sample + (1-probeAlpha)*prev
			} else {
				e.probeEWMA[tid] = sample
			}
		}
		if l.Standby {
			continue
		}
		activeCount++
		e.probeAliveIdx[tid] = l.Index
	}
	for tid := range e.probeEWMA {
		if !e.probeSeen[tid] {
			delete(e.probeEWMA, tid)
		}
	}

	if e.probePending {
		e.probePending = false
		for tid := range e.probeAliveIdx {
			if !e.prevKnownTIDs[tid] {
				e.probeTID = tid
				e.probeAge = 0
				break
			}
		}
	}

	if e.probeTID != "" {
		idx, alive := e.probeAliveIdx[e.probeTID]
		if !alive {
			e.probeTID = ""
			e.probeAge = 0
			return RotationAction{}
		}
		e.probeAge++
		if e.probeAge < probeObserveTicks {
			return RotationAction{}
		}
		probeSm, okProbe := e.probeEWMA[e.probeTID]
		worstIdx := -1
		worstSm := -1.0
		for tid, i := range e.probeAliveIdx {
			if tid == e.probeTID {
				continue
			}
			sm, ok := e.probeEWMA[tid]
			if !ok {
				continue
			}
			if sm > worstSm {
				worstSm = sm
				worstIdx = i
			}
		}
		if !okProbe || worstIdx < 0 {
			return RotationAction{}
		}
		e.probeTID = ""
		if probeSm < worstSm {
			return shedActive(sets, worstIdx)
		}
		return RotationAction{DropLegs: []int{idx}}
	}

	if !e.probePending && activeCount == probeTarget {
		for k := range e.prevKnownTIDs {
			delete(e.prevKnownTIDs, k)
		}
		for tid := range e.probeAliveIdx {
			e.prevKnownTIDs[tid] = true
		}
		e.probePending = true
		return RotationAction{AddLeg: true}
	}
	return RotationAction{}
}

// --- adaptive (composite) ---

const (
	// adaptFwdActive is the STEADY lean forward (upstream / request) leg count: a
	// single low-latency route, so an interactive / idle request path pays no mux
	// head-of-line cost. Like the reverse side it is only a STEADY floor: the
	// forward mux WIDENS under sustained UPLOAD (SentBytes) saturation — tickAdaptive
	// grows adaptFwdTarget and emits AddForwardLeg (forward-only aux legs) — then
	// collapses back to adaptFwdActive when the upload goes idle. adaptRevActive is
	// the STEADY active reverse width — deliberately 1:
	// an interactive / idle flow rides ONE good leg (leg 0) and is never
	// scattered+reordered across high-variance legs. The reverse mux only WIDENS
	// under sustained bulk load (tickAdaptive promotes warm spares), then shrinks
	// back. adaptStandbyMax warm spares are established alongside and parked for
	// instant promotion, so decideAdaptive requests a symmetric Mux =
	// adaptRevActive + adaptStandbyMax (every leg full-duplex; forward-lean usage
	// is a send-side decision), and adaptTarget seeds to adaptRevActive.
	adaptFwdActive = 1
	adaptRevActive = 1
	adaptCap       = 8
	adaptAlpha     = 0.3
	adaptPeakDecay = 0.98
	// adaptStandbyMax is the warm-standby reserve the proactive park fills to.
	// adaptStandbyMin is the HARD FLOOR load may NEVER drain below: under
	// saturation the tick grows the active width with a FRESH leg rather than
	// promoting the last warm spare, so at least adaptStandbyMin instant-promote
	// legs are always parked and ready. This keeps warm standby a resilience
	// reserve (covers an active-leg drop with zero re-establish dip) instead of
	// load headroom that peak traffic empties to zero.
	// adaptStandbyMax sizes the warm-standby pool to "ALL VIABLE routes", not an
	// arbitrary number: the adaptive default pre-establishes a disjoint standby
	// route on every existing transport that can reach the destination (a 2-hop
	// leg src->I->dst reuses the ALREADY-UP src->I transport — no new first hop),
	// so any of them can be promoted to carrying at a moment's notice for maximal
	// resilience and instant, dip-free failover. This is a high SANITY CAP, not a
	// target: establishMuxRoutes builds up to Mux (= adaptRevActive +
	// adaptStandbyMax) best-effort and takes only as many as the topology's
	// disjoint-intermediate set actually offers. The RSN-oracle transport-type
	// ranking picks the best (stcpr/squicr) legs first and the health-gate keeps
	// latency outliers OUT of the ACTIVE set.
	//
	// UNCAPPED 2026-08-26 (operator direction): establish the full disjoint
	// standby pool the topology offers, not a tiny reserve — one warm-standby
	// route on every intermediate that can reach the destination, so any can be
	// promoted at a moment's notice. establishMuxRoutes builds up to Mux
	// (= adaptRevActive + adaptStandbyMax) best-effort and self-limits to the
	// disjoint-intermediate set the topology actually has; the RSN-oracle
	// transport-type ranking picks stcpr/squicr first and the health-gate keeps
	// latency outliers OUT of the ACTIVE set (adaptCap still caps active width).
	//
	// KNOWN RISKS to fix FORWARD as they're observed (MEASURED 2026-08-22 at 31):
	//   (1) setup-node dial STORM — each standby leg is its own route-setup
	//       handshake; at scale they fail "setup-node dial: context deadline
	//       exceeded" and self-heal re-dials forever (the puzzle: the src->I
	//       transport is UP, yet the route won't set up over it). Real fix =
	//       pipeline/batch the cascade setup so N legs aren't N handshakes.
	//   (2) wide-mux download STALL — a bulk transfer over a ~32-leg group timed
	//       out instead of aggregating; the reorder/aggregation (reorder.go,
	//       datagram_route_group.go) doesn't scale to many legs (#86 family).
	// Both are being worked; the pool is uncapped deliberately to surface them.
	//
	// TRUE UNCAP 2026-08-26: raised 60 -> 512 so the standby pool is the full
	// disjoint set the topology offers (a warm visor exposes ~480 disjoint
	// intermediates to a busy exit), not an arbitrary ceiling. 512 sits above any
	// realistic single-exit disjoint count, so the binding limit is the topology,
	// discovered by the self-heal's no-progress backoff (route_group.go) — it
	// fills to what actually establishes and stops, re-probing as new transports
	// come online. Risk (1) above is contained two ways: establishMuxRoutes now
	// caps its FOREGROUND initial dial (initialForegroundMux) so the dial returns
	// fast on a lean mux, and the background self-heal fills the rest one leg at a
	// time with the no-progress backoff — so uncapping the pool never becomes a
	// dial storm at connect time.
	adaptStandbyMax = 512
	adaptStandbyMin = 1
	// Health + anti-churn. A leg is a gross-outlier (kept OUT of the active mux,
	// where the no-skip reorder buffer would head-of-line-stall on it) when its
	// EWMA latency exceeds the absolute ceiling OR adaptOutlierMult x the active
	// median. A signal must persist adaptHysteresis consecutive ticks before it
	// reshapes the set, and adaptReshapeCooldown ticks of steadiness follow any
	// reshape — so the active set does not add/drop/promote/park every tick
	// (each reshape disrupts in-flight flows). adaptStallTicks catches a leg that
	// is alive but passes no traffic while the group is loaded (a dead 0-byte
	// leg) and evicts it too.
	//
	// The stall gate is throughput-graded, not just zero/non-zero: a leg whose
	// EWMA recv-rate is below adaptThroughputOutlierFrac of the active-set MEDIAN
	// recv-rate — a grossly-low-throughput leg, e.g. a webrtc data channel that
	// carries a fraction of what the stcpr legs do — drags the no-skip reorder
	// buffer exactly like a dead leg and is evicted the same way. Zero traffic is
	// the frac=0 special case, so this strictly widens the old dead-leg gate.
	adaptLatCeilingMs          = 1500
	adaptOutlierMult           = 3.0
	adaptHysteresis            = 3
	adaptReshapeCooldown       = 3
	adaptThroughputOutlierFrac = 0.25
)

func (e *Engine) tickAdaptive(legs []LegInfo) RotationAction {
	for k := range e.adaptSeen {
		delete(e.adaptSeen, k)
	}
	for k := range e.adaptAliveIdx {
		delete(e.adaptAliveIdx, k)
	}
	var rawTotal float64
	var rawSent float64
	aliveCount := 0
	standbyCount := 0
	newestAliveIdx := -1
	promotableIdx := -1
	movedRecv := make(map[string]bool)
	for _, l := range legs {
		tid := l.TransportID
		if tid != "" {
			e.adaptSeen[tid] = true
		}
		if !l.Alive {
			continue
		}
		if l.Standby {
			standbyCount++
			if promotableIdx == -1 || l.Index < promotableIdx {
				promotableIdx = l.Index
			}
			continue
		}
		aliveCount++
		if l.Index > newestAliveIdx {
			newestAliveIdx = l.Index
		}
		if tid == "" {
			continue
		}
		e.adaptAliveIdx[tid] = l.Index
		if l.LatencyMs > 0 {
			sample := float64(l.LatencyMs)
			if prev, ok := e.adaptLatEWMA[tid]; ok {
				e.adaptLatEWMA[tid] = adaptAlpha*sample + (1-adaptAlpha)*prev
			} else {
				e.adaptLatEWMA[tid] = sample
			}
		}
		if prev, ok := e.adaptPrevRecv[tid]; ok {
			if l.RecvBytes > prev {
				d := float64(l.RecvBytes - prev)
				rawTotal += d
				movedRecv[tid] = true
				// Per-leg recv-rate EWMA — the throughput signal the low-outlier
				// stall gate ranks on. Smoothed like latency so one busy/quiet
				// tick doesn't reclassify a leg.
				if r, ok := e.adaptRecvRate[tid]; ok {
					e.adaptRecvRate[tid] = adaptAlpha*d + (1-adaptAlpha)*r
				} else {
					e.adaptRecvRate[tid] = d
				}
			} else if r, ok := e.adaptRecvRate[tid]; ok {
				// No traffic this tick: decay toward zero so a leg that STOPS
				// delivering falls below the active median and is caught.
				e.adaptRecvRate[tid] = (1 - adaptAlpha) * r
			}
		}
		e.adaptPrevRecv[tid] = l.RecvBytes
		// Forward (upload) throughput — the SentBytes mirror of the RecvBytes
		// accumulation above. Feeds the independent forward sizing machine.
		if prev, ok := e.adaptPrevSent[tid]; ok && l.SentBytes > prev {
			rawSent += float64(l.SentBytes - prev)
		}
		e.adaptPrevSent[tid] = l.SentBytes
	}
	for tid := range e.adaptLatEWMA {
		if !e.adaptSeen[tid] {
			delete(e.adaptLatEWMA, tid)
		}
	}
	for tid := range e.adaptPrevRecv {
		if !e.adaptSeen[tid] {
			delete(e.adaptPrevRecv, tid)
		}
	}
	for tid := range e.adaptRecvRate {
		if !e.adaptSeen[tid] {
			delete(e.adaptRecvRate, tid)
		}
	}
	for tid := range e.adaptPrevSent {
		if !e.adaptSeen[tid] {
			delete(e.adaptPrevSent, tid)
		}
	}

	saturated, idle := false, false
	if !e.adaptSeeded {
		if rawTotal > 0 {
			e.adaptThroughputEWMA = rawTotal
			e.adaptPeak = rawTotal
			e.adaptSeeded = true
		}
	} else {
		e.adaptThroughputEWMA = adaptAlpha*rawTotal + (1-adaptAlpha)*e.adaptThroughputEWMA
		e.adaptPeak *= adaptPeakDecay
		if e.adaptThroughputEWMA > e.adaptPeak {
			e.adaptPeak = e.adaptThroughputEWMA
		}
		if e.adaptPeak > 0 {
			saturated = e.adaptThroughputEWMA >= 0.80*e.adaptPeak
			idle = e.adaptThroughputEWMA < 0.25*e.adaptPeak
		}
	}
	if idle {
		e.adaptIdleCount++
	} else {
		e.adaptIdleCount = 0
	}

	// Forward (upload) saturation/idle — the SentBytes mirror of the block
	// above, using the SAME EWMA smoothing, peak decay, and 0.80/0.25
	// saturated/idle thresholds. Stays fully dormant (fwdSaturated / fwdIdle
	// both false, adaptFwdSeeded never set) while SentBytes is flat, so a
	// download-only or idle flow evolves exactly as before.
	fwdSaturated, fwdIdle := false, false
	if !e.adaptFwdSeeded {
		if rawSent > 0 {
			e.adaptFwdThroughputEWMA = rawSent
			e.adaptFwdPeak = rawSent
			e.adaptFwdSeeded = true
		}
	} else {
		e.adaptFwdThroughputEWMA = adaptAlpha*rawSent + (1-adaptAlpha)*e.adaptFwdThroughputEWMA
		e.adaptFwdPeak *= adaptPeakDecay
		if e.adaptFwdThroughputEWMA > e.adaptFwdPeak {
			e.adaptFwdPeak = e.adaptFwdThroughputEWMA
		}
		if e.adaptFwdPeak > 0 {
			fwdSaturated = e.adaptFwdThroughputEWMA >= 0.80*e.adaptFwdPeak
			fwdIdle = e.adaptFwdThroughputEWMA < 0.25*e.adaptFwdPeak
		}
	}
	if fwdIdle {
		e.adaptFwdIdleCount++
	} else {
		e.adaptFwdIdleCount = 0
	}

	// Anti-churn cooldown: after any reshape, hold the active set steady for a
	// few ticks so a transient signal can't reshape the mux every tick (each
	// reshape disrupts in-flight flows).
	if e.adaptCooldown > 0 {
		e.adaptCooldown--
	}
	// Sustained-saturation streak — grow only on a multi-tick signal, not a blip.
	if saturated {
		e.adaptSatTicks++
	} else {
		e.adaptSatTicks = 0
	}
	// Forward sustained-saturation streak (upload analog).
	if fwdSaturated {
		e.adaptFwdSatTicks++
	} else {
		e.adaptFwdSatTicks = 0
	}

	// Active-set latency median (EWMA) for gross-outlier classification, plus the
	// per-active-leg health streaks. A leg (never leg 0) that reads a
	// gross-latency-outlier, OR is alive-but-passes-no-traffic while the group is
	// loaded (a dead 0-byte leg), for adaptHysteresis consecutive ticks is
	// UNHEALTHY and must leave the active mux — where the no-skip reorder buffer
	// would otherwise head-of-line-stall on it. Hysteresis keeps a single spike
	// from churning the set.
	var activeSm []float64
	var activeRates []float64
	for _, l := range legs {
		if !l.Alive || l.Standby {
			continue
		}
		if sm, ok := e.adaptLatEWMA[l.TransportID]; ok && sm > 0 {
			activeSm = append(activeSm, sm)
		}
		if r, ok := e.adaptRecvRate[l.TransportID]; ok && r > 0 {
			activeRates = append(activeRates, r)
		}
	}
	activeMedian := 0.0
	if len(activeSm) > 0 {
		sort.Float64s(activeSm)
		activeMedian = medianSorted(activeSm)
	}
	// Active-set MEDIAN recv-rate — the throughput yardstick a leg is judged a
	// low-outlier against. Only meaningful with ≥2 rate samples (a single active
	// leg has no peer to be an outlier of).
	activeMedianRate := 0.0
	if len(activeRates) >= 2 {
		sort.Float64s(activeRates)
		activeMedianRate = medianSorted(activeRates)
	}
	groupLoaded := rawTotal > 0
	unhealthyIdx := -1
	unhealthyTID := ""
	var unhealthyHops []string
	for _, l := range legs {
		if !l.Alive || l.Standby || l.Index == 0 || l.TransportID == "" {
			continue
		}
		tid := l.TransportID
		if legUnhealthyLat(e.adaptLatEWMA[tid], activeMedian) {
			e.adaptUnhealthy[tid]++
		} else {
			delete(e.adaptUnhealthy, tid)
		}
		if groupLoaded && (!movedRecv[tid] || legLowThroughput(e.adaptRecvRate[tid], activeMedianRate)) {
			e.adaptStall[tid]++
		} else {
			delete(e.adaptStall, tid)
		}
		if unhealthyIdx == -1 && (e.adaptUnhealthy[tid] >= adaptHysteresis || e.adaptStall[tid] >= adaptHysteresis) {
			unhealthyIdx = l.Index
			unhealthyTID = tid
			unhealthyHops = l.Hops
		}
	}
	for tid := range e.adaptUnhealthy {
		if !e.adaptSeen[tid] {
			delete(e.adaptUnhealthy, tid)
		}
	}
	for tid := range e.adaptStall {
		if !e.adaptSeen[tid] {
			delete(e.adaptStall, tid)
		}
	}

	// healthyPromotable = the standby leg SAFE and BEST to bring into the active
	// set: alive, not a known gross-latency-outlier, and — among those — the
	// FASTEST by measured latency (lowest LatencyMs, which snapshotLegs sources
	// from the live end-to-end route latency, not the stale first-hop transport
	// sample). Growing onto the fastest reserve leg keeps the active mux the
	// lowest-latency set, so a widening download never pulls a slow leg in ahead
	// of a fast one. A standby whose latency is not yet measured (LatencyMs<=0)
	// is a lower-priority fallback (lowest index) used only when no measured-
	// healthy spare exists — an unknown leg is promoted optimistically and
	// evicted later by rule (2) if its EWMA reveals it is bad. A bad standby
	// stays parked (harmless keepalive) rather than stalling the active mux.
	healthyPromotable := -1 // fastest measured-healthy standby
	healthyPromLat := 0.0   // its latency (for the running min)
	healthyUnknown := -1    // lowest-index healthy standby with no latency yet
	for _, l := range legs {
		if !l.Alive || !l.Standby {
			continue
		}
		if legUnhealthyLat(float64(l.LatencyMs), activeMedian) {
			continue
		}
		lat := float64(l.LatencyMs)
		if lat <= 0 {
			if healthyUnknown == -1 || l.Index < healthyUnknown {
				healthyUnknown = l.Index
			}
			continue
		}
		if healthyPromotable == -1 || lat < healthyPromLat {
			healthyPromotable = l.Index
			healthyPromLat = lat
		}
	}
	if healthyPromotable == -1 {
		healthyPromotable = healthyUnknown
	}

	// desiredActive is the combined active-leg width the group converges to:
	// the reverse steady/grown target PLUS any extra forward legs the upload
	// machine has grown (forwardExtra). Folding the forward growth into the
	// convergence target is what stops the reverse-side park/drop-recovery
	// rules from immediately tearing down a leg the forward machine just added.
	// When the forward machine is dormant (SentBytes flat) forwardExtra is 0
	// and desiredActive == adaptTarget, so every rule below behaves exactly as
	// the reverse-only controller did.
	forwardExtra := e.adaptFwdTarget - adaptFwdActive
	desiredActive := e.adaptTarget + forwardExtra
	if desiredActive > adaptCap {
		desiredActive = adaptCap
	}

	// (0) No active legs — bring one up, preferring a healthy warm spare.
	if aliveCount == 0 {
		if healthyPromotable >= 0 {
			return RotationAction{PromoteFromStandby: []int{healthyPromotable}}
		}
		if promotableIdx >= 0 {
			return RotationAction{PromoteFromStandby: []int{promotableIdx}}
		}
		return RotationAction{AddLeg: true}
	}

	// (1) Drop recovery (safety — bypasses the cooldown). An active leg died
	// (active fell below the steady target): promote a HEALTHY warm spare
	// INSTANTLY and re-establish a replacement to restore the floor — zero
	// re-dial dip. Restoring lost capacity outranks every optimization.
	if aliveCount < desiredActive {
		if healthyPromotable >= 0 {
			return RotationAction{PromoteFromStandby: []int{healthyPromotable}, AddLeg: true}
		}
		return RotationAction{AddLeg: true}
	}

	// (2) Evict a SUSTAINED-unhealthy active leg (the health gate — the fix for
	// stuttering connections). Never leg 0, never the last active leg. Hot-swap a
	// healthy spare in when one exists; else park the bad leg (drop only when the
	// reserve is full, excluding its hops so self-heal re-dials a different path).
	if unhealthyIdx > 0 && aliveCount > 1 {
		e.adaptCooldown = adaptReshapeCooldown
		delete(e.adaptUnhealthy, unhealthyTID)
		delete(e.adaptStall, unhealthyTID)
		if healthyPromotable >= 0 {
			return RotationAction{PromoteFromStandby: []int{healthyPromotable}, DemoteToStandby: []int{unhealthyIdx}}
		}
		if standbyCount < adaptStandbyMax {
			return RotationAction{DemoteToStandby: []int{unhealthyIdx}}
		}
		return RotationAction{DropLegs: []int{unhealthyIdx}, ExcludeHops: unhealthyHops}
	}

	// Under cooldown, hold the set steady (anti-churn) — no optimization reshape.
	if e.adaptCooldown > 0 {
		return RotationAction{}
	}

	// (3) Proactive park: converge the surplus down to the steady active target
	// as warm standby, parking ALL surplus legs in ONE tick and SLOWEST-first.
	// Establishes the reserve at dial (never leg 0; not under load) and is what
	// makes an idle / interactive flow settle onto the fastest few legs.
	//
	// Two properties matter here, both driven by the live end-to-end route
	// latency (snapshotLegs sources LegInfo.LatencyMs from legEndToEndLatencyMs,
	// not the stale first-hop transport RTT):
	//   - ALL surplus at once: with the mux uncapped (adaptStandbyMax=60) the
	//     router brings up ~60 legs ALL born active; a one-leg-per-tick park can
	//     never catch up, leaving a 25-30-wide active mux whose no-skip reorder
	//     buffer head-of-line-stalls on its slowest leg (measured: 335 B took
	//     22 s over a 28-wide idle mux). Parking the whole surplus in one tick
	//     collapses that to the lean target immediately.
	//   - SLOWEST-first: the surplus we shed is the highest-latency active legs,
	//     so the set we KEEP active is always the fastest desiredActive legs. A
	//     slow leg never sits active while a faster leg is parked.
	// Cooldown is set after a bulk park so the freshly-lean set settles before
	// any optimization reshape; drop-recovery (1) and unhealthy-evict (2) still
	// bypass it, so safety is preserved.
	if !saturated && !fwdSaturated && aliveCount > desiredActive && standbyCount < adaptStandbyMax && newestAliveIdx > 0 {
		// Rank active non-leg-0 legs by smoothed latency, slowest first. Use the
		// EWMA where we have it, else the raw sample; an unmeasured leg sorts as
		// slowest (parked first) so unknowns don't linger in the active set.
		type actLeg struct {
			idx int
			lat float64
		}
		var act []actLeg
		for _, l := range legs {
			if !l.Alive || l.Standby || l.Index == 0 {
				continue
			}
			lat := e.adaptLatEWMA[l.TransportID]
			if lat <= 0 {
				lat = float64(l.LatencyMs)
			}
			if lat <= 0 {
				lat = adaptLatCeilingMs // unknown → park first
			}
			act = append(act, actLeg{l.Index, lat})
		}
		sort.Slice(act, func(i, j int) bool { return act[i].lat > act[j].lat })
		surplus := aliveCount - desiredActive
		if room := adaptStandbyMax - standbyCount; surplus > room {
			surplus = room
		}
		if surplus > len(act) {
			surplus = len(act)
		}
		if surplus > 0 {
			demote := make([]int, surplus)
			for i := 0; i < surplus; i++ {
				demote[i] = act[i].idx
			}
			e.adaptCooldown = adaptReshapeCooldown
			return RotationAction{DemoteToStandby: demote}
		}
	}

	// (4) Grow the active width ONLY on SUSTAINED bulk saturation (multi-tick),
	// consuming surplus standby above the floor first, else a fresh leg — so the
	// wide mux is reserved for real bulk load and never drains the reserve below
	// adaptStandbyMin.
	if e.adaptSatTicks >= adaptHysteresis && aliveCount < adaptCap {
		// Grow the REVERSE target by one. Subtract forwardExtra so the reverse
		// width tracks only the download legs even when forward growth has
		// enlarged aliveCount (a no-op when the forward machine is dormant).
		e.adaptTarget = aliveCount - forwardExtra + 1
		if e.adaptTarget > adaptCap {
			e.adaptTarget = adaptCap
		}
		e.adaptSatTicks = 0
		e.adaptCooldown = adaptReshapeCooldown
		if healthyPromotable >= 0 && standbyCount > adaptStandbyMin {
			return RotationAction{PromoteFromStandby: []int{healthyPromotable}}
		}
		return RotationAction{AddLeg: true}
	}

	// (4b) Grow the FORWARD width on SUSTAINED upload saturation — the exact
	// mirror of (4), driven by SentBytes. Emits AddForwardLeg so the router
	// appends the aux leg forward-only (appendRouteAsymmetric addFwd=true,
	// addRev=false): the extra upstream send capacity is added WITHOUT enlarging
	// the reverse/download set. Bounded by the same adaptCap as the reverse side.
	// A fresh leg (not a warm-standby promote) is used because the standby pool
	// is full-duplex — promoting one would also grow the reverse set.
	if e.adaptFwdSatTicks >= adaptHysteresis && aliveCount < adaptCap && desiredActive < adaptCap {
		e.adaptFwdTarget++
		if e.adaptFwdTarget > adaptCap {
			e.adaptFwdTarget = adaptCap
		}
		e.adaptFwdSatTicks = 0
		e.adaptCooldown = adaptReshapeCooldown
		return RotationAction{AddForwardLeg: true}
	}

	// (5) Shrink on SUSTAINED idle back toward the single-leg steady target, so a
	// finished bulk transfer releases its extra legs (parked to the reserve, or
	// dropped once it is full). Never leg 0. The reverse-portion width
	// (aliveCount-forwardExtra) gates this so forward-grown legs aren't shrunk
	// here (rule 5b owns them).
	if e.adaptIdleCount >= adaptHysteresis && aliveCount-forwardExtra > adaptRevActive && newestAliveIdx > 0 {
		e.adaptIdleCount = 0
		e.adaptTarget = aliveCount - forwardExtra - 1
		if e.adaptTarget < adaptRevActive {
			e.adaptTarget = adaptRevActive
		}
		e.adaptCooldown = adaptReshapeCooldown
		if standbyCount < adaptStandbyMax {
			return RotationAction{DemoteToStandby: []int{newestAliveIdx}}
		}
		return RotationAction{DropLegs: []int{newestAliveIdx}}
	}

	// (5b) Shrink the FORWARD width on SUSTAINED upload idle back toward the lean
	// single forward leg — the mirror of (5), driven by SentBytes idle. Parks the
	// newest leg to the reserve (or drops it once the reserve is full). Never
	// leg 0.
	if e.adaptFwdIdleCount >= adaptHysteresis && e.adaptFwdTarget > adaptFwdActive && aliveCount > adaptFwdActive && newestAliveIdx > 0 {
		e.adaptFwdIdleCount = 0
		e.adaptFwdTarget--
		if e.adaptFwdTarget < adaptFwdActive {
			e.adaptFwdTarget = adaptFwdActive
		}
		e.adaptCooldown = adaptReshapeCooldown
		if standbyCount < adaptStandbyMax {
			return RotationAction{DemoteToStandby: []int{newestAliveIdx}}
		}
		return RotationAction{DropLegs: []int{newestAliveIdx}}
	}

	return RotationAction{}
}

// legUnhealthyLat reports whether a latency (ms) is a gross outlier — above the
// absolute ceiling, or more than adaptOutlierMult times the active-set median.
// A non-positive latency is "unknown" (not yet measured) and treated as healthy
// (optimistic): an unknown leg promoted into the active set is evicted later if
// its EWMA reveals it is bad, so unknowns never block bringing capacity up.
func legUnhealthyLat(lat, median float64) bool {
	if lat <= 0 {
		return false
	}
	if lat > adaptLatCeilingMs {
		return true
	}
	if median > 0 && lat > adaptOutlierMult*median {
		return true
	}
	return false
}

// legLowThroughput reports whether an active leg's EWMA recv-rate is a gross
// LOW outlier — below adaptThroughputOutlierFrac of the active-set median rate.
// Such a leg is delivering a fraction of what its peers do (a low-bandwidth path
// like a webrtc data channel), so it drags the no-skip reorder buffer and is
// evicted from the active mux like a dead leg. Guards: an unknown median (0, too
// few active legs to compare) or an unknown rate (leg not yet measured) is NOT
// an outlier — throughput ranking needs a peer set and a sample to judge against.
func legLowThroughput(rate, medianRate float64) bool {
	if medianRate <= 0 {
		return false
	}
	return rate < adaptThroughputOutlierFrac*medianRate
}

// --- ledbat (delay-based scavenger) ---

const (
	// ledbatAlpha is the EWMA smoothing factor for the per-leg delay signal —
	// matched to the other controllers so a leg's smoothed delay reacts at the
	// same rate everywhere.
	ledbatAlpha = 0.3
	// ledbatTargetMs is the LEDBAT queuing-delay target (RFC 6817 uses 100ms; a
	// tighter 60ms here makes the scavenger yield sooner). When a leg's smoothed
	// delay rises more than this above its own base (min) delay, the flow is
	// judged to be queuing — i.e. causing congestion — and the controller backs
	// off. While every active leg stays within the target of its base, there is
	// no self-induced queuing and the controller may grow.
	ledbatTargetMs = 60.0
	// ledbatMinActive is the yield floor: the controller shrinks the active set
	// toward this width under congestion but never below it, so the flow always
	// keeps one leg making progress.
	ledbatMinActive = 1
)

// tickLedbat is the ledbat preset's on_tick controller: a delay-based,
// background/scavenger congestion response over the mux active set. It tracks
// each leg's EWMA-smoothed delay and a per-leg base (running-min) delay, then
// derives the queuing delay (smoothed - base):
//
//   - BACK OFF (yield): if ANY active leg's queuing delay exceeds ledbatTargetMs
//     — the flow is causing congestion — demote the HIGHEST-queuing-delay active
//     leg (never leg 0) to warm standby, shrinking the active set toward
//     ledbatMinActive. Parked, not torn down, so it can be re-promoted for free
//     when the congestion clears.
//   - GROW: if every active leg reads within the target of its base (no
//     self-induced queuing) and the active set is below ledbatMux, promote one
//     warm standby leg back. Growth only ever draws on the parked reserve — the
//     scavenger never dials fresh legs beyond its lean provisioned width.
//
// One arbitrated action per tick, like the other controllers. Pure integer/
// float arithmetic and map state only (no time.Now / rand) so it is
// deterministic and identical under wazero and native compilation.
func (e *Engine) tickLedbat(legs []LegInfo) RotationAction {
	for k := range e.ledbatSeen {
		delete(e.ledbatSeen, k)
	}
	for _, l := range legs {
		tid := l.TransportID
		if tid == "" {
			continue
		}
		e.ledbatSeen[tid] = true
		if !l.Alive || l.LatencyMs <= 0 {
			continue
		}
		sample := float64(l.LatencyMs)
		if prev, ok := e.ledbatEWMA[tid]; ok {
			e.ledbatEWMA[tid] = ledbatAlpha*sample + (1-ledbatAlpha)*prev
		} else {
			e.ledbatEWMA[tid] = sample
		}
		sm := e.ledbatEWMA[tid]
		if b, ok := e.ledbatBase[tid]; !ok || sm < b {
			e.ledbatBase[tid] = sm
		}
	}
	for tid := range e.ledbatEWMA {
		if !e.ledbatSeen[tid] {
			delete(e.ledbatEWMA, tid)
		}
	}
	for tid := range e.ledbatBase {
		if !e.ledbatSeen[tid] {
			delete(e.ledbatBase, tid)
		}
	}

	sets := classifyLegs(legs)
	// No active legs — bring one up from the parked reserve (no fresh dial).
	if sets.activeCount == 0 {
		if sets.promotable >= 0 {
			return RotationAction{PromoteFromStandby: []int{sets.promotable}}
		}
		return RotationAction{}
	}

	// Queuing delay across the active set. maxQ (including leg 0) decides whether
	// the flow is congesting; worstIdx (excluding leg 0) is the demote target so
	// the primary leg is never parked.
	maxQ := 0.0
	worstIdx := -1
	worstQ := 0.0
	for _, l := range legs {
		if !l.Alive || l.Standby {
			continue
		}
		sm, ok := e.ledbatEWMA[l.TransportID]
		if !ok {
			continue
		}
		q := sm - e.ledbatBase[l.TransportID]
		if q > maxQ {
			maxQ = q
		}
		if l.Index == 0 {
			continue
		}
		if worstIdx == -1 || q > worstQ {
			worstIdx, worstQ = l.Index, q
		}
	}

	// BACK OFF: congestion detected — park the worst active (non-primary) leg,
	// yielding capacity, until we reach the floor.
	if maxQ > ledbatTargetMs && sets.activeCount > ledbatMinActive && worstIdx >= 0 {
		return RotationAction{DemoteToStandby: []int{worstIdx}}
	}

	// GROW: no self-induced queuing and below the cap — re-promote one parked leg.
	if maxQ <= ledbatTargetMs && sets.activeCount < ledbatMux && sets.promotable >= 0 {
		return RotationAction{PromoteFromStandby: []int{sets.promotable}}
	}
	return RotationAction{}
}

// --- coupled (MPTCP-style coupled congestion control) ---

const (
	// coupledMux is the modest symmetric mux the coupled preset provisions and
	// the CEILING the cautious coupled-increase grows the active set back up to.
	coupledMux = 4
	// coupledFloor is the minimum active width the coupled-decrease will never
	// shed below — the aggregate keeps at least this many legs carrying traffic.
	coupledFloor = 2
	// coupledAlpha smooths per-leg latency (the worst-leg tiebreak signal).
	coupledAlpha = 0.3
	// coupledCooldownTicks holds the active set steady for a few ticks after any
	// grow/shed so a transient loss blip can't churn the mux every tick.
	coupledCooldownTicks = 3
)

// tickCoupled is the coupled congestion controller. The two congestion signals
// are per-leg: the RETRANSMIT delta since last tick (loss, packet-normalized by
// the sent-bytes delta) and the EWMA-smoothed latency. It arbitrates at most one
// structural change per tick, in priority order:
//
//	(0) no active legs        -> bring one up (promote a spare, else dial)
//	(1) active < floor        -> restore the floor (recovery; bypasses cooldown)
//	    cooldown active       -> hold steady (anti-churn)
//	(2) any active leg's loss RISING -> COUPLED DECREASE: shed the WORST leg
//	    (highest loss, latency-tiebroken; never leg 0, never below the floor),
//	    concentrating traffic on the good legs instead of equal-spreading it
//	    across a lossy one.
//	(3) NO active leg showing rising loss AND active < ceiling -> COUPLED
//	    INCREASE (LIA-cautious): promote AT MOST one warm spare.
//
// Because a shed fires the instant loss appears and a grow fires only when the
// WHOLE active set is loss-free, aggregate aggressiveness stays bounded — the
// "coupling" property. Deterministic (no time.Now/rand) for wasm parity.
func (e *Engine) tickCoupled(legs []LegInfo) RotationAction {
	for k := range e.coupledSeen {
		delete(e.coupledSeen, k)
	}
	activeCount := 0
	standbyCount := 0
	promotable := -1
	worstIdx := -1
	worstScore := -1.0
	anyRisingLoss := false
	for _, l := range legs {
		tid := l.TransportID
		if tid != "" {
			e.coupledSeen[tid] = true
		}
		if !l.Alive {
			continue
		}
		if l.Standby {
			standbyCount++
			if promotable == -1 || l.Index < promotable {
				promotable = l.Index
			}
			continue
		}
		activeCount++
		if tid == "" {
			continue
		}
		if l.LatencyMs > 0 {
			sample := float64(l.LatencyMs)
			if prev, ok := e.coupledLatEWMA[tid]; ok {
				e.coupledLatEWMA[tid] = coupledAlpha*sample + (1-coupledAlpha)*prev
			} else {
				e.coupledLatEWMA[tid] = sample
			}
		}
		var retransDelta, sentDelta uint64
		if prev, ok := e.coupledPrevRetransmits[tid]; ok && l.Retransmits >= prev {
			retransDelta = l.Retransmits - prev
		}
		if prev, ok := e.coupledPrevSent[tid]; ok && l.SentBytes >= prev {
			sentDelta = l.SentBytes - prev
		}
		e.coupledPrevRetransmits[tid] = l.Retransmits
		e.coupledPrevSent[tid] = l.SentBytes
		if retransDelta > 0 {
			anyRisingLoss = true
		}
		// Loss ratio = retransmits / sent-segments this tick (a ~1KB segment unit;
		// +1 avoids divide-by-zero when a leg sent nothing). Loss dominates the
		// worst-leg score; smoothed latency breaks ties. Leg 0 (the primary) is
		// never a shed candidate.
		sentSegs := sentDelta / 1024
		lossRatio := float64(retransDelta) / float64(sentSegs+1)
		score := lossRatio*1e6 + e.coupledLatEWMA[tid]
		if l.Index != 0 && score > worstScore {
			worstScore = score
			worstIdx = l.Index
		}
	}
	for tid := range e.coupledLatEWMA {
		if !e.coupledSeen[tid] {
			delete(e.coupledLatEWMA, tid)
		}
	}
	for tid := range e.coupledPrevRetransmits {
		if !e.coupledSeen[tid] {
			delete(e.coupledPrevRetransmits, tid)
		}
	}
	for tid := range e.coupledPrevSent {
		if !e.coupledSeen[tid] {
			delete(e.coupledPrevSent, tid)
		}
	}

	if e.coupledCooldown > 0 {
		e.coupledCooldown--
	}

	// (0) No active legs — bring capacity up (recovery, bypasses cooldown).
	if activeCount == 0 {
		if promotable >= 0 {
			return RotationAction{PromoteFromStandby: []int{promotable}}
		}
		return RotationAction{AddLeg: true}
	}
	// (1) Restore the floor if an active leg dropped (recovery, bypasses cooldown).
	if activeCount < coupledFloor {
		if promotable >= 0 {
			return RotationAction{PromoteFromStandby: []int{promotable}}
		}
		return RotationAction{AddLeg: true}
	}
	// Anti-churn: hold the set steady during cooldown.
	if e.coupledCooldown > 0 {
		return RotationAction{}
	}

	// (2) Coupled DECREASE: congestion (rising loss on any active leg) -> shed the
	// worst leg so aggregate aggressiveness stays bounded.
	if anyRisingLoss && activeCount > coupledFloor && worstIdx > 0 {
		e.coupledCooldown = coupledCooldownTicks
		if standbyCount < nodipStandbyMax {
			return RotationAction{DemoteToStandby: []int{worstIdx}}
		}
		return RotationAction{DropLegs: []int{worstIdx}}
	}
	// (3) Coupled INCREASE (LIA-cautious): the whole active set is loss-free and we
	// are below the ceiling -> promote at most one warm spare.
	if !anyRisingLoss && activeCount < coupledMux && promotable >= 0 {
		e.coupledCooldown = coupledCooldownTicks
		return RotationAction{PromoteFromStandby: []int{promotable}}
	}
	return RotationAction{}
}
