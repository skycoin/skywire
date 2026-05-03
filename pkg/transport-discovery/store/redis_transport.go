package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

func (s *redisStore) RegisterTransport(ctx context.Context, sEntry *transport.SignedEntry) error {
	entry := sEntry.Entry
	if entry == nil {
		return ErrBadEntry
	}

	sEntry.Registered = time.Now().UnixNano()
	now := time.Now()

	data := TransportData{
		ID:         entry.ID.String(),
		EdgeA:      entry.Edges[0].Hex(),
		EdgeB:      entry.Edges[1].Hex(),
		Type:       string(entry.Type),
		Label:      string(entry.Label),
		LastUpdate: now.Unix(),
	}

	// SignedEntry.Latency / SignedEntry.Bandwidth used to be the
	// channel by which visors pushed bw/latency telemetry to TPD. As
	// of the visor-self-tracking-stats change, that path is gone:
	// visors publish telemetry on their own CXO feeds and TPD
	// subscribes via pkg/transport-discovery/cxoaggregator. The
	// fields are still on the wire for old visors, but TPD ignores
	// them — incoming values are not persisted into TransportData.

	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}

	tpKey := s.transportKey(entry.ID)
	edgeAKey := s.edgeKey(entry.Edges[0])
	edgeBKey := s.edgeKey(entry.Edges[1])

	// Always apply TTL so stale transports expire when visors stop re-registering.
	pipe := s.client.Pipeline()
	pipe.Set(ctx, tpKey, string(raw), s.ttl)
	pipe.SAdd(ctx, edgeAKey, entry.ID.String())
	pipe.SAdd(ctx, s.allTpsIndexKey(), entry.ID.String())
	if s.ttl > 0 {
		pipe.Expire(ctx, edgeAKey, s.ttl)
	}
	if entry.Edges[0] != entry.Edges[1] {
		pipe.SAdd(ctx, edgeBKey, entry.ID.String())
		if s.ttl > 0 {
			pipe.Expire(ctx, edgeBKey, s.ttl)
		}
	}

	// Track visor PKs so they appear in /visors even after transports expire.
	pipe.SAdd(ctx, s.visorAllKey(), entry.Edges[0].Hex())
	if entry.Edges[0] != entry.Edges[1] {
		pipe.SAdd(ctx, s.visorAllKey(), entry.Edges[1].Hex())
	}
	pipe.Expire(ctx, s.visorAllKey(), 400*24*time.Hour)

	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}

	// Invalidate the per-edge entry cache so mirrorEdges (called by the
	// API layer right after this returns) re-fetches the post-write list.
	s.edgeCache.Invalidate(entry.Edges[0], entry.Edges[1])

	return nil
}

// RegisterTransportsBatch registers multiple transports in a single Redis
// pipeline. This reduces TCP round-trips from N pipelines (one per transport)
// to 1 pipeline for the entire batch. At ~50 registrations/sec × 8 commands
// each, this cuts Redis syscall overhead significantly.
func (s *redisStore) RegisterTransportsBatch(ctx context.Context, entries []*transport.SignedEntry) error {
	if len(entries) == 0 {
		return nil
	}

	pipe := s.client.Pipeline()
	now := time.Now()

	for _, sEntry := range entries {
		entry := sEntry.Entry
		if entry == nil {
			continue
		}
		sEntry.Registered = now.UnixNano()

		data := TransportData{
			ID:         entry.ID.String(),
			EdgeA:      entry.Edges[0].Hex(),
			EdgeB:      entry.Edges[1].Hex(),
			Type:       string(entry.Type),
			Label:      string(entry.Label),
			LastUpdate: now.Unix(),
		}
		// bw/latency from SignedEntry no longer persisted — see
		// RegisterTransport above.

		raw, err := json.Marshal(data)
		if err != nil {
			continue
		}

		tpKey := s.transportKey(entry.ID)
		edgeAKey := s.edgeKey(entry.Edges[0])

		pipe.Set(ctx, tpKey, string(raw), s.ttl)
		pipe.SAdd(ctx, edgeAKey, entry.ID.String())
		pipe.SAdd(ctx, s.allTpsIndexKey(), entry.ID.String())
		if s.ttl > 0 {
			pipe.Expire(ctx, edgeAKey, s.ttl)
		}
		if entry.Edges[0] != entry.Edges[1] {
			edgeBKey := s.edgeKey(entry.Edges[1])
			pipe.SAdd(ctx, edgeBKey, entry.ID.String())
			if s.ttl > 0 {
				pipe.Expire(ctx, edgeBKey, s.ttl)
			}
		}
		pipe.SAdd(ctx, s.visorAllKey(), entry.Edges[0].Hex())
		if entry.Edges[0] != entry.Edges[1] {
			pipe.SAdd(ctx, s.visorAllKey(), entry.Edges[1].Hex())
		}
	}

	pipe.Expire(ctx, s.visorAllKey(), 400*24*time.Hour)

	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}

	// Invalidate every touched edge so mirrorEdges sees the post-batch
	// state on the next GetTransportsByEdge call.
	for _, sEntry := range entries {
		if sEntry == nil || sEntry.Entry == nil {
			continue
		}
		s.edgeCache.Invalidate(sEntry.Entry.Edges[0], sEntry.Entry.Edges[1])
	}
	return nil
}

