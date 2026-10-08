// Package store pkg/deployment/tpd/store/today_metrics.go c4-net-discovery
//
// Today's per-transport metrics, kept current by what changed.
//
// The CXO metrics publisher rewrites today's leaf every minute. Reading it
// whole meant one HGETALL (today's bandwidth hash) and one GET (latency) per
// transport per minute — ~107k of each on the live deployment (2026-09-30),
// nearly all for transports whose figures had not moved. Bandwidth and
// latency are written through this store (UpdateBandwidth, UpdateLatency),
// so it knows exactly which transports moved: those are re-read, and every
// other row is carried over.
//
// Settled days are handled the other way round: a day that can no longer
// change is saved, as the gzipped leaf bytes the publisher produced, next to
// the data it summarizes. A restart then reads 29 small values instead of
// re-aggregating 30 days (~4.9M HGETALLs per restart, measured).
package store

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// todayMetrics is the cache behind GetTodayTransportMetrics. Its zero value
// is ready to use.
type todayMetrics struct {
	mu    sync.Mutex
	date  string
	rows  map[uuid.UUID]TransportMetric
	dirty map[uuid.UUID]struct{}
}

// markDirty records that a transport's figures for today moved.
func (t *todayMetrics) markDirty(id uuid.UUID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.dirty == nil {
		t.dirty = make(map[uuid.UUID]struct{})
	}
	t.dirty[id] = struct{}{}
}

// takeDirty returns and clears the dirty set.
func (t *todayMetrics) takeDirty() map[uuid.UUID]struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	d := t.dirty
	t.dirty = nil
	return d
}

// todayQuery is the query the metrics publisher makes for one day.
func todayQuery() MetricsQuery {
	return MetricsQuery{Days: 1, Live: "all", Edges: true, Bandwidth: true, Latency: true}
}

// GetTodayTransportMetrics returns today's metrics for every transport, as
// GetAllTransportMetrics would for one day, re-reading only the transports
// whose bandwidth or latency was written since the last call. The first
// call of a UTC day reads everything.
func (s *redisStore) GetTodayTransportMetrics(ctx context.Context) ([]TransportMetric, error) {
	date := time.Now().UTC().Format(MetricsDateFormat)
	t := &s.today

	t.mu.Lock()
	seeded := t.date == date && t.rows != nil
	t.mu.Unlock()

	if !seeded {
		t.takeDirty() // the full read covers them
		all, err := s.GetAllTransportMetrics(ctx, todayQuery())
		if err != nil {
			return nil, err
		}
		rows := make(map[uuid.UUID]TransportMetric, len(all))
		for _, m := range all {
			if id, err := uuid.Parse(m.ID); err == nil {
				rows[id] = m
			}
		}
		t.mu.Lock()
		t.date, t.rows = date, rows
		t.mu.Unlock()
		return all, nil
	}

	// The registered set says who is live; it is also where a transport
	// seen for the first time today gets its type and edges.
	registered, err := s.getAllTransportsWithQoS(ctx, true)
	if err != nil && err != ErrTransportNotFound {
		return nil, err
	}
	byID := make(map[uuid.UUID]*transport.Entry, len(registered))
	for _, e := range registered {
		byID[e.ID] = e
	}

	dirty := t.takeDirty()
	entries := make([]*transport.Entry, 0, len(dirty))
	expired := make(map[uuid.UUID]bool)
	t.mu.Lock()
	for id := range dirty {
		if e, ok := byID[id]; ok {
			entries = append(entries, e)
			continue
		}
		// Reported but no longer registered: its row's edges still name it.
		if row, ok := t.rows[id]; ok && len(row.Edges) == 2 {
			e := &transport.Entry{ID: id, Type: types.Type(row.Type)}
			if e.Edges[0].Set(row.Edges[0]) == nil && e.Edges[1].Set(row.Edges[1]) == nil {
				entries = append(entries, e)
				expired[id] = true
			}
		}
	}
	t.mu.Unlock()

	var fresh []TransportMetric
	if len(entries) > 0 {
		fresh, err = s.buildTransportMetrics(ctx, entries, expired, todayQuery())
		if err != nil {
			// Put the marks back: nothing was applied.
			for id := range dirty {
				t.markDirty(id)
			}
			return nil, err
		}
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.date != date {
		// The day turned while this ran; the next call reads it whole.
		return nil, fmt.Errorf("day changed during today's metrics read")
	}
	for _, m := range fresh {
		if id, err := uuid.Parse(m.ID); err == nil {
			t.rows[id] = m
		}
	}
	out := make([]TransportMetric, 0, len(t.rows))
	for id, m := range t.rows {
		_, live := byID[id]
		m.Live = live
		t.rows[id] = m
		out = append(out, m)
	}
	return out, nil
}

// metricsLeafKey holds one settled day's published leaf parts.
func (s *redisStore) metricsLeafKey(date string) string {
	return fmt.Sprintf("%s:metrics:leaf:%s", serviceName, date)
}

// SaveMetricsLeaf keeps a settled day's leaf parts (gzipped, as published)
// so a restart does not have to rebuild them: in the archive when there is
// one, and in redis for leafLiveDays (the history TTL without an archive).
func (s *redisStore) SaveMetricsLeaf(ctx context.Context, date string, parts [][]byte) error {
	ttl := time.Duration(historyTTLSeconds) * time.Second
	if s.leafArchive != "" {
		if err := s.writeLeafFile(date, parts); err != nil {
			return fmt.Errorf("archive leaf %s: %w", date, err)
		}
		ttl = leafLiveDays * 24 * time.Hour
	}
	key := s.metricsLeafKey(date)
	pipe := s.client.TxPipeline()
	pipe.Del(ctx, key)
	for _, p := range parts {
		pipe.RPush(ctx, key, p)
	}
	pipe.Expire(ctx, key, ttl)
	_, err := pipe.Exec(ctx)
	return err
}

// LoadMetricsLeaves returns the saved leaf parts of each date that has them,
// from redis or else the archive.
func (s *redisStore) LoadMetricsLeaves(ctx context.Context, dates []string) (map[string][][]byte, error) {
	out, err := s.loadLeavesFromRedis(ctx, dates)
	if err != nil || s.leafArchive == "" {
		return out, err
	}
	for _, d := range dates {
		if _, ok := out[d]; ok {
			continue
		}
		parts, err := s.readLeafFile(d)
		if err != nil {
			s.log.WithError(err).WithField("date", d).Warn("could not read archived metrics leaf")
			continue
		}
		if len(parts) > 0 {
			out[d] = parts
		}
	}
	return out, nil
}

func (s *redisStore) loadLeavesFromRedis(ctx context.Context, dates []string) (map[string][][]byte, error) {
	pipe := s.client.Pipeline()
	cmds := make(map[string]interface{ Result() ([]string, error) }, len(dates))
	for _, d := range dates {
		cmds[d] = pipe.LRange(ctx, s.metricsLeafKey(d), 0, -1)
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, err
	}
	out := make(map[string][][]byte, len(dates))
	for d, c := range cmds {
		vals, err := c.Result()
		if err != nil || len(vals) == 0 {
			continue
		}
		parts := make([][]byte, len(vals))
		for i, v := range vals {
			parts[i] = []byte(v)
		}
		out[d] = parts
	}
	return out, nil
}
