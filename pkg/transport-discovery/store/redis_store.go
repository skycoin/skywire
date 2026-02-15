package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	Latency    int64  `json:"latency"`     // Latency in milliseconds, updated on each re-register
	Bandwidth  uint64 `json:"bandwidth"`   // Total bytes (sent + recv)
	LastUpdate int64  `json:"last_update"` // Unix timestamp of last update
}

type redisStore struct {
	client *redis.Client
	ttl    time.Duration
	log    *logging.Logger
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

	return &redisStore{client: redisCl, ttl: ttl, log: logger}, nil
}

func (s *redisStore) RegisterTransport(ctx context.Context, sEntry *transport.SignedEntry) error {
	return s.RegisterTransportWithLatency(ctx, sEntry, sEntry.Latency)
}

// RegisterTransportWithLatency registers transport with latency value.
func (s *redisStore) RegisterTransportWithLatency(ctx context.Context, sEntry *transport.SignedEntry, latency int64) error {
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
		Latency:    latency,
		LastUpdate: now.Unix(),
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

	pipe := s.client.Pipeline()
	pipe.Set(ctx, tpKey, string(raw), s.ttl)
	pipe.SAdd(ctx, edgeAKey, entry.ID.String())
	pipe.Expire(ctx, edgeAKey, s.ttl)
	if entry.Edges[0] != entry.Edges[1] {
		pipe.SAdd(ctx, edgeBKey, entry.ID.String())
		pipe.Expire(ctx, edgeBKey, s.ttl)
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return err
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
	if entry.Edges[0] != entry.Edges[1] {
		pipe.SRem(ctx, edgeBKey, id.String())
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}

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
	edgeKey := s.edgeKey(pk)

	ids, err := s.client.SMembers(ctx, edgeKey).Result()
	if err != nil {
		return nil, err
	}

	if len(ids) == 0 {
		return nil, ErrTransportNotFound
	}

	var entries []*transport.Entry
	for _, idStr := range ids {
		id, err := uuid.Parse(idStr)
		if err != nil {
			continue
		}

		entry, err := s.GetTransportByID(ctx, id)
		if err != nil {
			// Transport expired, remove from edge set
			if errors.Is(err, ErrTransportNotFound) {
				s.client.SRem(ctx, edgeKey, idStr)
			}
			continue
		}
		entries = append(entries, entry)
	}

	if len(entries) == 0 {
		return nil, ErrTransportNotFound
	}

	return entries, nil
}

func (s *redisStore) GetNumberOfTransports(ctx context.Context) (map[types.Type]int, error) {
	response := map[types.Type]int{
		types.STCP:  0,
		types.STCPR: 0,
		types.SUDPH: 0,
		types.DMSG:  0,
	}

	pattern := fmt.Sprintf("%s:tp:*", serviceName)
	iter := s.client.Scan(ctx, 0, pattern, 10000).Iterator()

	for iter.Next(ctx) {
		key := iter.Val()
		raw, err := s.client.Get(ctx, key).Result()
		if err != nil {
			continue
		}

		var data TransportData
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			continue
		}
		response[types.Type(data.Type)]++
	}

	return response, iter.Err()
}

