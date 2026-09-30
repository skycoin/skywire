// Package store pkg/deployment/tpd/store/transport_beat_memo.go c4-net-discovery
package store

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// beatMemoSweepEvery is how often slots from earlier days are dropped.
const beatMemoSweepEvery = 10 * time.Minute

// beatSlot identifies one 5-minute timeline slot: the UTC epoch day and the
// slot within it.
type beatSlot struct {
	day  int64
	slot int64
}

// transportBeatMemo remembers the last timeline slot each transport's
// heartbeat was written for. Transport uptime is the timeline — one bit per
// 5-minute slot — so a second heartbeat in the same slot writes nothing new.
// Three paths report transport heartbeats (the CXO reconcile, the HTTP
// register and the telemetry ingest), together every ~20 s per transport;
// the memo turns that into one write per slot. The zero value is ready.
type transportBeatMemo struct {
	mu        sync.Mutex
	last      map[uuid.UUID]beatSlot
	lastSweep time.Time
}

func slotOf(t time.Time) beatSlot {
	t = t.UTC()
	return beatSlot{day: epochDay(t), slot: currentTimelineSlot(t)}
}

// recorded reports whether id's heartbeat for the slot of at is already
// written.
func (m *transportBeatMemo) recorded(id uuid.UUID, at time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.last[id]
	return ok && s == slotOf(at)
}

// note records that id's heartbeat for the slot of at was written.
func (m *transportBeatMemo) note(id uuid.UUID, at time.Time) {
	s := slotOf(at)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.last == nil {
		m.last = make(map[uuid.UUID]beatSlot)
	}
	m.last[id] = s
	if at.Sub(m.lastSweep) >= beatMemoSweepEvery {
		m.lastSweep = at
		for k, v := range m.last {
			if v.day < s.day {
				delete(m.last, k)
			}
		}
	}
}