func (s *redisStore) DeregisterTransport(ctx context.Context, id uuid.UUID) error {
	// First get the transport to know the edges
	entry, err := s.GetTransportByID(ctx, id)
	if err != nil {
		if errors.Is(err, ErrTransportNotFound) {
			return nil // Already deleted
		}
		return err
	}

	tpKey := s.transportKey(id)
	edgeAKey := s.edgeKey(entry.Edges[0])
	edgeBKey := s.edgeKey(entry.Edges[1])

	pipe := s.client.Pipeline()
	pipe.Del(ctx, tpKey)
	pipe.SRem(ctx, edgeAKey, id.String())
	pipe.SRem(ctx, s.allTpsIndexKey(), id.String())
	if entry.Edges[0] != entry.Edges[1] {
		pipe.SRem(ctx, edgeBKey, id.String())
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}

	s.edgeCache.Invalidate(entry.Edges[0], entry.Edges[1])
	return nil
}

func (s *redisStore) GetTransportByID(ctx context.Context, id uuid.UUID) (*transport.Entry, error) {
	tpKey := s.transportKey(id)

	raw, err := s.client.Get(ctx, tpKey).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, ErrTransportNotFound
		}
		return nil, err
	}

	var data TransportData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return nil, err
	}

	entry, err := s.dataToEntry(data)
	if err != nil {
		return nil, err
	}
	rec, _ := s.getLatencyRecord(ctx, id) //nolint:errcheck // best-effort overlay; entry stays usable without latency
	if rec != nil && rec.Avg > 0 {
		entry.Latency = float64(rec.Avg) / 1000.0
	}
	return entry, nil
}

func (s *redisStore) GetTransportsByEdge(ctx context.Context, pk cipher.PubKey) ([]*transport.Entry, error) {
	if entries, ok := s.edgeCache.Get(pk); ok {
		return entries, nil
	}

	edgeKey := s.edgeKey(pk)

	ids, err := s.client.SMembers(ctx, edgeKey).Result()
	if err != nil {
		return nil, err
	}

	if len(ids) == 0 {
		return nil, ErrTransportNotFound
	}

	// Build transport keys and filter out unparseable UUIDs
	type idMapping struct {
		idStr string
		id    uuid.UUID
	}
	var mappings []idMapping
	var keys []string
	for _, idStr := range ids {
		id, err := uuid.Parse(idStr)
		if err != nil {
			continue
		}
		mappings = append(mappings, idMapping{idStr: idStr, id: id})
		keys = append(keys, s.transportKey(id))
	}

	if len(keys) == 0 {
		return nil, ErrTransportNotFound
	}

	// Fetch all transport values in one MGET call
	vals, err := s.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, err
	}

	var entries []*transport.Entry
	var staleIDs []interface{}
	for i, val := range vals {
		raw, ok := val.(string)
		if !ok || raw == "" {
			// Transport expired or missing, mark for cleanup
			staleIDs = append(staleIDs, mappings[i].idStr)
			continue
		}

		var data TransportData
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			continue
		}

		entry, err := s.dataToEntry(data)
		if err != nil {
			continue
		}
		entries = append(entries, entry)
	}

	// Clean up stale IDs from the edge set via pipeline
	if len(staleIDs) > 0 {
		pipe := s.client.Pipeline()
		pipe.SRem(ctx, edgeKey, staleIDs...)
		_, _ = pipe.Exec(ctx) //nolint:errcheck
	}

	if len(entries) == 0 {
		return nil, ErrTransportNotFound
	}

	s.hydrateDurableLatency(ctx, entries)
	s.edgeCache.Put(pk, entries)
	return entries, nil
}

