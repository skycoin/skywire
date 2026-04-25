package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

const (
	serviceName = "transport-discovery"
)

// TransportData stores transport entry with additional metadata.
type TransportData struct {
	ID         string `json:"id"`
	EdgeA      string `json:"edge_a"`
	EdgeB      string `json:"edge_b"`
	Type       string `json:"type"`
	Label      string `json:"label"`
	LatencyMin int64  `json:"lat_min"`     // Minimum latency in microseconds
	LatencyMax int64  `json:"lat_max"`     // Maximum latency in microseconds
	LatencyAvg int64  `json:"lat_avg"`     // Average latency in microseconds
	Bandwidth  uint64 `json:"bandwidth"`   // Total bytes (sent + recv)
	LastUpdate int64  `json:"last_update"` // Unix timestamp of last update
}

type redisStore struct {
	client      *redis.Client
	ttl         time.Duration
	log         *logging.Logger
	pkCache     *pubKeyCache
	edgeCache   *edgeEntriesCache
	allTpsCache *allTransportsCache
}

func newRedisStore(ctx context.Context, addr, password string, poolSize int, ttl time.Duration, logger *logging.Logger) (*redisStore, error) {
	opt, err := redis.ParseURL(addr)
	if err != nil {
		return nil, fmt.Errorf("addr: %w", err)
	}

	opt.Password = password

	if poolSize != 0 {
		opt.PoolSize = poolSize
	}

	opt.MinIdleConns = poolSize / 2
	opt.MaxConnAge = 30 * time.Minute
	opt.PoolTimeout = 10 * time.Second
	opt.IdleTimeout = 5 * time.Minute
	opt.IdleCheckFrequency = 1 * time.Minute
	opt.DialTimeout = 5 * time.Second
	opt.ReadTimeout = 5 * time.Second
	opt.WriteTimeout = 5 * time.Second

	redisCl := redis.NewClient(opt)

	err = netutil.NewRetrier(logger, netutil.DefaultInitBackoff, netutil.DefaultMaxBackoff, 10, netutil.DefaultFactor).Do(ctx, func() error {
		_, err = redisCl.Ping(ctx).Result()
		return err
	})
	if err != nil {
		return nil, err
	}

	return &redisStore{
		client:      redisCl,
		ttl:         ttl,
		log:         logger,
		pkCache:     newPubKeyCache(defaultPubKeyCacheCap),
		edgeCache:   newEdgeEntriesCache(defaultEdgeEntriesCacheCap, defaultEdgeEntriesCacheTTL),
		allTpsCache: newAllTransportsCache(defaultAllTransportsCacheTTL),
	}, nil
}

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

	// Handle latency if provided
	if sEntry.Latency != nil {
		data.LatencyMin = sEntry.Latency.Min
		data.LatencyMax = sEntry.Latency.Max
		data.LatencyAvg = sEntry.Latency.Avg
	}

	// Handle bandwidth if provided
	if sEntry.Bandwidth != nil {
		data.Bandwidth = sEntry.Bandwidth.SentBytes + sEntry.Bandwidth.RecvBytes

		// Determine the reporting visor from auth context; fall back to edges[0]
		reporterPK := httpauth.PKFromCtx(ctx)
		if reporterPK.Null() {
			reporterPK = entry.Edges[0]
		}

		// Update bandwidth aggregations
		if err := s.updateBandwidth(ctx, entry.ID.String(), reporterPK,
			sEntry.Bandwidth.SentBytes, sEntry.Bandwidth.RecvBytes); err != nil {
			s.log.WithError(err).Warn("Failed to update bandwidth aggregation")
		}
	}

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
		if sEntry.Latency != nil {
			data.LatencyMin = sEntry.Latency.Min
			data.LatencyMax = sEntry.Latency.Max
			data.LatencyAvg = sEntry.Latency.Avg
		}
		if sEntry.Bandwidth != nil {
			data.Bandwidth = sEntry.Bandwidth.SentBytes + sEntry.Bandwidth.RecvBytes
		}

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

	return s.dataToEntry(data)
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
func (s *redisStore) getAllTransportsWithQoS(ctx context.Context, selfTransports bool) ([]*transport.Entry, error) {
	if entries, ok := s.allTpsCache.Get(selfTransports, true); ok {
		return entries, nil
	}
	entries, err := s.scanAllTransports(ctx, selfTransports, true)
	if err != nil {
		return nil, err
	}
	s.allTpsCache.Put(selfTransports, true, entries)
	return entries, nil
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

func (s *redisStore) Close() {
	if err := s.client.Close(); err != nil {
		s.log.WithError(err).Warn("Failed to close Redis client")
	}
}

func (s *redisStore) transportKey(id uuid.UUID) string {
	return fmt.Sprintf("%s:tp:%s", serviceName, id.String())
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

// Bandwidth key generators
func (s *redisStore) bandwidthPrevKey(tpID, reporterPKHex string) string {
	return fmt.Sprintf("%s:bw:prev:%s:%s", serviceName, tpID, reporterPKHex)
}

func (s *redisStore) bandwidthDailyKey(tpID string, t time.Time) string {
	return fmt.Sprintf("%s:bw:daily:%s:%s", serviceName, tpID, t.Format("2006-01-02"))
}

// Visor-level bandwidth key generators
func (s *redisStore) visorBandwidthDailyKey(pkHex string, t time.Time) string {
	return fmt.Sprintf("%s:bw:visor:daily:%s:%s", serviceName, pkHex, t.Format("2006-01-02"))
}

func (s *redisStore) visorAllKey() string {
	return fmt.Sprintf("%s:bw:visor:all", serviceName)
}

// updateBandwidth calculates the bandwidth delta and updates per-transport and
// per-visor aggregation hashes in Redis. Sent and recv are tracked separately
// per-reporter so the metrics API can reconstruct accurate per-edge bandwidth.
// The prev snapshot is keyed per-reporter to avoid cross-contamination when both
// edges of a transport register independently.
func (s *redisStore) updateBandwidth(ctx context.Context, transportID string,
	reporterPK cipher.PubKey, currentSent, currentRecv uint64) error {

	now := time.Now().UTC()
	reporterHex := reporterPK.Hex()

	// 1. Get previous snapshot (per-reporter) to calculate deltas
	prevKey := s.bandwidthPrevKey(transportID, reporterHex)
	prevResult, err := s.client.HGetAll(ctx, prevKey).Result()

	var deltaSent, deltaRecv uint64
	if err == nil && len(prevResult) > 0 {
		var prevSent, prevRecv uint64
		fmt.Sscanf(prevResult["sent"], "%d", &prevSent) //nolint:errcheck,gosec
		fmt.Sscanf(prevResult["recv"], "%d", &prevRecv) //nolint:errcheck,gosec
		if currentSent >= prevSent {
			deltaSent = currentSent - prevSent
		} else {
			deltaSent = currentSent // Counter reset
		}
		if currentRecv >= prevRecv {
			deltaRecv = currentRecv - prevRecv
		} else {
			deltaRecv = currentRecv // Counter reset
		}
	} else {
		// First time or key expired — use full current values
		deltaSent = currentSent
		deltaRecv = currentRecv
	}

	// 2. Store current as previous for next calculation
	// TTL of 10 minutes allows for missed re-registration cycles (every 90s)
	pipe := s.client.Pipeline()
	pipe.HSet(ctx, prevKey, "sent", currentSent, "recv", currentRecv)
	pipe.Expire(ctx, prevKey, 10*time.Minute)

	// 3. Add deltas to aggregations
	delta := deltaSent + deltaRecv
	if delta > 0 {
		// Per-transport daily aggregation — store per-reporter sent/recv separately
		dailyKey := s.bandwidthDailyKey(transportID, now)
		pipe.HIncrBy(ctx, dailyKey, reporterHex+":sent", int64(deltaSent)) //nolint:gosec
		pipe.HIncrBy(ctx, dailyKey, reporterHex+":recv", int64(deltaRecv)) //nolint:gosec
		// Keep combined total for backward compatibility
		pipe.HIncrBy(ctx, dailyKey, "bandwidth", int64(delta)) //nolint:gosec
		pipe.HSet(ctx, dailyKey, "updated_at", now.Unix())
		pipe.Expire(ctx, dailyKey, 35*24*time.Hour)

		// Per-visor daily aggregation — only for the reporter
		pipe.SAdd(ctx, s.visorAllKey(), reporterHex)
		pipe.Expire(ctx, s.visorAllKey(), 400*24*time.Hour)

		vDaily := s.visorBandwidthDailyKey(reporterHex, now)
		pipe.HIncrBy(ctx, vDaily, "bandwidth", int64(delta)) //nolint:gosec
		pipe.HSet(ctx, vDaily, "updated_at", now.Unix())
		pipe.Expire(ctx, vDaily, 35*24*time.Hour)
	}

	_, err = pipe.Exec(ctx)
	return err
}

// GetTransportBandwidth retrieves bandwidth aggregations for a transport.
func (s *redisStore) GetTransportBandwidth(ctx context.Context, tpID uuid.UUID,
	period string, limit int) ([]BandwidthAggregation, error) {

	transportID := tpID.String()
	now := time.Now().UTC()
	var results []BandwidthAggregation

	switch period {
	case "daily":
		for i := 0; i < limit; i++ {
			t := now.AddDate(0, 0, -i)
			key := s.bandwidthDailyKey(transportID, t)
			agg, err := s.getBandwidthFromHash(ctx, key, transportID, "daily", t.Format("2006-01-02"))
			if err == nil {
				results = append(results, agg)
			}
		}
	default:
		return nil, fmt.Errorf("invalid period: %s (only 'daily' is supported)", period)
	}

	return results, nil
}

// GetVisorBandwidth retrieves aggregated bandwidth for all transports of a visor.
func (s *redisStore) GetVisorBandwidth(ctx context.Context, pk cipher.PubKey,
	period string, limit int) ([]BandwidthAggregation, error) {

	if period != "daily" {
		return nil, fmt.Errorf("invalid period: %s (only 'daily' is supported)", period)
	}

	entries, err := s.GetTransportsByEdge(ctx, pk)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()

	// Pipeline all HGetAll calls: entries × days
	type bwLookup struct {
		dateStr string
	}
	var lookups []bwLookup
	var cmds []*redis.StringStringMapCmd

	pipe := s.client.Pipeline()
	for d := 0; d < limit; d++ {
		t := now.AddDate(0, 0, -d)
		dateStr := t.Format("2006-01-02")
		for _, entry := range entries {
			key := s.bandwidthDailyKey(entry.ID.String(), t)
			cmds = append(cmds, pipe.HGetAll(ctx, key))
			lookups = append(lookups, bwLookup{dateStr: dateStr})
		}
	}
	_, _ = pipe.Exec(ctx) //nolint:errcheck

	// Aggregate by date
	aggregatedByPeriod := make(map[string]*BandwidthAggregation)
	for i, cmd := range cmds {
		result, err := cmd.Result()
		if err != nil || len(result) == 0 {
			continue
		}
		var bw uint64
		if val, ok := result["bandwidth"]; ok {
			fmt.Sscanf(val, "%d", &bw) //nolint:errcheck,gosec
		}
		if bw == 0 {
			continue
		}
		var updatedAt int64
		if val, ok := result["updated_at"]; ok {
			fmt.Sscanf(val, "%d", &updatedAt) //nolint:errcheck,gosec
		}

		dateStr := lookups[i].dateStr
		if existing, ok := aggregatedByPeriod[dateStr]; ok {
			existing.Bandwidth += bw
			if updatedAt > existing.UpdatedAt {
				existing.UpdatedAt = updatedAt
			}
		} else {
			aggregatedByPeriod[dateStr] = &BandwidthAggregation{
				TransportID: pk.Hex(),
				Period:      period,
				PeriodKey:   dateStr,
				Bandwidth:   bw,
				UpdatedAt:   updatedAt,
			}
		}
	}

	var results []BandwidthAggregation
	for _, agg := range aggregatedByPeriod {
		results = append(results, *agg)
	}

	return results, nil
}

// GetAllVisorSummaries returns uptime summaries for all visors with active transports.
// Online status is determined by having active transports.
// Version and Daily uptime fields require uptime tracker integration.
// uptimeKey returns the Redis key for a visor's heartbeat data on a given date.
func uptimeKey(pk string, date string) string {
	return fmt.Sprintf("%s:uptime:%s:%s", serviceName, pk, date)
}

// uptimeOnlineKey returns the Redis key for the set of visors seen today.
func uptimeOnlineKey(date string) string {
	return fmt.Sprintf("%s:uptime:online:%s", serviceName, date)
}

// uptimeTimelineKey returns the Redis key for a visor's timeline bitmap on a given date.
// The bitmap has 288 bits — one per 5-minute slot in the day.
func uptimeTimelineKey(pk string, date string) string {
	return fmt.Sprintf("%s:uptime:%s:%s:timeline", serviceName, pk, date)
}

// timelineSlots is the number of 5-minute slots in a day (288).
const timelineSlots = 24 * 60 / 5

// currentTimelineSlot returns the 0-based slot index for the given time (0–287).
func currentTimelineSlot(t time.Time) int64 {
	return int64(t.Hour()*12 + t.Minute()/5)
}

// uptimeHistoryDays is the number of days of daily uptime to include in v2 responses.
const uptimeHistoryDays = 7

// expectedHeartbeatsPerDay is the number of heartbeats expected in a full day.
// Visors heartbeat via transport re-registration every 90 seconds (960/day),
// AND via the explicit /v4/update endpoint every 5 minutes (288/day).
// Use the 90s interval as the baseline since transport registration is the
// primary heartbeat source.
const expectedHeartbeatsPerDay = float64(24*60*60) / float64(90) // 960

// RecordHeartbeat records a visor heartbeat for uptime tracking.
// Each heartbeat increments the daily counter and updates the version/last_seen.
func (s *redisStore) RecordHeartbeat(ctx context.Context, pk cipher.PubKey, version string) error {
	now := time.Now().UTC()
	date := now.Format("2006-01-02")
	pkHex := pk.Hex()
	key := uptimeKey(pkHex, date)

	pipe := s.client.Pipeline()

	// Increment heartbeat count for today
	pipe.HIncrBy(ctx, key, "count", 1)
	// Update version and last_seen
	pipe.HSet(ctx, key, "version", version)
	pipe.HSet(ctx, key, "last_seen", now.Unix())
	// Set TTL: keep for 8 days (7 days history + buffer)
	pipe.Expire(ctx, key, 8*24*time.Hour)

	// Track this visor in today's online set
	pipe.SAdd(ctx, uptimeOnlineKey(date), pkHex)
	pipe.Expire(ctx, uptimeOnlineKey(date), 8*24*time.Hour)

	// Set the current 5-minute slot in the timeline bitmap.
	tlKey := uptimeTimelineKey(pkHex, date)
	pipe.SetBit(ctx, tlKey, currentTimelineSlot(now), 1)
	pipe.Expire(ctx, tlKey, 8*24*time.Hour)

	_, err := pipe.Exec(ctx)
	return err
}

// minP2PTransportsOnline is the minimum number of p2p transports (stcpr, sudph)
// a visor must have to be considered online. A visor with 2+ p2p transports is
// genuinely participating in the network with proven peer-to-peer reachability.
const minP2PTransportsOnline = 2

func (s *redisStore) GetAllVisorSummaries(ctx context.Context, v2 bool, timeline bool) ([]VisorSummary, error) {
	now := time.Now().UTC()
	today := now.Format("2006-01-02")

	// Get all visors that have been seen today (from heartbeats)
	heartbeatVisors, err := s.client.SMembers(ctx, uptimeOnlineKey(today)).Result()
	if err != nil {
		heartbeatVisors = nil
	}

	// Count p2p transports per visor for online determination.
	p2pCounts := s.getP2PTransportCounts(ctx)

	// Merge heartbeat visors and visors with p2p transports.
	allVisors := make(map[string]struct{})
	for _, pk := range heartbeatVisors {
		allVisors[pk] = struct{}{}
	}
	for pk := range p2pCounts {
		allVisors[pk] = struct{}{}
	}

	if len(allVisors) == 0 {
		return []VisorSummary{}, nil
	}

	result := make([]VisorSummary, 0, len(allVisors))

	for pkHex := range allVisors {
		var pk cipher.PubKey
		if err := pk.UnmarshalText([]byte(pkHex)); err != nil {
			continue
		}

		// Online = has 2+ p2p transports (stcpr/sudph).
		// This indicates genuine peer-to-peer network participation,
		// not just dmsg infrastructure connectivity.
		online := p2pCounts[pkHex] >= minP2PTransportsOnline

		// Get version from heartbeat data.
		version := ""
		key := uptimeKey(pkHex, today)
		vals, err := s.client.HMGet(ctx, key, "version").Result()
		if err == nil && len(vals) >= 1 {
			if v, ok := vals[0].(string); ok {
				version = v
			}
		}

		summary := VisorSummary{
			PK:      pk,
			Online:  online,
			Version: version,
		}

		// Add daily history for v2+
		if v2 || timeline {
			summary.Daily = s.getDailyUptime(ctx, pkHex, now)
		}

		// Add timeline bitmaps for v3
		if timeline {
			summary.Timeline = s.GetDailyTimeline(ctx, pkHex, now)
		}

		result = append(result, summary)
	}

	return result, nil
}

// getDailyUptime computes the last 7 days of uptime percentages for a visor.
func (s *redisStore) getDailyUptime(ctx context.Context, pkHex string, now time.Time) map[string]string {
	daily := make(map[string]string)

	for i := 0; i < uptimeHistoryDays; i++ {
		date := now.AddDate(0, 0, -i).Format("2006-01-02")
		key := uptimeKey(pkHex, date)

		countStr, err := s.client.HGet(ctx, key, "count").Result()
		if err != nil {
			continue
		}
		count, err := strconv.ParseFloat(countStr, 64)
		if err != nil {
			continue
		}

		pct := (count / expectedHeartbeatsPerDay) * 100
		if pct > 100 {
			pct = 100
		}
		daily[date] = fmt.Sprintf("%.2f", pct)
	}

	return daily
}

// getDailyTimeline reads the timeline bitmap for each of the last 7 days and
// converts it to a 288-char string per day. '.' = heartbeat received in that
// 5-minute slot, ' ' = missed.
func (s *redisStore) GetDailyTimeline(ctx context.Context, pkHex string, now time.Time) map[string]string {
	timelines := make(map[string]string)

	for i := 0; i < uptimeHistoryDays; i++ {
		date := now.AddDate(0, 0, -i).Format("2006-01-02")
		tlKey := uptimeTimelineKey(pkHex, date)

		// Read all 288 bits. Redis GETBIT returns 0 for unset bits
		// and for keys that don't exist, so this is safe.
		var buf [timelineSlots]byte
		pipe := s.client.Pipeline()
		cmds := make([]*redis.IntCmd, timelineSlots)
		for slot := 0; slot < timelineSlots; slot++ {
			cmds[slot] = pipe.GetBit(ctx, tlKey, int64(slot))
		}
		_, err := pipe.Exec(ctx)
		if err != nil {
			continue
		}

		hasAny := false
		for slot := 0; slot < timelineSlots; slot++ {
			if cmds[slot].Val() == 1 {
				buf[slot] = '.'
				hasAny = true
			} else {
				buf[slot] = ' '
			}
		}

		// Only include days that have at least one heartbeat.
		if hasAny {
			timelines[date] = string(buf[:])
		}
	}

	return timelines
}

/*
	<<< Transport uptime tracking (stcpr/sudph only) >>>
*/

func tpUptimeKey(tpID string, date string) string {
	return fmt.Sprintf("%s:tp-uptime:%s:%s", serviceName, tpID, date)
}

func tpUptimeOnlineKey(date string) string {
	return fmt.Sprintf("%s:tp-uptime:online:%s", serviceName, date)
}

func tpUptimeTimelineKey(tpID string, date string) string {
	return fmt.Sprintf("%s:tp-uptime:%s:%s:timeline", serviceName, tpID, date)
}

func (s *redisStore) RecordTransportHeartbeat(ctx context.Context, tpID uuid.UUID, tpType string) error {
	// Only track p2p transport types.
	if tpType != "stcpr" && tpType != "sudph" {
		return nil
	}

	now := time.Now().UTC()
	date := now.Format("2006-01-02")
	idStr := tpID.String()
	key := tpUptimeKey(idStr, date)

	pipe := s.client.Pipeline()

	pipe.HIncrBy(ctx, key, "count", 1)
	pipe.HSet(ctx, key, "type", tpType)
	pipe.HSet(ctx, key, "last_seen", now.Unix())
	pipe.Expire(ctx, key, 8*24*time.Hour)

	pipe.SAdd(ctx, tpUptimeOnlineKey(date), idStr)
	pipe.Expire(ctx, tpUptimeOnlineKey(date), 8*24*time.Hour)

	tlKey := tpUptimeTimelineKey(idStr, date)
	pipe.SetBit(ctx, tlKey, currentTimelineSlot(now), 1)
	pipe.Expire(ctx, tlKey, 8*24*time.Hour)

	_, err := pipe.Exec(ctx)
	return err
}

func (s *redisStore) GetTransportUptimeSummaries(ctx context.Context, tpIDs []uuid.UUID, v2 bool, timeline bool) ([]TransportUptimeSummary, error) {
	now := time.Now().UTC()
	today := now.Format("2006-01-02")

	// If no specific IDs requested, get all seen today.
	if len(tpIDs) == 0 {
		members, err := s.client.SMembers(ctx, tpUptimeOnlineKey(today)).Result()
		if err != nil {
			return []TransportUptimeSummary{}, nil
		}
		for _, m := range members {
			if id, perr := uuid.Parse(m); perr == nil {
				tpIDs = append(tpIDs, id)
			}
		}
	}

	result := make([]TransportUptimeSummary, 0, len(tpIDs))
	for _, id := range tpIDs {
		idStr := id.String()
		key := tpUptimeKey(idStr, today)

		// Check online: has a heartbeat today and the transport entry still exists.
		online := false
		tpType := ""
		vals, err := s.client.HMGet(ctx, key, "last_seen", "type").Result()
		if err == nil && len(vals) >= 2 {
			if lastStr, ok := vals[0].(string); ok {
				if lastSeen, perr := strconv.ParseInt(lastStr, 10, 64); perr == nil {
					if now.Sub(time.Unix(lastSeen, 0)) < onlineThresholdTP {
						online = true
					}
				}
			}
			if t, ok := vals[1].(string); ok {
				tpType = t
			}
		}

		summary := TransportUptimeSummary{
			ID:     id,
			Online: online,
			Type:   tpType,
		}

		if v2 || timeline {
			summary.Daily = s.getTransportDailyUptime(ctx, idStr, now)
		}
		if timeline {
			summary.Timeline = s.GetTransportDailyTimeline(ctx, idStr, now)
		}

		result = append(result, summary)
	}

	return result, nil
}

// onlineThresholdTP is how long since last heartbeat a transport is still considered online.
// ~3 missed 90s re-registrations.
const onlineThresholdTP = 5 * time.Minute

func (s *redisStore) GetTransportUptimeByVisor(ctx context.Context, pk cipher.PubKey, v2 bool, timeline bool) ([]TransportUptimeSummary, error) {
	// Get all transport IDs for this visor from the edge index.
	entries, err := s.GetTransportsByEdge(ctx, pk)
	if err != nil {
		return nil, err
	}

	ids := make([]uuid.UUID, 0, len(entries))
	for _, e := range entries {
		if e.Type == "stcpr" || e.Type == "sudph" {
			ids = append(ids, e.ID)
		}
	}

	return s.GetTransportUptimeSummaries(ctx, ids, v2, timeline)
}

func (s *redisStore) getTransportDailyUptime(ctx context.Context, tpID string, now time.Time) map[string]string {
	daily := make(map[string]string)
	for i := 0; i < uptimeHistoryDays; i++ {
		date := now.AddDate(0, 0, -i).Format("2006-01-02")
		countStr, err := s.client.HGet(ctx, tpUptimeKey(tpID, date), "count").Result()
		if err != nil {
			continue
		}
		count, err := strconv.ParseFloat(countStr, 64)
		if err != nil {
			continue
		}
		pct := (count / expectedHeartbeatsPerDay) * 100
		if pct > 100 {
			pct = 100
		}
		daily[date] = fmt.Sprintf("%.2f", pct)
	}
	return daily
}

func (s *redisStore) GetTransportDailyTimeline(ctx context.Context, tpID string, now time.Time) map[string]string {
	timelines := make(map[string]string)
	for i := 0; i < uptimeHistoryDays; i++ {
		date := now.AddDate(0, 0, -i).Format("2006-01-02")
		tlKey := tpUptimeTimelineKey(tpID, date)

		pipe := s.client.Pipeline()
		cmds := make([]*redis.IntCmd, timelineSlots)
		for slot := 0; slot < timelineSlots; slot++ {
			cmds[slot] = pipe.GetBit(ctx, tlKey, int64(slot))
		}
		_, err := pipe.Exec(ctx)
		if err != nil {
			continue
		}

		var buf [timelineSlots]byte
		hasAny := false
		for slot := 0; slot < timelineSlots; slot++ {
			if cmds[slot].Val() == 1 {
				buf[slot] = '.'
				hasAny = true
			} else {
				buf[slot] = ' '
			}
		}
		if hasAny {
			timelines[date] = string(buf[:])
		}
	}
	return timelines
}

// getP2PTransportCounts returns a map of visor PK hex → count of p2p transports
// (stcpr, sudph). A visor is considered online when it has 2+ p2p transports,
// indicating genuine peer-to-peer network participation (not just dmsg
// infrastructure connectivity).
func (s *redisStore) getP2PTransportCounts(ctx context.Context) map[string]int {
	keys, ids, err := s.allTransportKeysFromIndex(ctx)
	if err != nil {
		return nil
	}

	counts := make(map[string]int)
	var stale []interface{}

	const mgetBatch = 10000
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
			// Only count p2p transport types (stcpr, sudph), not dmsg.
			if data.Type == "stcpr" || data.Type == "sudph" {
				counts[data.EdgeA]++
				if data.EdgeA != data.EdgeB {
					counts[data.EdgeB]++
				}
			}
		}
	}
	s.maybeReapStaleTransports(stale)

	return counts
}

