// Package store pkg/deployment/tpd/store/live_set.go c4-net-discovery
//
// The live transport set: TPD's transports held in memory by the store that
// writes them.
//
// Every transport TPD knows arrives through this store (the CXO snapshot
// reconcile, the HTTP register), so the writing process can keep the whole
// set — entries, latency, throughput — as it writes it, and answer the
// whole-set reads from memory. Those reads were an index SMEMBERS plus an
// MGET of every transport, latency and throughput key: the routing, metrics
// and all-transports publishers each minute, and the route finder every
// 15 s, ~80k transports each time on prod01 (2026-09-30).
//
// Only the writer can hold it: a process that does not write (a route finder
// on its own) would see nothing change, so it keeps reading redis. A route
// finder in the same process as TPD reads TPD's set through SharedLiveStore.
//
// Entries lapse after the same TTL as their redis keys, refreshed by the same
// writes (register, touch). The redis index sets used to be pruned of
// lapsed IDs by the scans this replaces, so the set's sweep prunes them.
package store

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
)

// liveSweepEvery is how often lapsed transports are dropped from the live
// set and the redis index sets.
const liveSweepEvery = time.Minute

type liveTransport struct {
	entry   transport.Entry
	expires time.Time
}

type liveValue struct {
	v  float64
	at time.Time
}

// liveSet is the in-memory transport set. Its zero value is off: every
// method is a no-op and reads report not-ready.
type liveSet struct {
	mu   sync.RWMutex
	on   bool
	ttl  time.Duration
	tps  map[uuid.UUID]*liveTransport
	edge map[cipher.PubKey]map[uuid.UUID]struct{} // transports by either edge
	lat  map[uuid.UUID]liveValue                  // latest average, ms
	tput map[uuid.UUID]liveValue                  // peak, bytes/s
}

func (l *liveSet) put(entries []*transport.Entry, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.on {
		return
	}
	for _, e := range entries {
		if e == nil {
			continue
		}
		c := *e
		c.Latency, c.ThroughputBps = 0, 0 // carried in lat/tput
		if old, ok := l.tps[e.ID]; ok && old.entry.Edges != c.Edges {
			l.unindex(&old.entry)
		}
		l.tps[e.ID] = &liveTransport{entry: c, expires: now.Add(l.ttl)}
		l.index(&c)
	}
}

func (l *liveSet) touch(ids []uuid.UUID, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.on {
		return
	}
	for _, id := range ids {
		if t, ok := l.tps[id]; ok {
			t.expires = now.Add(l.ttl)
		}
	}
}

func (l *liveSet) del(ids ...uuid.UUID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.on {
		return
	}
	for _, id := range ids {
		if t, ok := l.tps[id]; ok {
			l.unindex(&t.entry)
			delete(l.tps, id)
		}
	}
}

func (l *liveSet) setLatency(id uuid.UUID, ms float64, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.on && ms > 0 {
		l.lat[id] = liveValue{v: ms, at: now}
	}
}

// setThroughput keeps the peak, as the redis record does.
func (l *liveSet) setThroughput(id uuid.UUID, bps float64, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.on || bps <= 0 {
		return
	}
	if prev, ok := l.tput[id]; ok && prev.v > bps && now.Sub(prev.at) < throughputTTL {
		bps = prev.v
	}
	l.tput[id] = liveValue{v: bps, at: now}
}

// snapshot returns fresh copies of the unexpired transports, with latency
// and throughput overlaid when withQoS; ok is false when the set is off.
func (l *liveSet) snapshot(selfTransports, withQoS bool, now time.Time) (out []*transport.Entry, ok bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if !l.on {
		return nil, false
	}
	out = make([]*transport.Entry, 0, len(l.tps))
	for _, t := range l.tps {
		if now.After(t.expires) {
			continue
		}
		if !selfTransports && t.entry.Edges[0] == t.entry.Edges[1] {
			continue
		}
		e := t.entry
		if withQoS {
			l.overlayQoS(&e, now)
		}
		out = append(out, &e)
	}
	return out, true
}

// byEdge returns fresh copies of the unexpired transports with pk on either
// edge, like snapshot; ok is false when the set is off.
func (l *liveSet) byEdge(pk cipher.PubKey, withQoS bool, now time.Time) (out []*transport.Entry, ok bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if !l.on {
		return nil, false
	}
	for id := range l.edge[pk] {
		t, ok := l.tps[id]
		if !ok || now.After(t.expires) {
			continue
		}
		e := t.entry
		if withQoS {
			l.overlayQoS(&e, now)
		}
		out = append(out, &e)
	}
	return out, true
}

func (l *liveSet) overlayQoS(e *transport.Entry, now time.Time) {
	if v, ok := l.lat[e.ID]; ok && now.Sub(v.at) < latencyTTL {
		e.Latency = v.v
	}
	if v, ok := l.tput[e.ID]; ok && now.Sub(v.at) < throughputTTL {
		e.ThroughputBps = v.v
	}
}

// index and unindex keep the edge index in step with tps; callers hold mu.
func (l *liveSet) index(e *transport.Entry) {
	if l.edge == nil {
		l.edge = make(map[cipher.PubKey]map[uuid.UUID]struct{})
	}
	for _, pk := range e.Edges {
		m := l.edge[pk]
		if m == nil {
			m = make(map[uuid.UUID]struct{}, 2)
			l.edge[pk] = m
		}
		m[e.ID] = struct{}{}
	}
}

