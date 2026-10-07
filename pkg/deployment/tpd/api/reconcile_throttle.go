// Package api pkg/deployment/tpd/api/reconcile_throttle.go c4-net-discovery
package api

import (
	"hash/fnv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
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
// when: a new or changed entry is written in full; an unchanged one only has
// its lifetime extended (EXPIRE), once refreshGap has passed (a third of the
// TTL, so a refresh is never missed) — the reporter's live subscription is
// what keeps it; and its heartbeat is recorded at most once per heartbeatGap.

const (
	// defaultReconcileRefreshGap is used until SetEntryTimeout is called.
	// A third of the default 5 min entry TTL.
	defaultReconcileRefreshGap = 100 * time.Second
	// reconcileHeartbeatGap dedupes the two edges' reports of one transport
	// before they reach the store, which in turn writes at most one heartbeat
	// per 5-minute timeline slot (store/transport_beat_memo.go).
	reconcileHeartbeatGap = 30 * time.Second
)

type reconcileMark struct {
	fp           uint64
	registeredAt time.Time
	heartbeatAt  time.Time
	// listedAt is when each edge (by EdgeIndex) last listed the transport
	// in a snapshot; zero once that edge's snapshot omits it.
	listedAt [2]time.Time
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

// entryFingerprint covers what identifies a transport: its edges and type.
//
// Not the label. Each edge labels a transport for itself — the dialer
// "automatic" (autoconnect) or "user", the acceptor always "user" — so the
// two edges' snapshots disagree on it for nearly every transport. With the
// label in the fingerprint every snapshot from one edge looked like a change
// to the other's, and the transport was re-registered (9 redis commands) on
// each report instead of once per refreshGap: ~2k registrations a second on
// prod01 (2026-09-30). Which edge's label the store keeps was already
// last-writer-wins.
func entryFingerprint(e *transport.Entry) uint64 {
	h := fnv.New64a()
	h.Write(e.Edges[0][:])  //nolint:errcheck,gosec
	h.Write(e.Edges[1][:])  //nolint:errcheck,gosec
	h.Write([]byte(e.Type)) //nolint:errcheck,gosec
	return h.Sum64()
}

// plan splits entries into those that must be written now (new, changed, or
// forgotten after a failed write or a delete), those whose registration only
// needs its lifetime extended (unchanged, refresh due: a touch, not a
// rewrite), and those whose heartbeat is due — marking all as done as of now.
// A write that then fails must be handed back through forget so it is retried
// on the next snapshot.
func (t *reconcileThrottle) plan(now time.Time, entries []*transport.Entry) (register, touch, heartbeat []*transport.Entry) {
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
		switch {
		case m.fp != fp || m.registeredAt.IsZero():
			m.fp, m.registeredAt = fp, now
			register = append(register, e)
		case now.Sub(m.registeredAt) >= t.refreshGap:
			m.registeredAt = now
			touch = append(touch, e)
		}
		if now.Sub(m.heartbeatAt) >= t.heartbeatGap {
			m.heartbeatAt = now
			heartbeat = append(heartbeat, e)
		}
	}
	return register, touch, heartbeat
}

// listed records that reporter's snapshot lists entries (after plan, which
// made their marks).
func (t *reconcileThrottle) listed(now time.Time, reporter cipher.PubKey, entries []*transport.Entry) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, e := range entries {
		if m, i := t.marks[e.ID], e.EdgeIndex(reporter); m != nil && i >= 0 {
			m.listedAt[i] = now
		}
	}
}

// listedByOther reports whether the edge of e that is not reporter listed it
// within the refresh gap — more than two of its 45 s republishes — and
// records that reporter no longer does. A transport one edge omits while
// the other still lists it is kept: the two edges' lists are published
// apart, so one lags the other (a just-dialed transport, a just-closed
// one), and deleting on that lag only had the other edge's next snapshot
// put the transport straight back, a flap every downstream feed paid for.
// Once neither edge lists it, it is deleted.
func (t *reconcileThrottle) listedByOther(now time.Time, reporter cipher.PubKey, e *transport.Entry) bool {
	i := e.EdgeIndex(reporter)
	if i < 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	m := t.marks[e.ID]
	if m == nil {
		return false
	}
	m.listedAt[i] = time.Time{}
	other := m.listedAt[1-i]
	return !other.IsZero() && now.Sub(other) < t.refreshGap
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

// forgetID clears the registration mark of a transport deleted outside the
// reconcile (an HTTP delete, a CXO tombstone). The mark is shared by both
// edges; left set, the other edge's next snapshots would only touch an entry
// that no longer exists.
func (t *reconcileThrottle) forgetID(id uuid.UUID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if m := t.marks[id]; m != nil {
		m.registeredAt = time.Time{}
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

// seed marks entries already in the store as registered one refreshGap ago,
// so after a restart their first report extends their lifetime (a touch)
// rather than rewriting them. Without it every restart re-registered the
// whole mesh, nine redis writes per transport (~86k transports on prod01).
func (t *reconcileThrottle) seed(now time.Time, entries []*transport.Entry) {
	t.mu.Lock()
	defer t.mu.Unlock()
	at := now.Add(-t.refreshGap)
	for _, e := range entries {
		if e == nil {
			continue
		}
		if _, ok := t.marks[e.ID]; !ok {
			t.marks[e.ID] = &reconcileMark{fp: entryFingerprint(e), registeredAt: at}
		}
	}
}
