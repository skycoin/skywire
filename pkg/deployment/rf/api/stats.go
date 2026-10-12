// Package api pkg/deployment/rf/api/stats.go
package api

import (
	"sort"
	"sync"
	"time"
)

// maxCountedHops is the last hop bucket; longer routes count there.
const maxCountedHops = 6

// maxCountedRoutes is the last bucket of routes found per pair.
const maxCountedRoutes = 6

// requestStats counts route requests since the finder started.
type requestStats struct {
	mu       sync.Mutex
	requests uint64
	pairs    uint64
	noRoute  uint64
	failed   uint64
	hops     [maxCountedHops + 1]uint64
	// perPair counts pairs by how many routes were found for them.
	perPair [maxCountedRoutes + 1]uint64
	elapsed time.Duration
	// times are the search times since the last takeTimes, in ms.
	times []float64
}

// maxSearchTimes bounds the search times kept between samples.
const maxSearchTimes = 4096

func (s *requestStats) request(pairs int, elapsed time.Duration) {
	s.mu.Lock()
	s.requests++
	s.pairs += uint64(pairs) //nolint:gosec
	s.elapsed += elapsed
	if len(s.times) < maxSearchTimes {
		s.times = append(s.times, float64(elapsed)/float64(time.Millisecond))
	}
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

// takeTimes returns the search times since the last call, sorted.
func (s *requestStats) takeTimes() []float64 {
	s.mu.Lock()
	t := s.times
	s.times = nil
	s.mu.Unlock()
	sort.Float64s(t)
	return t
}

func (s *requestStats) snapshot() requestStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return requestStats{requests: s.requests, pairs: s.pairs, noRoute: s.noRoute, failed: s.failed, hops: s.hops, perPair: s.perPair, elapsed: s.elapsed}
}

// found counts the routes returned for one pair: how many, and the hops of
// each.
func (s *requestStats) found(hops []int) {
	if len(hops) == 0 {
		return
	}
	n := len(hops)
	if n > maxCountedRoutes {
		n = maxCountedRoutes
	}
	s.mu.Lock()
	s.perPair[n]++
	s.mu.Unlock()
	for _, h := range hops {
		s.route(h)
	}
}