func (s *redisStore) getBandwidthFromHash(ctx context.Context, key, transportID, period, periodKey string) (BandwidthAggregation, error) {
	result, err := s.client.HGetAll(ctx, key).Result()
	if err != nil || len(result) == 0 {
		return BandwidthAggregation{}, errors.New("not found")
	}

	agg := BandwidthAggregation{
		TransportID: transportID,
		Period:      period,
		PeriodKey:   periodKey,
	}

	if val, ok := result["bandwidth"]; ok {
		if _, err := fmt.Sscanf(val, "%d", &agg.Bandwidth); err != nil {
			return BandwidthAggregation{}, fmt.Errorf("failed to parse bandwidth value %q: %w", val, err)
		}
	}
	if val, ok := result["updated_at"]; ok {
		if _, err := fmt.Sscanf(val, "%d", &agg.UpdatedAt); err != nil {
			return BandwidthAggregation{}, fmt.Errorf("failed to parse updated_at value %q: %w", val, err)
		}
	}

	return agg, nil
}

// GetNetworkMetrics returns network-wide aggregate metrics.
// Uses Redis pipelining for efficient bulk fetching.
func (s *redisStore) GetNetworkMetrics(ctx context.Context, query MetricsQuery) (*NetworkMetricResponse, error) {
	days := query.Days
	if days <= 0 {
		days = 35 // All available
	}
	if days > 35 {
		days = 35
	}

	now := time.Now().UTC()
	response := &NetworkMetricResponse{
		Daily:      make([]DailyAggregate, 0, days),
		Cumulative: &CumulativeAggregate{ByType: make(map[string]*TypeMetricAggregate)},
	}

	// Get all transports to aggregate (with QoS data for latency)
	entries, err := s.getAllTransportsWithQoS(ctx, true)
	if err != nil && !errors.Is(err, ErrTransportNotFound) {
		return nil, err
	}

	if len(entries) == 0 {
		return response, nil
	}

	// Build all bandwidth keys and fetch via pipeline
	type bwKey struct {
		dayIdx   int
		entryIdx int
		tpType   string
		dateStr  string
	}
	var bwKeys []bwKey
	var bwResults []*redis.StringStringMapCmd

	pipe := s.client.Pipeline()
	for d := 0; d < days; d++ {
		t := now.AddDate(0, 0, -d)
		dateStr := t.Format("2006-01-02")
		for i, entry := range entries {
			key := s.bandwidthDailyKey(entry.ID.String(), t)
			bwKeys = append(bwKeys, bwKey{
				dayIdx:   d,
				entryIdx: i,
				tpType:   string(entry.Type),
				dateStr:  dateStr,
			})
			bwResults = append(bwResults, pipe.HGetAll(ctx, key))
		}
	}
	_, _ = pipe.Exec(ctx) //nolint:errcheck

	// Process results: aggregate by day
	type dayData struct {
		agg          DailyAggregate
		latencySum   float64
		latencyCount int
		typeLatSum   map[string]float64
		typeLatCount map[string]int
	}
	dayMap := make(map[int]*dayData)

	for i, bk := range bwKeys {
		result, err := bwResults[i].Result()
		if err != nil || len(result) == 0 {
			continue
		}

		var bw uint64
		if val, ok := result["bandwidth"]; ok {
			fmt.Sscanf(val, "%d", &bw) //nolint:errcheck,gosec
		}
		if bw == 0 {
			continue
		}

		// Initialize day data if needed
		if dayMap[bk.dayIdx] == nil {
			dayMap[bk.dayIdx] = &dayData{
				agg: DailyAggregate{
					Date:   bk.dateStr,
					ByType: make(map[string]*TypeMetricAggregate),
				},
				typeLatSum:   make(map[string]float64),
				typeLatCount: make(map[string]int),
			}
		}
		dd := dayMap[bk.dayIdx]

		if query.Bandwidth {
			dd.agg.Bandwidth += bw
			if dd.agg.ByType[bk.tpType] == nil {
				dd.agg.ByType[bk.tpType] = &TypeMetricAggregate{}
			}
			dd.agg.ByType[bk.tpType].Bandwidth += bw

			response.Cumulative.Bandwidth += bw
			if response.Cumulative.ByType[bk.tpType] == nil {
				response.Cumulative.ByType[bk.tpType] = &TypeMetricAggregate{}
			}
			response.Cumulative.ByType[bk.tpType].Bandwidth += bw
		}

		// Track latency for proper averaging
		if query.Latency {
			entry := entries[bk.entryIdx]
			if entry.Latency > 0 {
				dd.latencySum += entry.Latency
				dd.latencyCount++
				dd.typeLatSum[bk.tpType] += entry.Latency
				dd.typeLatCount[bk.tpType]++
			}
		}
	}

	// Compute averages and build response
	for d := 0; d < days; d++ {
		dd, ok := dayMap[d]
		if !ok {
			continue
		}

		// Compute proper latency averages
		if dd.latencyCount > 0 {
			dd.agg.Latency = dd.latencySum / float64(dd.latencyCount)
		}
		for tpType, sum := range dd.typeLatSum {
			if count := dd.typeLatCount[tpType]; count > 0 {
				if dd.agg.ByType[tpType] == nil {
					dd.agg.ByType[tpType] = &TypeMetricAggregate{}
				}
				dd.agg.ByType[tpType].Latency = sum / float64(count)
			}
		}

		if dd.agg.Bandwidth > 0 || dd.agg.Latency > 0 {
			response.Daily = append(response.Daily, dd.agg)
		}
	}

	return response, nil
}

