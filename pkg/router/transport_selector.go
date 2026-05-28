// Package router pkg/router/transport_selector.go
package router

import (
	"sync"
	"sync/atomic"

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
)

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
	// smallLegSchedule holds the round-robin schedule across the
	// non-leg-0 transports used by WeightModeSizeThreshold when
	// the packet is at or under the threshold. Built at Rebuild
	// time so per-packet selection stays lock-free.
	smallLegSchedule []int
	smallLegCounter  uint32
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

// SetSizeThreshold stores the payload-size boundary used by
// WeightModeSizeThreshold. Caller must call Rebuild afterwards
// (or SetMode + Rebuild) for it to take effect.
func (ts *transportSelector) SetSizeThreshold(n int) {
	ts.mu.Lock()
	ts.sizeThreshold = n
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
