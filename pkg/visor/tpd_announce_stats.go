// Package visor pkg/visor/tpd_announce_stats.go c3-vis-core
package visor

import (
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// tpdAnnounceStats records the announces of the feed that carries the
// transport list, for `visor state`. Its zero value is ready to use.
type tpdAnnounceStats struct {
	mu       sync.Mutex
	to       cipher.PubKey
	ok       int64
	failed   int64
	lastOK   time.Time
	lastFail time.Time
	lastErr  string
}

func (s *tpdAnnounceStats) record(to cipher.PubKey, err error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.to = to
	if err != nil {
		s.failed++
		s.lastFail = time.Now()
		s.lastErr = err.Error()
		return
	}
	s.ok++
	s.lastOK = time.Now()
}

func secsSince(t time.Time) float64 {
	if t.IsZero() {
		return -1
	}
	return time.Since(t).Seconds()
}

// state is nil until the first announce, so a visor that never announces
// (no dmsg:// discovery URL) is told apart from one whose announces fail.
func (s *tpdAnnounceStats) state() *visorapi.TPDAnnounceState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ok == 0 && s.failed == 0 {
		return nil
	}
	return &visorapi.TPDAnnounceState{
		To: s.to.Hex(), OK: s.ok, Failed: s.failed,
		SecsSinceOK: secsSince(s.lastOK), LastError: s.lastErr, SecsSinceLastFail: secsSince(s.lastFail),
	}
}