// GetVisorAggregateMetrics returns aggregate metrics for specified visors.
func (s *redisStore) GetVisorAggregateMetrics(ctx context.Context, pks []cipher.PubKey, query MetricsQuery) (map[string]*VisorMetricResponse, error) {
	days := query.Days
	if days <= 0 {
		days = 35
	}
	if days > 35 {
		days = 35
	}

	now := time.Now().UTC()
	result := make(map[string]*VisorMetricResponse)

	for _, pk := range pks {
		pkHex := pk.Hex()
		visorResp := &VisorMetricResponse{
			Daily:      make([]DailyVisorAggregate, 0, days),
			Cumulative: &VisorCumulativeAggregate{},
		}

		var totalSent, totalRecv uint64

		// Calculate latency once (it's a point-in-time metric, not per-day)
		var avgLatency float64
		if query.Latency {
			entries, _ := s.GetTransportsByEdge(ctx, pk) //nolint:errcheck
			var latencySum float64
			var latencyCount int
			for _, entry := range entries {
				if entry.Latency > 0 {
					latencySum += entry.Latency
					latencyCount++
				}
			}
			if latencyCount > 0 {
				avgLatency = latencySum / float64(latencyCount)
			}
		}

		// Pipeline all daily bandwidth lookups for this visor
		type bwCmd struct {
			dateStr string
			cmd     *redis.StringStringMapCmd
		}
		var bwCmds []bwCmd
		if query.Bandwidth {
			pipe := s.client.Pipeline()
			for d := 0; d < days; d++ {
				t := now.AddDate(0, 0, -d)
				bwCmds = append(bwCmds, bwCmd{
					dateStr: t.Format("2006-01-02"),
					cmd:     pipe.HGetAll(ctx, s.visorBandwidthDailyKey(pkHex, t)),
				})
			}
			_, _ = pipe.Exec(ctx) //nolint:errcheck
		}

		for d := 0; d < days; d++ {
			dailyAgg := DailyVisorAggregate{}

			if d < len(bwCmds) {
				dailyAgg.Date = bwCmds[d].dateStr
				bwResult, err := bwCmds[d].cmd.Result()
				if err == nil && len(bwResult) > 0 {
					var bw uint64
					if val, ok := bwResult["bandwidth"]; ok {
						fmt.Sscanf(val, "%d", &bw) //nolint:errcheck,gosec
					}
					if bw > 0 {
						dailyAgg.Bandwidth = &VisorBandwidthAggregate{
							Sent:  bw / 2,
							Recv:  bw / 2,
							Total: bw,
						}
						totalSent += bw / 2
						totalRecv += bw / 2
					}
				}
			} else {
				t := now.AddDate(0, 0, -d)
				dailyAgg.Date = t.Format("2006-01-02")
			}

			if query.Latency {
				dailyAgg.Latency = avgLatency
			}

			if dailyAgg.Bandwidth != nil || dailyAgg.Latency > 0 {
				visorResp.Daily = append(visorResp.Daily, dailyAgg)
			}
		}

		if query.Bandwidth && (totalSent > 0 || totalRecv > 0) {
			visorResp.Cumulative.Bandwidth = &VisorBandwidthAggregate{
				Sent:  totalSent,
				Recv:  totalRecv,
				Total: totalSent + totalRecv,
			}
		}
		if query.Latency && avgLatency > 0 {
			visorResp.Cumulative.Latency = avgLatency
		}

		result[pkHex] = visorResp
	}

	return result, nil
}

