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
	adaptPrevKnown      map[string]bool
	adaptThroughputEWMA float64
	adaptPeak           float64
	adaptSeeded         bool
	adaptIdleCount      int
	adaptTick           int
	adaptTarget         int
	adaptProbeTID       string
	adaptProbeAge       int
	adaptProbePending   bool
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
		adaptSeen:       map[string]bool{},
		adaptAliveIdx:   map[string]int{},
		adaptPrevKnown:  map[string]bool{},
		adaptTarget:     adaptRevActive,
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
	case "adaptive":
		return e.tickAdaptive(legs)
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
	// adaptFwdActive is the lean forward (upstream / request) leg count: a
	// single low-latency route, so the request path never pays mux
	// head-of-line cost. adaptRevActive is the steady active reverse (bulk
	// download) width; adaptStandbyMax warm spares are established alongside it
	// and parked by tickAdaptive for instant, dip-free promotion. The reverse
	// mux decideAdaptive requests is therefore adaptRevActive + adaptStandbyMax,
	// and the tick's steady active target (adaptTarget) seeds to adaptRevActive.
	adaptFwdActive    = 1
	adaptRevActive    = 2
	adaptFloor        = 1
	adaptCap          = 6
	adaptAlpha        = 0.3
	adaptPeakDecay    = 0.98
	adaptExploreEvery = 6
	adaptObserve      = 3
	adaptStandbyMax   = 2
)

func (e *Engine) tickAdaptive(legs []LegInfo) RotationAction {
	e.adaptTick++

	for k := range e.adaptSeen {
		delete(e.adaptSeen, k)
	}
	for k := range e.adaptAliveIdx {
		delete(e.adaptAliveIdx, k)
	}
	var rawTotal float64
	aliveCount := 0
	standbyCount := 0
	oldestAliveIdx := -1
	newestAliveIdx := -1
	promotableIdx := -1
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
		if oldestAliveIdx == -1 || l.Index < oldestAliveIdx {
			oldestAliveIdx = l.Index
		}
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
		if prev, ok := e.adaptPrevRecv[tid]; ok && l.RecvBytes >= prev {
			rawTotal += float64(l.RecvBytes - prev)
		}
		e.adaptPrevRecv[tid] = l.RecvBytes
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

	if aliveCount == 0 {
		if promotableIdx >= 0 {
			return RotationAction{PromoteFromStandby: []int{promotableIdx}}
		}
		return RotationAction{}
	}
	if aliveCount < adaptFloor {
		if promotableIdx >= 0 {
			return RotationAction{PromoteFromStandby: []int{promotableIdx}}
		}
		return RotationAction{AddLeg: true}
	}

	probeInFlight := e.adaptProbePending || e.adaptProbeTID != ""
	if !probeInFlight {
		if idx, hops, ok := e.adaptWorstOutlier(legs); ok {
			if promotableIdx >= 0 {
				return RotationAction{
					PromoteFromStandby: []int{promotableIdx},
					DemoteToStandby:    []int{idx},
				}
			}
			return RotationAction{
				DropLegs:    []int{idx},
				AddLeg:      true,
				ExcludeHops: hops,
			}
		}
		// Proactive warm standby: once no leg is a latency outlier and we're not
		// under load, hold the surplus above the steady active target as warm
		// standby — established, kept alive, ready for instant (dip-free)
		// promotion — WITHOUT waiting for an idle signal. The asymmetric decide
		// seeds adaptRevActive+adaptStandbyMax reverse legs; the router brings
		// them all up active, and this parks the surplus down to adaptTarget on
		// the first ticks. Park the NEWEST surplus leg (highest index) — never
		// leg 0, the primary/forward leg the router refuses to demote
		// (route_mux.setLegStandby). Suppressed while saturated so a burst keeps
		// its full width (the saturated branch below instead GROWS the target).
		if !saturated && aliveCount > e.adaptTarget && standbyCount < adaptStandbyMax && newestAliveIdx > 0 {
			return RotationAction{DemoteToStandby: []int{newestAliveIdx}}
		}
		if saturated && aliveCount < adaptCap {
			e.adaptTarget = aliveCount + 1
			if e.adaptTarget > adaptCap {
				e.adaptTarget = adaptCap
			}
			if promotableIdx >= 0 {
				return RotationAction{PromoteFromStandby: []int{promotableIdx}}
			}
			return RotationAction{AddLeg: true}
		}
		if e.adaptIdleCount >= 2 && aliveCount > adaptFloor {
			e.adaptIdleCount = 0
			e.adaptTarget = aliveCount - 1
			if e.adaptTarget < adaptFloor {
				e.adaptTarget = adaptFloor
			}
			if standbyCount < adaptStandbyMax {
				return RotationAction{DemoteToStandby: []int{oldestAliveIdx}}
			}
			return RotationAction{DropLegs: []int{oldestAliveIdx}}
		}
	}

	sets := legSets{
		activeCount:  aliveCount,
		standbyCount: standbyCount,
		oldestActive: oldestAliveIdx,
		promotable:   promotableIdx,
	}
	return e.adaptExplore(sets)
}

func (e *Engine) adaptWorstOutlier(legs []LegInfo) (int, []string, bool) {
	worstIdx := -1
	worstSm := -1.0
	var worstHops []string
	var smoothed []float64
	for _, l := range legs {
		if !l.Alive || l.Standby {
			continue
		}
		sm, ok := e.adaptLatEWMA[l.TransportID]
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

func (e *Engine) adaptExplore(sets legSets) RotationAction {
	aliveCount := sets.activeCount
	if e.adaptProbePending {
		e.adaptProbePending = false
		for tid := range e.adaptAliveIdx {
			if !e.adaptPrevKnown[tid] {
				e.adaptProbeTID = tid
				e.adaptProbeAge = 0
				break
			}
		}
	}

	if e.adaptProbeTID != "" {
		idx, alive := e.adaptAliveIdx[e.adaptProbeTID]
		if !alive {
			e.adaptProbeTID = ""
			e.adaptProbeAge = 0
			return RotationAction{}
		}
		e.adaptProbeAge++
		if e.adaptProbeAge < adaptObserve {
			return RotationAction{}
		}
		probeSm, okProbe := e.adaptLatEWMA[e.adaptProbeTID]
		worstIdx := -1
		worstSm := -1.0
		for tid, i := range e.adaptAliveIdx {
			if tid == e.adaptProbeTID {
				continue
			}
			sm, ok := e.adaptLatEWMA[tid]
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
		e.adaptProbeTID = ""
		if probeSm < worstSm {
			return shedActive(sets, worstIdx)
		}
		return RotationAction{DropLegs: []int{idx}}
	}

	if e.adaptTick%adaptExploreEvery == 0 && aliveCount == e.adaptTarget {
		for k := range e.adaptPrevKnown {
			delete(e.adaptPrevKnown, k)
		}
		for tid := range e.adaptAliveIdx {
			e.adaptPrevKnown[tid] = true
		}
		e.adaptProbePending = true
		return RotationAction{AddLeg: true}
	}
	return RotationAction{}
}
