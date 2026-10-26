// Package api pkg/deployment/tpd/api/routing_smoothing.go c4-net-discovery
package api

import (
	"math"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// Routing smoothing defaults. A 10-minute feed with a 6-publish window
// takes the minimum latency of the last hour.
const (
	DefaultRoutingLatencyWindow     = 6
	DefaultRoutingSmoothHysteresis  = 0.25
	routingSmoothForgetAfterMissing = DefaultRoutingLatencyWindow
)

// RoutingSmoothing sets how the routing feed steadies the figures TPD holds,
// which are the last value a visor reported.
type RoutingSmoothing struct {
	// LatencyWindow is how many publishes the published latency is the
	// minimum of. The minimum is the path's propagation delay; samples above
	// it are queueing on a busy hop. Zero means DefaultRoutingLatencyWindow.
	LatencyWindow int
	// Hysteresis is the relative change a figure must make before the feed
	// publishes it. Zero means DefaultRoutingSmoothHysteresis; a negative
	// value publishes every change.
	Hysteresis float64
}

func (s RoutingSmoothing) withDefaults() RoutingSmoothing {
	if s.LatencyWindow <= 0 {
		s.LatencyWindow = DefaultRoutingLatencyWindow
	}
	if s.Hysteresis == 0 {
		s.Hysteresis = DefaultRoutingSmoothHysteresis
	}
	if s.Hysteresis < 0 {
		s.Hysteresis = 0
	}
	return s
}

type smoothKey struct {
	edges [2]cipher.PubKey
	typ   types.Type
}

type smoothState struct {
	latencies  []float64 // ring of the last LatencyWindow reports, 0 = none
	next       int
	latency    float64 // published latency
	throughput float64 // published throughput
	missing    int     // publishes in a row the transport was absent
}

// routingSmoother keeps per-transport state between publishes. Only the
// publish loop uses it.
type routingSmoother struct {
	cfg   RoutingSmoothing
	state map[smoothKey]*smoothState
}

func newRoutingSmoother(cfg RoutingSmoothing) *routingSmoother {
	return &routingSmoother{cfg: cfg.withDefaults(), state: make(map[smoothKey]*smoothState)}
}

// apply returns copies of entries with their latency and throughput
// replaced by the smoothed figures, and forgets transports absent for a
// while. A transport that leaves and returns within that time keeps its
// window.
func (s *routingSmoother) apply(entries []*transport.Entry) []*transport.Entry {
	seen := make(map[smoothKey]bool, len(entries))
	out := make([]*transport.Entry, 0, len(entries))
	for _, e := range entries {
		if e == nil {
			continue
		}
		k := smoothKey{edges: transport.SortEdges(e.Edges[0], e.Edges[1]), typ: e.Type}
		seen[k] = true
		st := s.state[k]
		if st == nil {
			st = &smoothState{latencies: make([]float64, s.cfg.LatencyWindow)}
			s.state[k] = st
		}
		st.missing = 0
		st.latencies[st.next] = e.Latency
		st.next = (st.next + 1) % len(st.latencies)
		st.latency = s.settle(st.latency, minPositive(st.latencies))
		st.throughput = s.settle(st.throughput, e.ThroughputBps)

		c := *e
		c.Latency, c.ThroughputBps = st.latency, st.throughput
		out = append(out, &c)
	}
	for k, st := range s.state {
		if seen[k] {
			continue
		}
		if st.missing++; st.missing >= routingSmoothForgetAfterMissing {
			delete(s.state, k)
		}
	}
	return out
}

// settle returns the figure to publish: the candidate when nothing is
// published yet, or when it differs from the published figure by more than
// the hysteresis; otherwise the published figure.
func (s *routingSmoother) settle(published, candidate float64) float64 {
	if published == 0 || candidate == 0 {
		return candidate
	}
	if math.Abs(candidate-published) > s.cfg.Hysteresis*published {
		return candidate
	}
	return published
}

func minPositive(vs []float64) float64 {
	m := 0.0
	for _, v := range vs {
		if v > 0 && (m == 0 || v < m) {
			m = v
		}
	}
	return m
}
