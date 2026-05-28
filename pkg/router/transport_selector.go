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
	if ts.mode == WeightModeSticky5Tuple || ts.mode == WeightModeLatencyAdaptive {
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