func (s *redisStore) GetAllTransports(ctx context.Context, selfTransports bool) ([]*transport.Entry, error) {
	var entries []*transport.Entry

	pattern := fmt.Sprintf("%s:tp:*", serviceName)
	iter := s.client.Scan(ctx, 0, pattern, 10000).Iterator()

	for iter.Next(ctx) {
		key := iter.Val()
		raw, err := s.client.Get(ctx, key).Result()
		if err != nil {
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

		if !selfTransports && entry.Edges[0] == entry.Edges[1] {
			continue
		}

		entries = append(entries, entry)
	}

	return entries, iter.Err()
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

func (s *redisStore) dataToEntry(data TransportData) (*transport.Entry, error) {
	id, err := uuid.Parse(data.ID)
	if err != nil {
		return nil, err
	}

	var edgeA, edgeB cipher.PubKey
	if err := edgeA.UnmarshalText([]byte(data.EdgeA)); err != nil {
		return nil, err
	}
	if err := edgeB.UnmarshalText([]byte(data.EdgeB)); err != nil {
		return nil, err
	}

	return &transport.Entry{
		ID:        id,
		Edges:     [2]cipher.PubKey{edgeA, edgeB},
		Type:      types.Type(data.Type),
		Label:     transport.Label(data.Label),
		Latency:   data.Latency,
		Bandwidth: data.Bandwidth,
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

// updateBandwidth calculates the bandwidth delta (sent + recv combined) and
// updates per-transport and per-visor aggregation hashes in Redis.
// The prev snapshot is keyed per-reporter to avoid cross-contamination when both
// edges of a transport register independently.
func (s *redisStore) updateBandwidth(ctx context.Context, transportID string,
	reporterPK cipher.PubKey, currentSent, currentRecv uint64) error {

	now := time.Now().UTC()
	reporterHex := reporterPK.Hex()
	currentBW := currentSent + currentRecv

	// 1. Get previous snapshot (per-reporter) to calculate delta
	prevKey := s.bandwidthPrevKey(transportID, reporterHex)
	prevBW, err := s.client.Get(ctx, prevKey).Uint64()

	var delta uint64
	if err == nil {
		// Previous snapshot exists — compute delta
		if currentBW >= prevBW {
			delta = currentBW - prevBW
		} else {
			delta = currentBW // Counter reset, use full value
		}
	} else {
		// First time or key expired — use full current value as delta
		// so we don't lose bandwidth accumulated before this point
		delta = currentBW
	}

	// 2. Store current as previous for next calculation
	// TTL of 10 minutes allows for missed re-registration cycles (every 90s)
	pipe := s.client.Pipeline()
	pipe.Set(ctx, prevKey, currentBW, 10*time.Minute)

	// 3. Add delta to aggregations (only if we have a delta)
	if delta > 0 {
		deltaI := int64(delta)

		// Per-transport daily aggregation
		dailyKey := s.bandwidthDailyKey(transportID, now)
		pipe.HIncrBy(ctx, dailyKey, "bandwidth", deltaI)
		pipe.HSet(ctx, dailyKey, "updated_at", now.Unix())
		pipe.Expire(ctx, dailyKey, 35*24*time.Hour)

		// Per-visor daily aggregation — only for the reporter
		pipe.SAdd(ctx, s.visorAllKey(), reporterHex)
		pipe.Expire(ctx, s.visorAllKey(), 400*24*time.Hour)

		vDaily := s.visorBandwidthDailyKey(reporterHex, now)
		pipe.HIncrBy(ctx, vDaily, "bandwidth", deltaI)
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

	// Get all transports for this visor
	entries, err := s.GetTransportsByEdge(ctx, pk)
	if err != nil {
		return nil, err
	}

	// Aggregate bandwidth from all transports
	aggregatedByPeriod := make(map[string]*BandwidthAggregation)

	for _, entry := range entries {
		tpBandwidth, err := s.GetTransportBandwidth(ctx, entry.ID, period, limit)
		if err != nil {
			continue
		}

		for _, bw := range tpBandwidth {
			if existing, ok := aggregatedByPeriod[bw.PeriodKey]; ok {
				existing.Bandwidth += bw.Bandwidth
				if bw.UpdatedAt > existing.UpdatedAt {
					existing.UpdatedAt = bw.UpdatedAt
				}
			} else {
				aggregatedByPeriod[bw.PeriodKey] = &BandwidthAggregation{
					TransportID: pk.Hex(), // Use visor PK as identifier
					Period:      period,
					PeriodKey:   bw.PeriodKey,
					Bandwidth:   bw.Bandwidth,
					UpdatedAt:   bw.UpdatedAt,
				}
			}
		}
	}

	// Convert map to slice
	var results []BandwidthAggregation
	for _, agg := range aggregatedByPeriod {
		results = append(results, *agg)
	}

	return results, nil
}

// GetAllVisorSummaries discovers all known visors (including offline ones with
// historical bandwidth), fetches visor-level aggregated bandwidth, and determines
// online status by scanning active transports.
func (s *redisStore) GetAllVisorSummaries(ctx context.Context) ([]VisorSummary, error) {
	// Phase 1: Get all known visor PKs from the global set
	allPKHexes, err := s.client.SMembers(ctx, s.visorAllKey()).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}

	// Phase 2: SCAN active tp:* keys to determine online visors and transport counts
	onlineVisors := make(map[string]int) // pkHex -> transport count
	pattern := fmt.Sprintf("%s:tp:*", serviceName)
	iter := s.client.Scan(ctx, 0, pattern, 10000).Iterator()

	for iter.Next(ctx) {
		key := iter.Val()
		raw, err := s.client.Get(ctx, key).Result()
		if err != nil {
			continue
		}

		var data TransportData
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			continue
		}

		onlineVisors[data.EdgeA]++
		if data.EdgeA != data.EdgeB {
			onlineVisors[data.EdgeB]++
		}
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}

	// Merge online visor PKs into the full set (in case visor:all set is stale)
	allPKSet := make(map[string]struct{}, len(allPKHexes))
	for _, pkHex := range allPKHexes {
		allPKSet[pkHex] = struct{}{}
	}
	for pkHex := range onlineVisors {
		allPKSet[pkHex] = struct{}{}
	}

	if len(allPKSet) == 0 {
		return []VisorSummary{}, nil
	}

	// Phase 3: Pipeline HGetAll for last 7 days of visor-level daily bandwidth
	now := time.Now().UTC()
	const daysToFetch = 7

	pkList := make([]string, 0, len(allPKSet))
	for pkHex := range allPKSet {
		pkList = append(pkList, pkHex)
	}

	pipe := s.client.Pipeline()
	// cmdMap[i][d] = HGetAll cmd for pkList[i] on day d (0=today, 6=6 days ago)
	cmdMap := make([][daysToFetch]*redis.StringStringMapCmd, len(pkList))

	for i, pkHex := range pkList {
		for d := 0; d < daysToFetch; d++ {
			t := now.AddDate(0, 0, -d)
			cmdMap[i][d] = pipe.HGetAll(ctx, s.visorBandwidthDailyKey(pkHex, t))
		}
	}

	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}

	// Phase 4: Assemble VisorSummary slices
	result := make([]VisorSummary, 0, len(pkList))

	for i, pkHex := range pkList {
		var pk cipher.PubKey
		if err := pk.UnmarshalText([]byte(pkHex)); err != nil {
			continue
		}

		tpCount := onlineVisors[pkHex]
		summary := VisorSummary{
			PK:              pk,
			Online:          tpCount > 0,
			TransportCount:  tpCount,
			DailyBandwidths: []DailyBandwidthEntry{},
		}

		for d := 0; d < daysToFetch; d++ {
			t := now.AddDate(0, 0, -d)
			dateStr := t.Format("2006-01-02")
			bwResult, err := cmdMap[i][d].Result()
			if err != nil || len(bwResult) == 0 {
				continue
			}
			var bw uint64
			if val, ok := bwResult["bandwidth"]; ok {
				fmt.Sscanf(val, "%d", &bw) //nolint:errcheck
			}
			if bw > 0 {
				summary.DailyBandwidths = append(summary.DailyBandwidths, DailyBandwidthEntry{
					Date:      dateStr,
					Bandwidth: bw,
				})
			}
		}

		result = append(result, summary)
	}

	return result, nil
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
		fmt.Sscanf(val, "%d", &agg.Bandwidth) //nolint:errcheck
	}
	if val, ok := result["updated_at"]; ok {
		fmt.Sscanf(val, "%d", &agg.UpdatedAt) //nolint:errcheck
	}

	return agg, nil
}

// BackupAndCleanOldBandwidth writes day-8 visor bandwidth to .txt files and deletes the Redis keys.
// It also cleans up per-transport daily bandwidth keys older than 8 days.
// The method is idempotent — it only processes data if day-8 keys exist.
func (s *redisStore) BackupAndCleanOldBandwidth(ctx context.Context, backupPath string) error {
	if backupPath == "" {
		return nil
	}

	if err := os.MkdirAll(backupPath, 0o755); err != nil {
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
			fmt.Sscanf(val, "%d", &bw) //nolint:errcheck
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
