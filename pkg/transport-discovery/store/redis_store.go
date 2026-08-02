// Package store pkg/transport-discovery/store/redis_store.go c4-net-discovery
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

// LatencyRecord is the durable per-transport latency snapshot persisted at
// transport-discovery:lat:<id>. It lives independently of the registration
// blob (TransportData) so a 5-min entry-timeout cycle doesn't erase
// latency the way it did when the values were stored only inside the tp:
// key. Bandwidth gets the same independence via bw:daily:* keys; this
// gives latency the equivalent durability so /metrics doesn't show a
// transport with bandwidth-today but no latency just because the visor's
// re-registration window briefly lapsed.
//
// Last-writer-wins on the per-transport key: latency is round-trip and
// both edges observe the same RTT modulo measurement noise, so no merge
// or per-edge tracking. Min/Max/Avg are stored in microseconds to match
// the existing TransportData layout. UpdatedAt is informational (lets
// readers gauge staleness) and not currently surfaced to the API.
type LatencyRecord struct {
	Min       int64 `json:"min"` // microseconds
	Max       int64 `json:"max"` // microseconds
	Avg       int64 `json:"avg"` // microseconds
	UpdatedAt int64 `json:"updated_at"`
}

// latencyTTL is how long a latency record sits in redis without being
// refreshed before it ages out. Mirrors bw:daily:* retention so the two
// telemetry types share the same observability window.
const latencyTTL = 35 * 24 * time.Hour

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

// expectedHeartbeatsPerDay is the denominator for TRANSPORT uptime: transports
// re-register on a ~90-second cadence (transport.Manager.runReRegisterTransports),
// so a continuously-registered transport lands ~960 RecordHeartbeat calls/day.
const expectedHeartbeatsPerDay = float64(24*60*60) / float64(90) // 960

// expectedVisorHeartbeatsPerDay is the denominator for VISOR uptime. A visor's
// dedicated presence heartbeat fires every 5 minutes (tickDuration in
// pkg/visor/init_services.go), so a continuously-up visor lands 288 heartbeats/
// day. Dividing visor uptime by the 90-second TRANSPORT figure (960) scored a
// continuously-up NON-hub visor at 288/960 = 30% — below the 75% reward bar —
// which is the v1.3.88+ fleet-wide reward-uptime regression (transport HUBS read
// 100% only because their frequent re-registration also calls RecordHeartbeat).
// Because the daily percentage is computed from the stored count at READ time,
// correcting the divisor also fixes every day still in Redis retroactively.
const expectedVisorHeartbeatsPerDay = float64(24*60*60) / float64(5*60) // 288

// RecordHeartbeat records a visor heartbeat for uptime tracking.
// Each heartbeat increments the daily counter and updates the version/last_seen.
// maxHeartbeatBackfillSlots caps how many missed 5-minute slots a single
// heartbeat backfills (see backfillStartSlot / RecordHeartbeat). 6 slots =
// 30 minutes: generous enough to cover a burst of dropped heartbeat ticks on a
// visor that was actually up, but small enough that a genuine longer absence is
// not credited.
const maxHeartbeatBackfillSlots = 6

// backfillStartSlot returns the first timeline slot a heartbeat at `now` should
// set, given the previous heartbeat time (`prev`, valid only when prevSeen>0).
// Normally that is just the current slot, but heartbeat DELIVERY is flaky (the
// visor's 5-min ticker drops ticks when its TPD auth contends with transport
// re-registration under load), so a continuously-up visor may only land a
// heartbeat every few slots. A heartbeat now plus one a short while ago means
// the visor was up across the gap, so backfill the missed slots — BOUNDED by
// maxHeartbeatBackfillSlots, and only within the same day (the timeline bitmap
// is per-day). A larger gap is treated as a real absence and NOT credited. This
// is what makes recorded uptime robust to under-delivery instead of collapsing
// to ~30%.
func backfillStartSlot(now, prev time.Time, prevSeen int64) int64 {
	curSlot := currentTimelineSlot(now)
	if prevSeen <= 0 || prev.Format("2006-01-02") != now.Format("2006-01-02") {
		return curSlot
	}
	if prevSlot := currentTimelineSlot(prev); prevSlot < curSlot && curSlot-prevSlot <= maxHeartbeatBackfillSlots {
		return prevSlot + 1 // prevSlot was already set by the earlier heartbeat
	}
	return curSlot
}

func (s *redisStore) RecordHeartbeat(ctx context.Context, pk cipher.PubKey, version string) error {
	now := time.Now().UTC()
	date := now.Format("2006-01-02")
	pkHex := pk.Hex()
	key := uptimeKey(pkHex, date)
	tlKey := uptimeTimelineKey(pkHex, date)
	curSlot := currentTimelineSlot(now)

	// Set the current slot, plus any bounded backfill of slots missed since the
	// previous heartbeat (flaky delivery — see backfillStartSlot).
	startSlot := curSlot
	if prevSeen, err := s.client.HGet(ctx, key, "last_seen").Int64(); err == nil {
		startSlot = backfillStartSlot(now, time.Unix(prevSeen, 0).UTC(), prevSeen)
	}

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

	// Set the current 5-minute slot (plus any bounded backfill) in the timeline
	// bitmap. SetBit is idempotent, so concurrent heartbeats for the same PK are
	// harmless.
	for slot := startSlot; slot <= curSlot; slot++ {
		pipe.SetBit(ctx, tlKey, slot, 1)
	}
	pipe.Expire(ctx, tlKey, 8*24*time.Hour)

	if _, err := pipe.Exec(ctx); err != nil {
		// Reward-critical: a persistent failure here silently drops the fleet's
		// uptime/presence data, which is exactly how a reward-uptime outage can
		// hide for days. Callers treat the heartbeat as best-effort and rely on
		// the store to surface failures, so log at Warn here rather than let it
		// vanish into a Debug line or a discarded error.
		s.log.WithError(err).Warn("RecordHeartbeat: failed to persist visor heartbeat (uptime/reward data lost for this tick)")
		return err
	}
	return nil
}

const minP2PTransportsOnline = 2
const onlineThresholdTP = 5 * time.Minute
