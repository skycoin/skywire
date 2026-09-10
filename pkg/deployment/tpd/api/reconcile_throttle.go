// Package api pkg/deployment/tpd/api/reconcile_throttle.go c4-net-discovery
package api

import (
	"hash/fnv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/transport"
)

// Every visor republishes its whole transport list with every CXO
// heartbeat (45 s), and both edges of a transport publish it, so the
// aggregator hands each live transport to ReconcileTransportsFromCXO
// roughly every 20 s. Registering it again each time is nine redis writes
// (the tp blob, five index SADDs, two EXPIREs, the edge pair) for a 5 min
// TTL that only needs refreshing a couple of times per period, and the
// per-transport heartbeat is another eight. On the TPD host that was
// ~460k of redis's 640k commands a minute (2026-09-10).
//
// reconcileThrottle remembers, per transport, what was last written and
// when: an unchanged entry is re-registered only once refreshGap has
// passed (a third of the TTL by default, so a refresh is never missed),
// and its heartbeat recorded at most once per heartbeatGap.

const (
	// defaultReconcileRefreshGap is used until SetEntryTimeout is called.
	// A third of the default 5 min entry TTL.
	defaultReconcileRefreshGap = 100 * time.Second
	// reconcileHeartbeatGap dedupes the two edges' reports of one transport.
	// The legacy uptime count expects a heartbeat every ~90 s (960/day, see
	// store.expectedHeartbeatsPerDay); 30 s keeps every 45 s report of at
	// least one edge, so a transport still lands ≥1920/day and reads 100%.
	reconcileHeartbeatGap = 30 * time.Second
)

type reconcileMark struct {
	fp           uint64
	registeredAt time.Time
	heartbeatAt  time.Time
}

type reconcileThrottle struct {
	mu           sync.Mutex
	refreshGap   time.Duration
	heartbeatGap time.Duration
	marks        map[uuid.UUID]*reconcileMark
	sweptAt      time.Time
}

func newReconcileThrottle(refreshGap, heartbeatGap time.Duration) *reconcileThrottle {
	return &reconcileThrottle{
		refreshGap:   refreshGap,
		heartbeatGap: heartbeatGap,
		marks:        make(map[uuid.UUID]*reconcileMark),
	}
}

// setRefreshGap adjusts the registration gap (from the configured TTL).
func (t *reconcileThrottle) setRefreshGap(d time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.refreshGap = d
}

// entryFingerprint covers everything the store persists for a transport.
func entryFingerprint(e *transport.Entry) uint64 {
	h := fnv.New64a()
	h.Write(e.Edges[0][:])   //nolint:errcheck,gosec
	h.Write(e.Edges[1][:])   //nolint:errcheck,gosec
	h.Write([]byte(e.Type))  //nolint:errcheck,gosec
	h.Write([]byte{0})       //nolint:errcheck,gosec
	h.Write([]byte(e.Label)) //nolint:errcheck,gosec
	return h.Sum64()
}

// plan splits entries into those that must be (re)registered now and those
// whose heartbeat is due, marking both as done as of now. A registration
// that then fails must be handed back through forget so it is retried on
// the next snapshot.
func (t *reconcileThrottle) plan(now time.Time, entries []*transport.Entry) (register, heartbeat []*transport.Entry) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sweepLocked(now)
	for _, e := range entries {
		fp := entryFingerprint(e)
		m := t.marks[e.ID]
		if m == nil {
			m = &reconcileMark{}
			t.marks[e.ID] = m
		}
		if m.fp != fp || now.Sub(m.registeredAt) >= t.refreshGap {
			m.fp, m.registeredAt = fp, now
			register = append(register, e)
		}
		if now.Sub(m.heartbeatAt) >= t.heartbeatGap {
			m.heartbeatAt = now
			heartbeat = append(heartbeat, e)
		}
	}
	return register, heartbeat
}

// forget clears the registration mark of entries whose write failed.
func (t *reconcileThrottle) forget(entries []*transport.Entry) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, e := range entries {
		if m := t.marks[e.ID]; m != nil {
			m.registeredAt = time.Time{}
		}
	}
}

// sweepLocked drops marks of transports not seen for a few refresh gaps,
// so the map tracks live transports rather than every one ever reported.
func (t *reconcileThrottle) sweepLocked(now time.Time) {
	if now.Sub(t.sweptAt) < t.refreshGap {
		return
	}
	t.sweptAt = now
	stale := 4 * t.refreshGap
	for id, m := range t.marks {
		last := m.registeredAt
		if m.heartbeatAt.After(last) {
			last = m.heartbeatAt
		}
		if now.Sub(last) > stale {
			delete(t.marks, id)
		}
	}
}
