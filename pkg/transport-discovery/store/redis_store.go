package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
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
	SentBytes  uint64 `json:"sent_bytes"`  // Cumulative sent bytes
	RecvBytes  uint64 `json:"recv_bytes"`  // Cumulative recv bytes
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
		data.SentBytes = sEntry.Bandwidth.SentBytes
		data.RecvBytes = sEntry.Bandwidth.RecvBytes

		// Update bandwidth aggregations
		if err := s.updateBandwidth(ctx, entry.ID.String(),
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
		ID:      id,
		Edges:   [2]cipher.PubKey{edgeA, edgeB},
		Type:    types.Type(data.Type),
		Label:   transport.Label(data.Label),
		Latency: data.Latency,
	}, nil
}

// Bandwidth key generators
func (s *redisStore) bandwidthPrevKey(tpID string) string {
	return fmt.Sprintf("%s:bw:prev:%s", serviceName, tpID)
}

func (s *redisStore) bandwidthDailyKey(tpID string, t time.Time) string {
	return fmt.Sprintf("%s:bw:daily:%s:%s", serviceName, tpID, t.Format("2006-01-02"))
}

func (s *redisStore) bandwidthWeeklyKey(tpID string, year, week int) string {
	return fmt.Sprintf("%s:bw:weekly:%s:%d-W%02d", serviceName, tpID, year, week)
}

func (s *redisStore) bandwidthMonthlyKey(tpID string, year, month int) string {
	return fmt.Sprintf("%s:bw:monthly:%s:%d-%02d", serviceName, tpID, year, month)
}

// updateBandwidth calculates deltas and updates bandwidth aggregations.
func (s *redisStore) updateBandwidth(ctx context.Context, transportID string,
	currentSent, currentRecv uint64) error {

	now := time.Now().UTC()

	// 1. Get previous snapshot to calculate delta
	prevKey := s.bandwidthPrevKey(transportID)
	prevData, err := s.client.Get(ctx, prevKey).Result()

	var deltaSent, deltaRecv uint64
	if err == nil {
		var prev struct {
			Sent uint64 `json:"sent"`
			Recv uint64 `json:"recv"`
		}
		if jsonErr := json.Unmarshal([]byte(prevData), &prev); jsonErr == nil {
			// Calculate delta (handle counter reset on visor restart)
			if currentSent >= prev.Sent {
				deltaSent = currentSent - prev.Sent
			} else {
				deltaSent = currentSent // Counter reset, use full value
			}
			if currentRecv >= prev.Recv {
				deltaRecv = currentRecv - prev.Recv
			} else {
				deltaRecv = currentRecv
			}
		}
	}
	// If error (first time or key expired), no delta to add

	// 2. Store current as previous for next calculation
	prevSnapshot, _ := json.Marshal(map[string]uint64{"sent": currentSent, "recv": currentRecv})

	pipe := s.client.Pipeline()
	pipe.Set(ctx, prevKey, prevSnapshot, 5*time.Minute)

	// 3. Add deltas to aggregations (only if we have deltas)
	if deltaSent > 0 || deltaRecv > 0 {
		// Daily (35 days retention)
		dailyKey := s.bandwidthDailyKey(transportID, now)
		pipe.HIncrBy(ctx, dailyKey, "total_sent", int64(deltaSent))
		pipe.HIncrBy(ctx, dailyKey, "total_recv", int64(deltaRecv))
		pipe.HSet(ctx, dailyKey, "updated_at", now.Unix())
		pipe.Expire(ctx, dailyKey, 35*24*time.Hour)

		// Weekly (60 days retention)
		year, week := now.ISOWeek()
		weeklyKey := s.bandwidthWeeklyKey(transportID, year, week)
		pipe.HIncrBy(ctx, weeklyKey, "total_sent", int64(deltaSent))
		pipe.HIncrBy(ctx, weeklyKey, "total_recv", int64(deltaRecv))
		pipe.HSet(ctx, weeklyKey, "updated_at", now.Unix())
		pipe.Expire(ctx, weeklyKey, 60*24*time.Hour)

		// Monthly (400 days retention)
		monthlyKey := s.bandwidthMonthlyKey(transportID, now.Year(), int(now.Month()))
		pipe.HIncrBy(ctx, monthlyKey, "total_sent", int64(deltaSent))
		pipe.HIncrBy(ctx, monthlyKey, "total_recv", int64(deltaRecv))
		pipe.HSet(ctx, monthlyKey, "updated_at", now.Unix())
		pipe.Expire(ctx, monthlyKey, 400*24*time.Hour)
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
	case "weekly":
		for i := 0; i < limit; i++ {
			t := now.AddDate(0, 0, -i*7)
			year, week := t.ISOWeek()
			key := s.bandwidthWeeklyKey(transportID, year, week)
			periodKey := fmt.Sprintf("%d-W%02d", year, week)
			agg, err := s.getBandwidthFromHash(ctx, key, transportID, "weekly", periodKey)
			if err == nil {
				results = append(results, agg)
			}
		}
	case "monthly":
		for i := 0; i < limit; i++ {
			t := now.AddDate(0, -i, 0)
			key := s.bandwidthMonthlyKey(transportID, t.Year(), int(t.Month()))
			periodKey := fmt.Sprintf("%d-%02d", t.Year(), int(t.Month()))
			agg, err := s.getBandwidthFromHash(ctx, key, transportID, "monthly", periodKey)
			if err == nil {
				results = append(results, agg)
			}
		}
	default:
		return nil, fmt.Errorf("invalid period: %s", period)
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
				existing.TotalSent += bw.TotalSent
				existing.TotalRecv += bw.TotalRecv
				if bw.UpdatedAt > existing.UpdatedAt {
					existing.UpdatedAt = bw.UpdatedAt
				}
			} else {
				aggregatedByPeriod[bw.PeriodKey] = &BandwidthAggregation{
					TransportID: pk.Hex(), // Use visor PK as identifier
					Period:      period,
					PeriodKey:   bw.PeriodKey,
					TotalSent:   bw.TotalSent,
					TotalRecv:   bw.TotalRecv,
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

	if val, ok := result["total_sent"]; ok {
		fmt.Sscanf(val, "%d", &agg.TotalSent)
	}
	if val, ok := result["total_recv"]; ok {
		fmt.Sscanf(val, "%d", &agg.TotalRecv)
	}
	if val, ok := result["updated_at"]; ok {
		fmt.Sscanf(val, "%d", &agg.UpdatedAt)
	}

	return agg, nil
}
