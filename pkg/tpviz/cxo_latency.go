// Package tpviz pkg/tpviz/cxo_latency.go c4-vis-latency
//
// The latency graph behind the latency-space view: an undirected,
// weighted graph over visor public keys where an edge weight is the
// measured round-trip time between the two visors.
//
// Source is TPD's metrics CXO feed (metrics/days/<n>), NOT the HTTP
// /metrics endpoint. The publisher writes []TransportMetric there every
// 60s and content-addressing means a subscriber pays the delta rather
// than the whole dataset — 85k transport rows is ~31MB over HTTP, and
// re-fetching that to move a few edges is what the feed exists to avoid.
// There is deliberately no HTTP fallback: a view that silently starts
// polling a rate-limited endpoint when the feed is cold is worse than a
// view that says the feed is cold.
package tpviz

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
)

// latencyMetricsDays is the day window read from the feed. The publisher
// writes 1, 7 and 30; the shortest is the most representative of the
// network as it is now, which is what a positional embedding wants.
const latencyMetricsDays = 1

// cxoTransportMetric is the decoding shape of store.TransportMetric,
// narrowed to the fields this view needs. Declared locally rather than
// imported so tpviz does not take a dependency on the TPD store package
// for two fields.
type cxoTransportMetric struct {
	Type  string   `json:"type"`
	Live  bool     `json:"live"`
	Edges []string `json:"edges"`
	// Latency is in MICROSECONDS, as published.
	Latency *struct {
		Min float64 `json:"min"`
		Max float64 `json:"max"`
		Avg float64 `json:"avg"`
	} `json:"latency"`
}

// LatencyEdge is one visor pair and the round-trip time between them.
type LatencyEdge struct {
	A string `json:"a"`
	B string `json:"b"`
	// MS is the round-trip time in milliseconds.
	MS float64 `json:"ms"`
	// N is how many transports between this pair were sampled.
	N int `json:"n"`
	// Type is the transport type that produced MS (the fastest one).
	Type string `json:"type"`
}

// LatencyGraph is what the view consumes.
type LatencyGraph struct {
	Edges []LatencyEdge `json:"edges"`
	// Visors is every public key that appears in Edges, sorted, so the
	// client can index points without a second pass.
	Visors []string `json:"visors"`
	Days   int      `json:"days"`
}

// tryCXOLatency builds the latency graph from the TPD metrics feed.
//
// Returns ok=false when the manager is not installed, the feed is not
// subscribed, or no leaf has arrived yet. The caller reports that state
// rather than reaching for HTTP.
func (s *Server) tryCXOLatency() (*LatencyGraph, bool) {
	mgr := s.cxoMgr()
	if mgr == nil {
		return nil, false
	}
	mgr.AcquireForTab(CXOTabNetworkVisualizer)
	defer mgr.ReleaseForTab(CXOTabNetworkVisualizer)

	// best[pair] keeps the LOWEST observed RTT for a visor pair. Several
	// transports commonly join the same two visors over different network
	// types; the pair's distance is the best path between them, not an
	// average dragged up by a slow one.
	type agg struct {
		ms    float64
		n     int
		tType string
	}
	best := make(map[[2]string]*agg)

	prefix := fmt.Sprintf("metrics/days/%d", latencyMetricsDays)
	ok := mgr.Walk(CXOFeedTPDMetrics, prefix, func(_ string, body []byte) bool {
		var metrics []cxoTransportMetric
		// The publisher gzips; Gunzip passes a raw body through unchanged. A
		// long window arrives as several "<prefix>/part/<NNNN>" leaves, which
		// this prefix Walk already visits — taking the minimum per visor pair
		// is the same answer whether the records came in one body or several.
		if err := json.Unmarshal(cxoutils.Gunzip(body), &metrics); err != nil {
			return true
		}
		for i := range metrics {
			m := &metrics[i]
			if m.Latency == nil || len(m.Edges) != 2 {
				continue
			}
			// A zero or negative average is an unmeasured transport, not a
			// zero-latency one.
			if m.Latency.Avg <= 0 {
				continue
			}
			a, b := m.Edges[0], m.Edges[1]
			if a == b {
				continue
			}
			if a > b {
				a, b = b, a
			}
			ms := m.Latency.Avg / 1000 // microseconds as published
			k := [2]string{a, b}
			cur, seen := best[k]
			if !seen {
				best[k] = &agg{ms: ms, n: 1, tType: m.Type}
				continue
			}
			cur.n++
			if ms < cur.ms {
				cur.ms, cur.tType = ms, m.Type
			}
		}
		return true
	})
	if !ok || len(best) == 0 {
		return nil, false
	}

	g := &LatencyGraph{Days: latencyMetricsDays, Edges: make([]LatencyEdge, 0, len(best))}
	seen := make(map[string]struct{})
	for k, v := range best {
		g.Edges = append(g.Edges, LatencyEdge{A: k[0], B: k[1], MS: v.ms, N: v.n, Type: v.tType})
		seen[k[0]] = struct{}{}
		seen[k[1]] = struct{}{}
	}
	// Deterministic order so the client's point indices are stable across
	// refreshes and the embedding does not jump when nothing changed.
	sort.Slice(g.Edges, func(i, j int) bool {
		if g.Edges[i].A != g.Edges[j].A {
			return g.Edges[i].A < g.Edges[j].A
		}
		return g.Edges[i].B < g.Edges[j].B
	})
	g.Visors = make([]string, 0, len(seen))
	for pk := range seen {
		g.Visors = append(g.Visors, pk)
	}
	sort.Strings(g.Visors)
	return g, true
}

// handleLatency serves the latency graph for the latency-space view.
//
// CXO only. When the feed has not delivered, this reports that state
// with 503 and a reason rather than falling back to TPD's HTTP /metrics:
// that endpoint rate-limits at 30 requests a minute and the full
// response is ~31MB, so a view that quietly polled it would be a worse
// failure than a view that says the feed is cold.
func (s *Server) handleLatency(w http.ResponseWriter, r *http.Request) {
	g, ok := s.tryCXOLatency()
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck,gosec // response already committed
			"error": "the TPD metrics CXO feed has no data yet",
			"hint":  "the subscription is acquired while the visualizer tab is open; the publisher writes every 60s",
		})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(g) //nolint:errcheck,gosec // response already committed
}