func (s *redisStore) GetNumberOfTransports(ctx context.Context) (map[types.Type]int, error) {
	response := map[types.Type]int{
		types.STCP:  0,
		types.STCPR: 0,
		types.SUDPH: 0,
		types.DMSG:  0,
	}

	keys, ids, err := s.allTransportKeysFromIndex(ctx)
	if err != nil {
		return nil, err
	}

	const mgetBatch = 10000
	var stale []interface{}
	for i := 0; i < len(keys); i += mgetBatch {
		end := i + mgetBatch
		if end > len(keys) {
			end = len(keys)
		}
		vals, err := s.client.MGet(ctx, keys[i:end]...).Result()
		if err != nil {
			continue
		}
		for j, val := range vals {
			raw, ok := val.(string)
			if !ok || raw == "" {
				stale = append(stale, ids[i+j])
				continue
			}
			var data TransportData
			if err := json.Unmarshal([]byte(raw), &data); err != nil {
				continue
			}
			response[types.Type(data.Type)]++
		}
	}
	s.maybeReapStaleTransports(stale)

	return response, nil
}

func (s *redisStore) GetAllTransports(ctx context.Context, selfTransports bool) ([]*transport.Entry, error) {
	if entries, ok := s.allTpsCache.Get(selfTransports, false); ok {
		return entries, nil
	}
	entries, err := s.scanAllTransports(ctx, selfTransports, false)
	if err != nil {
		return nil, err
	}
	s.allTpsCache.Put(selfTransports, false, entries)
	return entries, nil
}

// getAllTransportsWithQoS returns all transports including QoS metrics.
// Used internally by metrics functions that need bandwidth/latency data.
// Cached with the same TTL+slot scheme as GetAllTransports — metrics
// scrapers (Prometheus / Victoria Metrics) hit these endpoints on a
// regular cadence and were paying a full SCAN+MGET each time.
//
// Latency lives in dedicated lat:<id> keys (independent of the tp:<id>
// registration TTL); after scanAllTransports populates entries from the
// blobs we overlay the durable latency so aggregate-metric paths
// (GetNetworkMetrics, GetVisorAggregateMetrics) see the same values
// /metrics surfaces, including across registration churn.
func (s *redisStore) getAllTransportsWithQoS(ctx context.Context, selfTransports bool) ([]*transport.Entry, error) {
	if entries, ok := s.allTpsCache.Get(selfTransports, true); ok {
		return entries, nil
	}
	entries, err := s.scanAllTransports(ctx, selfTransports, true)
	if err != nil {
		return nil, err
	}
	s.hydrateDurableLatency(ctx, entries)
	s.allTpsCache.Put(selfTransports, true, entries)
	return entries, nil
}

// hydrateDurableLatency overlays the persisted lat:<id> values onto
// entry.Latency. Best-effort: a redis or decode error leaves the entry
// at whatever scanAllTransports produced (which after the latency
// move-out is 0 — the blob's lat_avg field is no longer written, but
// remains in the schema for backwards-compatible decoding of older
// payloads still present in redis).
func (s *redisStore) hydrateDurableLatency(ctx context.Context, entries []*transport.Entry) {
	if len(entries) == 0 {
		return
	}
	keys := make([]string, len(entries))
	for i, e := range entries {
		keys[i] = s.latencyKey(e.ID)
	}
	vals, err := s.client.MGet(ctx, keys...).Result()
	if err != nil {
		return
	}
	for i, v := range vals {
		raw, ok := v.(string)
		if !ok || raw == "" {
			continue
		}
		var rec LatencyRecord
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			continue
		}
		if rec.Avg > 0 {
			// Same us → ms conversion dataToEntry applies.
			entries[i].Latency = float64(rec.Avg) / 1000.0
		}
	}
}

