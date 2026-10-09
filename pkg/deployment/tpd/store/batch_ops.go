// Package store pkg/deployment/tpd/store/batch_ops.go c4-net-discovery
//
// Batched forms of the per-transport writes a CXO snapshot drives, so one
// snapshot costs a handful of pipelines rather than a round trip (or a
// pipeline) per transport.
package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
)

// BatchStore is implemented by stores that can apply a snapshot's writes in
// batches. The API uses it when present.
type BatchStore interface {
	// TouchTransports extends the lifetime of registered transports that are
	// still listed: the entry keys, and the reporter's edge index. Nothing is
	// rewritten. It returns the ids that are no longer stored, which a touch
	// cannot extend: they have to be registered again.
	TouchTransports(ctx context.Context, reporter cipher.PubKey, ids []uuid.UUID) (missing []uuid.UUID, err error)
	// DeregisterTransports removes transports and returns the ones that
	// existed.
	DeregisterTransports(ctx context.Context, ids []uuid.UUID) ([]*transport.Entry, error)
	// RecordTransportHeartbeats records an uptime heartbeat for each entry.
	RecordTransportHeartbeats(ctx context.Context, entries []*transport.Entry, at time.Time) error
}

// TouchTransports implements BatchStore.
func (s *redisStore) TouchTransports(ctx context.Context, reporter cipher.PubKey, ids []uuid.UUID) ([]uuid.UUID, error) {
	if len(ids) == 0 || s.ttl <= 0 {
		return nil, nil
	}
	pipe := s.client.Pipeline()
	expires := make([]*redis.BoolCmd, len(ids))
	for i, id := range ids {
		expires[i] = pipe.Expire(ctx, s.transportKey(id), s.ttl)
	}
	pipe.Expire(ctx, s.edgeKey(reporter), s.ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	// EXPIRE on a key that is gone succeeds and reports false. Those entries
	// were removed since they were written — expired, or deleted by a path
	// the reconcile throttle never hears of — and a touch does not bring
	// them back, so say which they are.
	var missing []uuid.UUID
	present := ids[:0:0]
	for i, id := range ids {
		if expires[i].Val() {
			present = append(present, id)
		} else {
			missing = append(missing, id)
		}
	}
	s.live.touch(present, time.Now())
	return missing, nil
}

// DeregisterTransports implements BatchStore: one MGET for the entries (their
// edges name the index sets to clean), one pipeline for every delete.
func (s *redisStore) DeregisterTransports(ctx context.Context, ids []uuid.UUID) ([]*transport.Entry, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	keys := make([]string, len(ids))
	for i, id := range ids {
		keys[i] = s.transportKey(id)
	}
	vals, err := s.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}
	removed := make([]*transport.Entry, 0, len(ids))
	pipe := s.client.Pipeline()
	for _, v := range vals {
		raw, ok := v.(string)
		if !ok || raw == "" {
			continue // already gone
		}
		var data TransportData
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			continue
		}
		entry, err := s.dataToEntry(data)
		if err != nil {
			continue
		}
		idStr := entry.ID.String()
		pipe.Del(ctx, s.transportKey(entry.ID))
		pipe.SRem(ctx, s.allTpsIndexKey(), idStr)
		pipe.SRem(ctx, s.edgeKey(entry.Edges[0]), idStr)
		if entry.Edges[0] != entry.Edges[1] {
			pipe.SRem(ctx, s.edgeKey(entry.Edges[1]), idStr)
		}
		removed = append(removed, entry)
	}
	if len(removed) == 0 {
		return nil, nil
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	for _, e := range removed {
		s.edgeCache.Invalidate(e.Edges[0], e.Edges[1])
		s.live.del(e.ID)
	}
	return removed, nil
}

// RecordTransportHeartbeats implements BatchStore with the same writes as
// RecordTransportHeartbeat, for every entry not yet recorded in its timeline
// slot, in one pipeline.
func (s *redisStore) RecordTransportHeartbeats(ctx context.Context, entries []*transport.Entry, at time.Time) error {
	if at.IsZero() {
		at = time.Now()
	}
	at = at.UTC()
	date := at.Format("2006-01-02")
	onlineKey := tpUptimeOnlineKey(date)
	slot := currentTimelineSlot(at)
	pipe := s.client.Pipeline()
	var written []uuid.UUID
	for _, e := range entries {
		if e == nil {
			continue
		}
		if !tracksUptime(string(e.Type)) {
			continue
		}
		if s.beats.recorded(e.ID, at) {
			continue
		}
		idStr := e.ID.String()
		key := tpUptimeKey(idStr, date)
		pipe.HSet(ctx, key, "type", string(e.Type), "last_seen", at.Unix())
		pipe.Expire(ctx, key, tpUptimeTodayTTL)
		pipe.SAdd(ctx, onlineKey, idStr)
		tlKey := tpUptimeTimelineKey(idStr, date)
		pipe.SetBit(ctx, tlKey, slot, 1)
		pipe.Expire(ctx, tlKey, tpUptimeTimelineTTL)
		written = append(written, e.ID)
	}
	if len(written) == 0 {
		return nil
	}
	pipe.Expire(ctx, onlineKey, tpUptimeTodayTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		s.log.WithError(err).Warn("RecordTransportHeartbeats: failed to persist transport heartbeats")
		return err
	}
	for _, id := range written {
		s.beats.note(id, at)
	}
	return nil
}

// TouchTransports implements BatchStore; the memory store has no TTL.
func (s *memoryStore) TouchTransports(context.Context, cipher.PubKey, []uuid.UUID) ([]uuid.UUID, error) {
	return nil, nil
}

// DeregisterTransports implements BatchStore.
func (s *memoryStore) DeregisterTransports(ctx context.Context, ids []uuid.UUID) ([]*transport.Entry, error) {
	var removed []*transport.Entry
	for _, id := range ids {
		e, err := s.GetTransportByID(ctx, id)
		if err != nil {
			continue
		}
		if err := s.DeregisterTransport(ctx, id); err != nil {
			return removed, err
		}
		removed = append(removed, e)
	}
	return removed, nil
}

// RecordTransportHeartbeats implements BatchStore; the memory store keeps no
// uptime.
func (s *memoryStore) RecordTransportHeartbeats(context.Context, []*transport.Entry, time.Time) error {
	return nil
}
