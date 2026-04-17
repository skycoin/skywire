// Package skynetweb pkg/skynetweb/stats.go
//
// Mirror of pkg/dmsgweb/stats.go. Structurally identical so the UI
// layer can treat both resolvers' stats uniformly. See dmsgweb/stats.go
// for design notes — duplicated rather than factored into a shared
// package because it's ~60 lines and avoids cross-package coupling
// between the two resolvers.
package skynetweb

import (
	"sync"
	"sync/atomic"
	"time"
)

// Stats collects per-resolver counters. Safe for concurrent use.
// Nil receiver methods are no-ops so callers don't need nil-guards.
type Stats struct {
	total   atomic.Uint64
	success atomic.Uint64
	failed  atomic.Uint64
	active  atomic.Int64
	started atomic.Int64

	mu            sync.Mutex
	lastReqAt     time.Time
	lastSuccessAt time.Time
	lastFailureAt time.Time
	lastErrorMsg  string
}

// StatsSnapshot is the read-only view.
type StatsSnapshot struct {
	StartedAt     time.Time  `json:"started_at,omitempty"`
	UptimeSec     int64      `json:"uptime_sec,omitempty"`
	TotalRequests uint64     `json:"total_requests"`
	Successful    uint64     `json:"successful"`
	Failed        uint64     `json:"failed"`
	Active        int64      `json:"active"`
	LastRequestAt *time.Time `json:"last_request_at,omitempty"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	LastFailureAt *time.Time `json:"last_failure_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
}

// NewStats returns a zero-valued Stats with its started_at clock set.
func NewStats() *Stats {
	s := &Stats{}
	s.started.Store(time.Now().UnixNano())
	return s
}

// RecordRequest — see dmsgweb.Stats.RecordRequest.
func (s *Stats) RecordRequest() func(err error) {
	if s == nil {
		return func(error) {}
	}
	s.total.Add(1)
	s.active.Add(1)
	s.mu.Lock()
	s.lastReqAt = time.Now()
	s.mu.Unlock()

	return func(err error) {
		s.active.Add(-1)
		now := time.Now()
		s.mu.Lock()
		defer s.mu.Unlock()
		if err == nil {
			s.success.Add(1)
			s.lastSuccessAt = now
			return
		}
		s.failed.Add(1)
		s.lastFailureAt = now
		msg := err.Error()
		if len(msg) > 512 {
			msg = msg[:509] + "..."
		}
		s.lastErrorMsg = msg
	}
}

// Snapshot returns a stable point-in-time copy.
func (s *Stats) Snapshot() StatsSnapshot {
	if s == nil {
		return StatsSnapshot{}
	}
	snap := StatsSnapshot{
		TotalRequests: s.total.Load(),
		Successful:    s.success.Load(),
		Failed:        s.failed.Load(),
		Active:        s.active.Load(),
	}
	if st := s.started.Load(); st != 0 {
		t := time.Unix(0, st)
		snap.StartedAt = t
		snap.UptimeSec = int64(time.Since(t).Seconds())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.lastReqAt.IsZero() {
		t := s.lastReqAt
		snap.LastRequestAt = &t
	}
	if !s.lastSuccessAt.IsZero() {
		t := s.lastSuccessAt
		snap.LastSuccessAt = &t
	}
	if !s.lastFailureAt.IsZero() {
		t := s.lastFailureAt
		snap.LastFailureAt = &t
	}
	snap.LastError = s.lastErrorMsg
	return snap
}
