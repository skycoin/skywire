// Package router pkg/router/transport_selector.go
package router

import (
	"sync"
	"sync/atomic"

	"github.com/skycoin/skywire/pkg/transport"
)

// transportSelector implements weighted transport selection based on latency.
// Faster transports (lower latency) get proportionally more packets.
// Falls back to equal-weight round-robin when latency data is unavailable.
type transportSelector struct {
	mu       sync.RWMutex
	schedule []int // pre-computed selection sequence of transport indices
	counter  uint32
}

func newTransportSelector() *transportSelector {
	return &transportSelector{}
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

	// Compute weight for each transport based on inverse latency.
	// Weight = 1000 / latency_ms. Unknown latency (0) gets weight 1.
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