// GetAllTransportMetrics returns metrics for all transports.
func (s *redisStore) GetAllTransportMetrics(ctx context.Context, query MetricsQuery) ([]TransportMetric, error) {
	entries, err := s.getAllTransportsWithQoS(ctx, true)
	if err != nil {
		if errors.Is(err, ErrTransportNotFound) {
			return []TransportMetric{}, nil
		}
		return nil, err
	}

	return s.buildTransportMetrics(ctx, entries, nil, query)
}

// GetTransportMetricsByIDs returns metrics for specific transports.
func (s *redisStore) GetTransportMetricsByIDs(ctx context.Context, ids []uuid.UUID, query MetricsQuery) ([]TransportMetric, error) {
	var entries []*transport.Entry
	for _, id := range ids {
		entry, err := s.GetTransportByID(ctx, id)
		if err != nil {
			continue // Skip not found
		}
		entries = append(entries, entry)
	}

	return s.buildTransportMetrics(ctx, entries, nil, query)
}

// GetTransportMetricsByVisors returns metrics for transports of specified visors.
func (s *redisStore) GetTransportMetricsByVisors(ctx context.Context, pks []cipher.PubKey, query MetricsQuery) ([]TransportMetric, error) {
	// Collect all unique transports for these visors
	seen := make(map[uuid.UUID]bool)
	var entries []*transport.Entry

	for _, pk := range pks {
		visorEntries, err := s.GetTransportsByEdge(ctx, pk)
		if err != nil {
			continue
		}
		for _, entry := range visorEntries {
			if !seen[entry.ID] {
				seen[entry.ID] = true
				entries = append(entries, entry)
			}
		}
	}

	return s.buildTransportMetrics(ctx, entries, nil, query)
}

