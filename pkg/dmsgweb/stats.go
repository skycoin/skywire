// Package dmsgweb pkg/dmsgweb/stats.go
//
// Stats collects per-resolver request counters and surfaces them to
// consumers via Snapshot(). Intentionally lightweight — atomic
// counters on the hot path, a short mutex-protected last-error
// window so the hypervisor UI can display recent failures without
// hitting the visor logs.
//
// Stats is opt-in: when Config.Stats is nil the runtime skips the
// bookkeeping entirely, so zero overhead for callers that don't
// care. The visor layer always wires it up so `skywire cli visor
// proxies --json` can pipe straight to UI widgets.
package dmsgweb

import (
	"sync"
	"sync/atomic"
	"time"
)

// Stats is the public stats container. Safe for concurrent use.
type Stats struct {
	// Hot-path atomics.
	total   atomic.Uint64
	success atomic.Uint64
	failed  atomic.Uint64
	active  atomic.Int64
	started atomic.Int64 // unix nanos; 0 if never started

	// Cold path — last-error window. Small enough to hold under a
	// single mutex without contention.
	mu            sync.Mutex
	lastReqAt     time.Time
	lastSuccessAt time.Time
	lastFailureAt time.Time
	lastErrorMsg  string
}

// StatsSnapshot is the read-only view exposed to RPC / UI layers.
// Safe to marshal.
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

// NewStats returns a zero-valued Stats with its "started at" clock
// set to now. Callers typically allocate one per resolver lifetime
// and pass it to every Config, so cumulative counters survive
// Start/Stop cycles.
func NewStats() *Stats {
	s := &Stats{}
	s.started.Store(time.Now().UnixNano())
	return s
}

// RecordRequest begins tracking a new request. Returns a closure
// that the caller defers to record completion; pass in the request
// error (nil on success). Pattern:
//
//	done := stats.RecordRequest()
//	// ... do the work ...
//	done(err)
//
// Nil receiver is a no-op, so callers can unconditionally call it
// without checking for nil Stats.
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
