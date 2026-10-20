package transport

import (
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// DialFailure is one outbound transport dial that failed. The events ring
// only holds transports that opened, so a dial that never completed left
// nothing behind but a debug log line.
type DialFailure struct {
	At     time.Time     `json:"at"`
	Type   string        `json:"type"`
	Remote cipher.PubKey `json:"remote"`
	Label  Label         `json:"label"`
	Error  string        `json:"error"`
	// DurS is how long the dial ran before it failed, which tells a timeout
	// from a refusal.
	DurS float64 `json:"dur_s"`
}

// DialStat counts the outbound dials of one transport type since start.
type DialStat struct {
	Attempts    uint64    `json:"attempts"`
	OK          uint64    `json:"ok"`
	Failed      uint64    `json:"failed"`
	LastError   string    `json:"last_error,omitempty"`
	LastErrorAt time.Time `json:"last_error_at,omitzero"`
}

// DialFailureRingSize is how many failed dials the manager keeps. They have
// their own ring so a burst of failures cannot push opens and closes out.
const DialFailureRingSize = 512

type dialRecorder struct {
	mu    sync.Mutex
	stats map[string]*DialStat
	fails []DialFailure
	next  int
	full  bool
}

// record counts one finished dial, and keeps it when it failed.
func (r *dialRecorder) record(typ string, remote cipher.PubKey, label Label, start time.Time, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stats == nil {
		r.stats = make(map[string]*DialStat)
	}
	s := r.stats[typ]
	if s == nil {
		s = &DialStat{}
		r.stats[typ] = s
	}
	s.Attempts++
	if err == nil {
		s.OK++
		return
	}
	now := time.Now()
	s.Failed++
	s.LastError, s.LastErrorAt = err.Error(), now
	if r.fails == nil {
		r.fails = make([]DialFailure, DialFailureRingSize)
	}
	r.fails[r.next] = DialFailure{
		At: now, Type: typ, Remote: remote, Label: label,
		Error: err.Error(), DurS: now.Sub(start).Seconds(),
	}
	r.next = (r.next + 1) % len(r.fails)
	if r.next == 0 {
		r.full = true
	}
}

func (r *dialRecorder) snapshot() (map[string]DialStat, []DialFailure) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var stats map[string]DialStat
	if len(r.stats) > 0 {
		stats = make(map[string]DialStat, len(r.stats))
		for t, s := range r.stats {
			stats[t] = *s
		}
	}
	var fails []DialFailure
	switch {
	case r.fails == nil:
	case !r.full:
		fails = append(fails, r.fails[:r.next]...)
	default:
		fails = make([]DialFailure, 0, len(r.fails))
		fails = append(fails, r.fails[r.next:]...)
		fails = append(fails, r.fails[:r.next]...)
	}
	return stats, fails
}

// DialStats returns the outbound dial counts per transport type and the
// recent failed dials, oldest first.
func (tm *Manager) DialStats() (map[string]DialStat, []DialFailure) {
	return tm.dialLog.snapshot()
}
