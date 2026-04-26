package store

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/netutil"
)

const (
	serviceName = "transport-discovery"
)

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
func (s *redisStore) Close() {
	if err := s.client.Close(); err != nil {
		s.log.WithError(err).Warn("Failed to close Redis client")
	}
}

const timelineSlots = 24 * 60 / 5
const uptimeHistoryDays = 7
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

const minP2PTransportsOnline = 2
const onlineThresholdTP = 5 * time.Minute