// buildTransportMetrics builds TransportMetric slice from entries.
// Uses Redis pipelining for efficient bulk fetching.
func (s *redisStore) buildTransportMetrics(ctx context.Context, entries []*transport.Entry, expiredIDs map[uuid.UUID]bool, query MetricsQuery) ([]TransportMetric, error) {
	days := query.Days
	if days <= 0 {
		days = 35
	}
	if days > 35 {
		days = 35
	}

	now := time.Now().UTC()

	// First pass: filter entries and prepare for bulk fetch
	type filteredEntry struct {
		entry  *transport.Entry
		isLive bool
	}
	var filtered []filteredEntry

	for _, entry := range entries {
		// Apply type filter
		if query.Type != "" && string(entry.Type) != query.Type {
			continue
		}

		// Apply live filter
		isLive := expiredIDs == nil || !expiredIDs[entry.ID]
		switch query.Live {
		case "true":
			if !isLive {
				continue
			}
		case "false":
			if isLive {
				continue
			}
		}

		filtered = append(filtered, filteredEntry{entry: entry, isLive: isLive})
	}

	if len(filtered) == 0 {
		return []TransportMetric{}, nil
	}

	// Fetch latency data via pipeline
	var latencyResults []*redis.StringCmd
	if query.Latency {
		pipe := s.client.Pipeline()
		latencyResults = make([]*redis.StringCmd, len(filtered))
		for i, f := range filtered {
			latencyResults[i] = pipe.Get(ctx, s.transportKey(f.entry.ID))
		}
		_, _ = pipe.Exec(ctx) //nolint:errcheck // Errors handled per-command via Result()
	}

	// Fetch bandwidth data via pipeline
	type bwKey struct {
		idx     int
		dayIdx  int
		dateStr string
	}
	var bwKeys []bwKey
	var bwResults []*redis.StringStringMapCmd

	if query.Bandwidth {
		pipe := s.client.Pipeline()
		for i, f := range filtered {
			for d := 0; d < days; d++ {
				t := now.AddDate(0, 0, -d)
				dateStr := t.Format("2006-01-02")
				key := s.bandwidthDailyKey(f.entry.ID.String(), t)
				bwKeys = append(bwKeys, bwKey{idx: i, dayIdx: d, dateStr: dateStr})
				bwResults = append(bwResults, pipe.HGetAll(ctx, key))
			}
		}
		_, _ = pipe.Exec(ctx) //nolint:errcheck // Errors handled per-command via Result()
	}

	// Build bandwidth lookup map: entryIdx -> []DailyEdgeBandwidth
	bwByEntry := make(map[int][]DailyEdgeBandwidth)
	for i, bk := range bwKeys {
		result, err := bwResults[i].Result()
		if err != nil || len(result) == 0 {
			continue
		}

		f := filtered[bk.idx]
		edgeAHex := f.entry.Edges[0].Hex()
		edgeBHex := f.entry.Edges[1].Hex()

		// Try to read per-reporter sent/recv fields (new format)
		var aSent, aRecv, bSent, bRecv uint64
		hasPerEdge := false
		if val, ok := result[edgeAHex+":sent"]; ok {
			fmt.Sscanf(val, "%d", &aSent) //nolint:errcheck,gosec
			hasPerEdge = true
		}
		if val, ok := result[edgeAHex+":recv"]; ok {
			fmt.Sscanf(val, "%d", &aRecv) //nolint:errcheck,gosec
			hasPerEdge = true
		}
		if val, ok := result[edgeBHex+":sent"]; ok {
			fmt.Sscanf(val, "%d", &bSent) //nolint:errcheck,gosec
			hasPerEdge = true
		}
		if val, ok := result[edgeBHex+":recv"]; ok {
			fmt.Sscanf(val, "%d", &bRecv) //nolint:errcheck,gosec
			hasPerEdge = true
		}

		if hasPerEdge && (aSent+aRecv+bSent+bRecv) > 0 {
			dailyMetric := DailyEdgeBandwidth{
				Date: bk.dateStr,
				A:    &EdgeBandwidth{Sent: aSent, Recv: aRecv},
				B:    &EdgeBandwidth{Sent: bSent, Recv: bRecv},
			}
			bwByEntry[bk.idx] = append(bwByEntry[bk.idx], dailyMetric)
		} else {
			// Fallback for old data: split combined total equally
			var bw uint64
			if val, ok := result["bandwidth"]; ok {
				fmt.Sscanf(val, "%d", &bw) //nolint:errcheck,gosec
			}
			if bw > 0 {
				halfBW := bw / 2
				dailyMetric := DailyEdgeBandwidth{
					Date: bk.dateStr,
					A:    &EdgeBandwidth{Sent: halfBW, Recv: halfBW},
					B:    &EdgeBandwidth{Sent: halfBW, Recv: halfBW},
				}
				bwByEntry[bk.idx] = append(bwByEntry[bk.idx], dailyMetric)
			}
		}
	}

	// Build results
	var results []TransportMetric
	for i, f := range filtered {
		metric := TransportMetric{
			ID:    f.entry.ID.String(),
			Type:  string(f.entry.Type),
			Live:  f.isLive,
			Daily: bwByEntry[i],
		}
		if metric.Daily == nil {
			metric.Daily = []DailyEdgeBandwidth{}
		}

		if query.Edges {
			metric.Edges = []string{f.entry.Edges[0].Hex(), f.entry.Edges[1].Hex()}
		}

		// Process latency result
		if query.Latency && latencyResults != nil {
			dataJSON, err := latencyResults[i].Result()
			if err == nil {
				var data TransportData
				if json.Unmarshal([]byte(dataJSON), &data) == nil && data.LatencyAvg > 0 {
					metric.Latency = &TransportLatency{
						Min: data.LatencyMin,
						Max: data.LatencyMax,
						Avg: data.LatencyAvg,
					}
				}
			}
		}

		// Skip transports without any metrics data
		if metric.Latency == nil && len(metric.Daily) == 0 {
			continue
		}

		results = append(results, metric)
	}

	if results == nil {
		return []TransportMetric{}, nil
	}
	return results, nil
}

