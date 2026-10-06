// Package api pkg/deployment/rf/api/stats.go
package api

import (
	"sync"
	"time"
)

// maxCountedHops is the last hop bucket; longer routes count there.
const maxCountedHops = 6

// requestStats counts route requests since the finder started.
type requestStats struct {
	mu       sync.Mutex
	requests uint64
	pairs    uint64
	noRoute  uint64
	failed   uint64
	hops     [maxCountedHops + 1]uint64
	elapsed  time.Duration
}

func (s *requestStats) request(pairs int, elapsed time.Duration) {
	s.mu.Lock()
	s.requests++
	s.pairs += uint64(pairs) //nolint:gosec
	s.elapsed += elapsed
	s.mu.Unlock()
}

// route counts the hops of the best route found for a pair.
func (s *requestStats) route(hops int) {
	if hops > maxCountedHops {
		hops = maxCountedHops
	}
	if hops < 0 {
		hops = 0
	}
	s.mu.Lock()
	s.hops[hops]++
	s.mu.Unlock()
}

func (s *requestStats) miss()  { s.add(&s.noRoute) }
func (s *requestStats) error() { s.add(&s.failed) }

func (s *requestStats) add(n *uint64) {
	s.mu.Lock()
	*n++
	s.mu.Unlock()
}

func (s *requestStats) snapshot() requestStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return requestStats{requests: s.requests, pairs: s.pairs, noRoute: s.noRoute, failed: s.failed, hops: s.hops, elapsed: s.elapsed}
}