func (l *liveSet) unindex(e *transport.Entry) {
	for _, pk := range e.Edges {
		if m := l.edge[pk]; m != nil {
			delete(m, e.ID)
			if len(m) == 0 {
				delete(l.edge, pk)
			}
		}
	}
}

// sweep drops lapsed transports and stale QoS values, returning the dropped
// entries so their redis index memberships can be pruned.
func (l *liveSet) sweep(now time.Time) []transport.Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.on {
		return nil
	}
	var gone []transport.Entry
	for id, t := range l.tps {
		if now.After(t.expires) {
			gone = append(gone, t.entry)
			l.unindex(&t.entry)
			delete(l.tps, id)
		}
	}
	for id, v := range l.lat {
		if now.Sub(v.at) >= latencyTTL {
			delete(l.lat, id)
		}
	}
	for id, v := range l.tput {
		if now.Sub(v.at) >= throughputTTL {
			delete(l.tput, id)
		}
	}
	return gone
}

// liveStores maps a redis URL to the store holding its live set, so a route
// finder in the same process can read TPD's set.
var liveStores sync.Map

// SharedLiveStore returns the store in this process that holds the live
// transport set for the redis at url, if any.
func SharedLiveStore(url string) (Store, bool) {
	v, ok := liveStores.Load(url)
	if !ok {
		return nil, false
	}
	return v.(Store), true
}

// EnableLiveSet loads every transport from redis into the live set, then
// keeps it current from this store's own writes and answers the whole-set
// reads from it. For the process that writes transports (TPD) only. The set
// is published under url for SharedLiveStore, and swept until ctx ends.
func (s *redisStore) EnableLiveSet(ctx context.Context, url string) error {
	entries, err := s.scanAllTransports(ctx, true, true)
	if err != nil && err != ErrTransportNotFound {
		return err
	}
	s.hydrateDurableLatency(ctx, entries)
	now := time.Now()
	l := &s.live
	l.mu.Lock()
	l.ttl = s.ttl
	if l.ttl <= 0 {
		l.ttl = 5 * time.Minute
	}
	l.tps = make(map[uuid.UUID]*liveTransport, len(entries))
	l.edge = make(map[cipher.PubKey]map[uuid.UUID]struct{}, len(entries))
	l.lat = make(map[uuid.UUID]liveValue, len(entries))
	l.tput = make(map[uuid.UUID]liveValue, len(entries))
	l.on = true
	l.mu.Unlock()
	l.put(entries, now)
	for _, e := range entries {
		l.setLatency(e.ID, e.Latency, now)
		l.setThroughput(e.ID, e.ThroughputBps, now)
	}
	liveStores.Store(url, Store(s))
	s.log.WithField("transports", len(entries)).Info("Live transport set loaded; whole-set reads now served from memory")

	go func() {
		defer liveStores.CompareAndDelete(url, Store(s))
		t := time.NewTicker(liveSweepEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				s.pruneIndexes(ctx, l.sweep(now))
			}
		}
	}()
	return nil
}

// pruneIndexes removes lapsed transports from the redis index sets (their
// own keys have already expired).
func (s *redisStore) pruneIndexes(ctx context.Context, gone []transport.Entry) {
	if len(gone) == 0 {
		return
	}
	pipe := s.client.Pipeline()
	ids := make([]interface{}, len(gone))
	for i, e := range gone {
		idStr := e.ID.String()
		ids[i] = idStr
		pipe.SRem(ctx, s.edgeKey(e.Edges[0]), idStr)
		if e.Edges[0] != e.Edges[1] {
			pipe.SRem(ctx, s.edgeKey(e.Edges[1]), idStr)
		}
	}
	pipe.SRem(ctx, s.allTpsIndexKey(), ids...)
	if _, err := pipe.Exec(ctx); err != nil {
		s.log.WithError(err).Debug("live set: pruning lapsed transports from the index failed")
	}
	for _, e := range gone {
		s.edgeCache.Invalidate(e.Edges[0], e.Edges[1])
	}
}

// liveByEdge answers a by-edge read from the live set when it is on. The
// CXO reconcile does one per reporting visor; from redis each was an
// SMEMBERS, an MGET and a JSON decode per transport (~9% of TPD CPU on
// prod01, 2026-09-30).
func (s *redisStore) liveByEdge(pk cipher.PubKey, withQoS bool) ([]*transport.Entry, bool) {
	return s.live.byEdge(pk, withQoS, time.Now())
}

// edgeErr is the by-edge reads' empty result: ErrTransportNotFound.
func (s *redisStore) edgeErr(entries []*transport.Entry) error {
	if len(entries) == 0 {
		return ErrTransportNotFound
	}
	return nil
}

// ShareStore publishes s as the transport set for url, for a route finder in
// the same process, until ctx ends.
func ShareStore(ctx context.Context, url string, s Store) {
	liveStores.Store(url, s)
	go func() {
		<-ctx.Done()
		liveStores.CompareAndDelete(url, s)
	}()
}