// BackupAndCleanOldBandwidth writes day-8 visor bandwidth to .txt files and deletes the Redis keys.
// It also cleans up per-transport daily bandwidth keys older than 8 days.
// The method is idempotent — it only processes data if day-8 keys exist.
func (s *redisStore) BackupAndCleanOldBandwidth(ctx context.Context, backupPath string) error {
	if backupPath == "" {
		return nil
	}

	if err := os.MkdirAll(backupPath, 0o750); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}

	now := time.Now().UTC()
	day8 := now.AddDate(0, 0, -8)
	day8Str := day8.Format("2006-01-02")

	// Get all known visor PKs
	allPKHexes, err := s.client.SMembers(ctx, s.visorAllKey()).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return err
	}

	pipe := s.client.Pipeline()
	cmds := make([]*redis.StringStringMapCmd, len(allPKHexes))
	for i, pkHex := range allPKHexes {
		cmds[i] = pipe.HGetAll(ctx, s.visorBandwidthDailyKey(pkHex, day8))
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return err
	}

	delPipe := s.client.Pipeline()
	for i, pkHex := range allPKHexes {
		result, err := cmds[i].Result()
		if err != nil || len(result) == 0 {
			continue
		}

		var bw uint64
		if val, ok := result["bandwidth"]; ok {
			if _, err := fmt.Sscanf(val, "%d", &bw); err != nil {
				return fmt.Errorf("failed to parse bandwidth value %q: %w", val, err)
			}
		}
		if bw == 0 {
			// No bandwidth data, just delete the key
			delPipe.Del(ctx, s.visorBandwidthDailyKey(pkHex, day8))
			continue
		}

		// Append to per-visor .txt file
		filePath := filepath.Join(backupPath, pkHex+".txt")
		f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec
		if err != nil {
			s.log.WithError(err).WithField("pk", pkHex).Warn("Failed to open backup file")
			continue
		}
		_, writeErr := fmt.Fprintf(f, "%s %d\n", day8Str, bw)
		closeErr := f.Close()
		if writeErr != nil || closeErr != nil {
			s.log.WithField("pk", pkHex).Warn("Failed to write/close backup file")
			continue
		}

		// Delete the Redis key
		delPipe.Del(ctx, s.visorBandwidthDailyKey(pkHex, day8))
	}

	// Clean up per-transport daily bandwidth keys older than 8 days
	oldPattern := fmt.Sprintf("%s:bw:daily:*:%s", serviceName, day8Str)
	iter := s.client.Scan(ctx, 0, oldPattern, 10000).Iterator()
	for iter.Next(ctx) {
		delPipe.Del(ctx, iter.Val())
	}

	if _, err := delPipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return err
	}

	return nil
}