// scanAllTransports is the shared implementation for GetAllTransports and getAllTransportsWithQoS.
// Reads the transport-id index set built by RegisterTransport / DeregisterTransport
// and MGET-fetches the values; lazy-removes stale members whose primary
// key TTL'd without an explicit deregister.
func (s *redisStore) scanAllTransports(ctx context.Context, selfTransports, withQoS bool) ([]*transport.Entry, error) {
	keys, ids, err := s.allTransportKeysFromIndex(ctx)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, nil
	}

	const mgetBatch = 10000
	var entries []*transport.Entry
	var stale []interface{}

	for i := 0; i < len(keys); i += mgetBatch {
		end := i + mgetBatch
		if end > len(keys) {
			end = len(keys)
		}

		vals, err := s.client.MGet(ctx, keys[i:end]...).Result()
		if err != nil {
			return nil, err
		}

		for j, val := range vals {
			raw, ok := val.(string)
			if !ok || raw == "" {
				stale = append(stale, ids[i+j])
				continue
			}

			var data TransportData
			if err := json.Unmarshal([]byte(raw), &data); err != nil {
				continue
			}

			var entry *transport.Entry
			if withQoS {
				entry, err = s.dataToEntry(data)
			} else {
				entry, err = s.dataToEntryCore(data)
			}
			if err != nil {
				continue
			}

			if !selfTransports && entry.Edges[0] == entry.Edges[1] {
				continue
			}

			entries = append(entries, entry)
		}
	}
	s.maybeReapStaleTransports(stale)

	return entries, nil
}

// allTpsIndexKey is the SET that tracks every live transport ID.
// Maintained on Register/Deregister; replaces the pre-existing
// SCAN of the tp:* keyspace used by GetNumberOfTransports,
// scanAllTransports, and getP2PTransportCounts.
func (s *redisStore) allTpsIndexKey() string {
	return fmt.Sprintf("%s:tp:_index", serviceName)
}

// allTransportKeysFromIndex reads the transport-id index set and returns
// the corresponding transport keys plus the raw IDs (parallel slices).
// Use the returned ids slice to SREM stale members on MGet miss.
func (s *redisStore) allTransportKeysFromIndex(ctx context.Context) (keys, ids []string, err error) {
	ids, err = s.client.SMembers(ctx, s.allTpsIndexKey()).Result()
	if err != nil {
		return nil, nil, err
	}
	keys = make([]string, len(ids))
	for i, id := range ids {
		keys[i] = fmt.Sprintf("%s:tp:%s", serviceName, id)
	}
	return keys, ids, nil
}

// maybeReapStaleTransports removes index members whose primary key
// TTL'd without an explicit Deregister. Fire-and-forget so the read
// path stays fast; index converges over time as readers detect stale
// members.
func (s *redisStore) maybeReapStaleTransports(stale []interface{}) {
	if len(stale) == 0 {
		return
	}
	go func() {
		s.client.SRem(context.Background(), s.allTpsIndexKey(), stale...) //nolint:errcheck
	}()
}

func (s *redisStore) transportKey(id uuid.UUID) string {
	return fmt.Sprintf("%s:tp:%s", serviceName, id.String())
}

func (s *redisStore) latencyKey(id uuid.UUID) string {
	return fmt.Sprintf("%s:lat:%s", serviceName, id.String())
}

func (s *redisStore) edgeKey(pk cipher.PubKey) string {
	return fmt.Sprintf("%s:edge:%s", serviceName, pk.Hex())
}

// dataToEntry converts TransportData to Entry with full QoS metrics.
// Used for endpoints that require bandwidth/latency data.
func (s *redisStore) dataToEntry(data TransportData) (*transport.Entry, error) {
	entry, err := s.dataToEntryCore(data)
	if err != nil {
		return nil, err
	}
	// Convert latency from microseconds to milliseconds for backwards compatibility
	entry.Latency = float64(data.LatencyAvg) / 1000.0
	entry.Bandwidth = data.Bandwidth
	return entry, nil
}

// dataToEntryCore converts TransportData to Entry without QoS metrics.
// Used for /all-transports which should not include bandwidth/latency per spec.
func (s *redisStore) dataToEntryCore(data TransportData) (*transport.Entry, error) {
	id, err := uuid.Parse(data.ID)
	if err != nil {
		return nil, err
	}

	edgeA, err := s.pkCache.Parse(data.EdgeA)
	if err != nil {
		return nil, err
	}
	edgeB, err := s.pkCache.Parse(data.EdgeB)
	if err != nil {
		return nil, err
	}

	return &transport.Entry{
		ID:    id,
		Edges: [2]cipher.PubKey{edgeA, edgeB},
		Type:  types.Type(data.Type),
		Label: transport.Label(data.Label),
	}, nil
}
