// Package store pkg/service-discovery/store/redis_store.go
package store

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	jsoniter "github.com/json-iterator/go"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/servicedisc"
)

var json = jsoniter.ConfigFastest

const (
	serviceKeyPrefix   = "service:"
	serviceTypesSetKey = "service_types"
)

type redisStore struct {
	log    logrus.FieldLogger
	client *redis.Client
	ttl    time.Duration

	done     chan struct{}
	doneOnce sync.Once
}

func newRedisStore(ctx context.Context, client *redis.Client, logger *logging.Logger, ttl time.Duration) (*redisStore, error) {
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis connection check failed: %w", err)
	}

	s := &redisStore{
		log:    logger,
		client: client,
		ttl:    ttl,
		done:   make(chan struct{}),
	}

	// Start background cleanup of expired set members
	go s.cleanupExpiredServices(ctx)

	return s, nil
}

// cleanupExpiredServices periodically removes expired service keys from the sets
func (s *redisStore) cleanupExpiredServices(ctx context.Context) {
	ticker := time.NewTicker(s.ttl / 2)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.done:
			return
		case <-ticker.C:
			s.removeExpiredFromSets(ctx)
		}
	}
}

// removeExpiredFromSets checks all service type sets and removes members whose keys have expired
func (s *redisStore) removeExpiredFromSets(ctx context.Context) {
	// Get all service types
	serviceTypes, err := s.client.SMembers(ctx, serviceTypesSetKey).Result()
	if err != nil {
		s.log.WithError(err).Warn("Failed to get service types for cleanup")
		return
	}

	for _, sType := range serviceTypes {
		setKey := s.serviceTypeSetKey(sType)
		pubKeys, err := s.client.SMembers(ctx, setKey).Result()
		if err != nil {
			s.log.WithError(err).Warnf("Failed to get members of set %s", setKey)
			continue
		}

		for _, pk := range pubKeys {
			key := fmt.Sprintf("%s%s:%s", serviceKeyPrefix, sType, pk)
			exists, err := s.client.Exists(ctx, key).Result()
			if err != nil {
				continue
			}
			if exists == 0 {
				// Key expired, remove from set
				s.client.SRem(ctx, setKey, pk)
				s.log.Debugf("Removed expired service %s from set %s", pk, setKey)
			}
		}

		// If set is empty, remove it from service_types
		count, _ := s.client.SCard(ctx, setKey).Result() //nolint: errcheck
		if count == 0 {
			s.client.SRem(ctx, serviceTypesSetKey, sType)
		}
	}
}

func (s *redisStore) serviceKey(sType string, addr servicedisc.SWAddr) string {
	return fmt.Sprintf("%s%s:%s", serviceKeyPrefix, sType, addr.PubKey().String())
}

func (s *redisStore) serviceTypeSetKey(sType string) string {
	return fmt.Sprintf("%s%s", serviceKeyPrefix, sType)
}

// visorServicesKey is the per-visor index set of service types the
// visor currently has registered. Members are plain type strings
// (e.g. "vpn", "skysocks", "visor"); combined with the PK they rebuild
// the full serviceKey for MGET.
func (s *redisStore) visorServicesKey(pkHex string) string {
	return "sd:visor-svc:" + pkHex
}

func (s *redisStore) Service(ctx context.Context, sType string, addr servicedisc.SWAddr) (*servicedisc.Service, *servicedisc.HTTPError) {
	key := s.serviceKey(sType, addr)

	data, err := s.client.Get(ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, &servicedisc.HTTPError{
				HTTPStatus: http.StatusNotFound,
				Err:        "service not found",
			}
		}
		return nil, s.processErr(err, http.StatusInternalServerError)
	}

	var service servicedisc.Service
	if err := json.Unmarshal(data, &service); err != nil {
		return nil, s.processErr(err, http.StatusInternalServerError)
	}

	return &service, nil
}

func (s *redisStore) Services(ctx context.Context, sType, version, country string) ([]servicedisc.Service, *servicedisc.HTTPError) {
	setKey := s.serviceTypeSetKey(sType)

	pubKeys, err := s.client.SMembers(ctx, setKey).Result()
	if err != nil {
		return nil, s.processErr(err, http.StatusInternalServerError)
	}

	if len(pubKeys) == 0 {
		return []servicedisc.Service{}, nil
	}

	var keys []string
	for _, pk := range pubKeys {
		keys = append(keys, fmt.Sprintf("%s%s:%s", serviceKeyPrefix, sType, pk))
	}

	values, err := s.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, s.processErr(err, http.StatusInternalServerError)
	}

	var records []servicedisc.Service
	for i, val := range values {
		if val == nil {
			// Key expired, clean up set (async)
			if i < len(pubKeys) {
				go s.client.SRem(context.Background(), setKey, pubKeys[i]) //nolint:gosec
			}
			continue
		}

		var service servicedisc.Service
		if err := json.Unmarshal([]byte(val.(string)), &service); err != nil {
			s.log.WithError(err).Warn("Failed to unmarshal service")
			continue
		}

		if version != "" && service.Version != version {
			continue
		}
		if country != "" && (service.Geo == nil || service.Geo.Country != country) {
			continue
		}

		if !service.DisplayNodeIP {
			service.LocalIPs = nil
		}
		service.DisplayNodeIP = false

		records = append(records, service)
	}

	return records, nil
}

func (s *redisStore) UpdateService(ctx context.Context, se *servicedisc.Service) *servicedisc.HTTPError {
	key := s.serviceKey(se.Type, se.Addr)
	setKey := s.serviceTypeSetKey(se.Type)

	data, err := json.Marshal(se)
	if err != nil {
		return s.processErr(err, http.StatusInternalServerError)
	}

	// Always apply TTL so stale services expire when clients stop
	// re-registering. The prior behavior skipped TTL on first-time
	// registrations "for backward compatibility with old visors",
	// which let crashed / offline clients accumulate in Redis
	// forever — same anti-pattern as the dmsg-discovery bug fixed
	// in #2305. Clients refresh their service entry every 90s (see
	// skyenv.ServiceDiscUpdateInterval), so a TTL >= ~2× refresh
	// (configured via --entry-timeout, default 5m) gives safe
	// margin for dropped or slow refreshes.
	pkHex := se.Addr.PubKey().Hex()
	pipe := s.client.Pipeline()
	pipe.Set(ctx, key, data, s.ttl)
	pipe.SAdd(ctx, setKey, se.Addr.PubKey().String())
	pipe.SAdd(ctx, serviceTypesSetKey, se.Type)
	pipe.SAdd(ctx, s.visorServicesKey(pkHex), se.Type)

	if _, err := pipe.Exec(ctx); err != nil {
		return s.processErr(err, http.StatusInternalServerError)
	}

	return nil
}

// UpdateServiceAndHeartbeat combines UpdateService + RecordHeartbeat into
// a single Redis pipeline. This halves the Redis round-trips per service
// registration (from 2 pipelines to 1).
func (s *redisStore) UpdateServiceAndHeartbeat(ctx context.Context, se *servicedisc.Service, version string) *servicedisc.HTTPError {
	key := s.serviceKey(se.Type, se.Addr)
	setKey := s.serviceTypeSetKey(se.Type)

	data, err := json.Marshal(se)
	if err != nil {
		return s.processErr(err, http.StatusInternalServerError)
	}

	now := time.Now().UTC()
	date := now.Format("2006-01-02")
	pkHex := se.Addr.PubKey().Hex()

	pipe := s.client.Pipeline()

	// Service update (4 commands — adds per-visor type index)
	pipe.Set(ctx, key, data, s.ttl)
	pipe.SAdd(ctx, setKey, se.Addr.PubKey().String())
	pipe.SAdd(ctx, serviceTypesSetKey, se.Type)
	pipe.SAdd(ctx, s.visorServicesKey(pkHex), se.Type)

	// Heartbeat (8 commands)
	uptimeKey := sdUptimeKey(pkHex, date)
	pipe.HIncrBy(ctx, uptimeKey, "count", 1)
	pipe.HSet(ctx, uptimeKey, "version", version)
	pipe.HSet(ctx, uptimeKey, "last_seen", now.Unix())
	pipe.Expire(ctx, uptimeKey, 8*24*time.Hour)

	onlineKey := sdUptimeOnlineKey(date)
	pipe.SAdd(ctx, onlineKey, pkHex)
	pipe.Expire(ctx, onlineKey, 8*24*time.Hour)

	tlKey := sdUptimeTimelineKey(pkHex, date)
	pipe.SetBit(ctx, tlKey, currentTimelineSlot(now), 1)
	pipe.Expire(ctx, tlKey, 8*24*time.Hour)

	if _, err := pipe.Exec(ctx); err != nil {
		return s.processErr(err, http.StatusInternalServerError)
	}

	return nil
}

func (s *redisStore) DeleteService(ctx context.Context, sType string, addr servicedisc.SWAddr) *servicedisc.HTTPError {
	key := s.serviceKey(sType, addr)
	setKey := s.serviceTypeSetKey(sType)
	pubKey := addr.PubKey().String()

	pipe := s.client.Pipeline()
	pipe.Del(ctx, key)
	pipe.SRem(ctx, setKey, pubKey)
	pipe.SRem(ctx, s.visorServicesKey(addr.PubKey().Hex()), sType)

	if _, err := pipe.Exec(ctx); err != nil {
		return s.processErr(err, http.StatusInternalServerError)
	}

	return nil
}

// ServicesByPK returns every currently-registered service for the given
// visor PK. Backed by the sd:visor-svc:<pkHex> index set maintained by
// UpdateService / UpdateServiceAndHeartbeat / DeleteService. Types
// whose primary service key has expired via TTL are silently skipped;
// the caller can treat an empty result as "no active services" and
// should not interpret it as an error.
func (s *redisStore) ServicesByPK(ctx context.Context, pk cipher.PubKey) ([]servicedisc.Service, *servicedisc.HTTPError) {
	pkHex := pk.Hex()
	types, err := s.client.SMembers(ctx, s.visorServicesKey(pkHex)).Result()
	if err != nil {
		return nil, s.processErr(err, http.StatusInternalServerError)
	}
	if len(types) == 0 {
		return nil, nil
	}
	addr := servicedisc.NewSWAddr(pk, 0)
	keys := make([]string, 0, len(types))
	for _, t := range types {
		keys = append(keys, s.serviceKey(t, addr))
	}
	vals, err := s.client.MGet(ctx, keys...).Result()
	if err != nil {
		return nil, s.processErr(err, http.StatusInternalServerError)
	}
	out := make([]servicedisc.Service, 0, len(vals))
	for i, v := range vals {
		raw, ok := v.(string)
		if !ok {
			// Primary entry expired; evict the stale index member so
			// the next ServicesByPK doesn't keep reporting it.
			s.client.SRem(ctx, s.visorServicesKey(pkHex), types[i]) //nolint:errcheck
			continue
		}
		var svc servicedisc.Service
		if err := json.Unmarshal([]byte(raw), &svc); err != nil {
			continue
		}
		out = append(out, svc)
	}
	return out, nil
}

func (s *redisStore) CountServiceTypes(ctx context.Context) (uint64, error) {
	count, err := s.client.SCard(ctx, serviceTypesSetKey).Result()
	if err != nil {
		return 0, fmt.Errorf("Redis command returned unexpected error: %w", err)
	}

	if count < 0 {
		return 0, nil
	}
	return uint64(count), nil
}

func (s *redisStore) CountServices(ctx context.Context, serviceType string) (uint64, error) {
	setKey := s.serviceTypeSetKey(serviceType)

	count, err := s.client.SCard(ctx, setKey).Result()
	if err != nil {
		return 0, fmt.Errorf("Redis command returned unexpected error: %w", err)
	}

	if count < 0 {
		return 0, nil
	}
	return uint64(count), nil
}

//nolint:unparam
func (s *redisStore) processErr(err error, status int) *servicedisc.HTTPError {
	if err != nil {
		return &servicedisc.HTTPError{
			HTTPStatus: status,
			Err:        err.Error(),
		}
	}
	return nil
}

func (s *redisStore) Close() (err error) {
	s.doneOnce.Do(func() {
		close(s.done)
	})
	return s.client.Close()
}

// ---- Uptime tracking ----

const (
	sdServiceName            = "sd"
	uptimeHistoryDays        = 7
	expectedHeartbeatsPerDay = float64(24*60*60) / float64(90) // 960
	timelineSlots            = 24 * 60 / 5                     // 288
)

// sdUptimeKey returns the Redis key for a visor's heartbeat hash on a given date.
// Format: "sd:uptime:{pk}:{date}"
func sdUptimeKey(pk, date string) string {
	return fmt.Sprintf("%s:uptime:%s:%s", sdServiceName, pk, date)
}

// sdUptimeOnlineKey returns the Redis key for the daily set of seen visors.
// Format: "sd:uptime:online:{date}"
func sdUptimeOnlineKey(date string) string {
	return fmt.Sprintf("%s:uptime:online:%s", sdServiceName, date)
}

// sdUptimeTimelineKey returns the Redis key for a visor's 288-bit timeline bitmap.
// Format: "sd:uptime:{pk}:{date}:timeline"
func sdUptimeTimelineKey(pk, date string) string {
	return fmt.Sprintf("%s:uptime:%s:%s:timeline", sdServiceName, pk, date)
}

// currentTimelineSlot returns the 0-based 5-minute slot index for the given UTC time (0–287).
func currentTimelineSlot(t time.Time) int64 {
	return int64(t.Hour()*12 + t.Minute()/5)
}

// RecordHeartbeat records a visor heartbeat for uptime tracking.
// Each call increments the daily counter, updates the version/last_seen hash
// fields, adds the PK to today's online set, and sets the current 5-min
// timeline slot.
func (s *redisStore) RecordHeartbeat(ctx context.Context, pk cipher.PubKey, version string) error {
	now := time.Now().UTC()
	date := now.Format("2006-01-02")
	pkHex := pk.Hex()
	key := sdUptimeKey(pkHex, date)

	pipe := s.client.Pipeline()

	pipe.HIncrBy(ctx, key, "count", 1)
	pipe.HSet(ctx, key, "version", version)
	pipe.HSet(ctx, key, "last_seen", now.Unix())
	pipe.Expire(ctx, key, 8*24*time.Hour)

	onlineKey := sdUptimeOnlineKey(date)
	pipe.SAdd(ctx, onlineKey, pkHex)
	pipe.Expire(ctx, onlineKey, 8*24*time.Hour)

	tlKey := sdUptimeTimelineKey(pkHex, date)
	pipe.SetBit(ctx, tlKey, currentTimelineSlot(now), 1)
	pipe.Expire(ctx, tlKey, 8*24*time.Hour)

	_, err := pipe.Exec(ctx)
	return err
}

// GetAllVisorSummaries returns uptime summaries for all visors seen today via
// heartbeats.  Online status = UpdateService succeeded (service entry exists).
// v2 adds version + daily uptime percentages; timeline (v3) is added by the
// API layer on-demand via GetDailyTimeline.
func (s *redisStore) GetAllVisorSummaries(ctx context.Context, v2 bool, timeline bool) ([]VisorSummary, error) {
	now := time.Now().UTC()
	today := now.Format("2006-01-02")

	// All visors seen via heartbeats today.
	pkHexes, err := s.client.SMembers(ctx, sdUptimeOnlineKey(today)).Result()
	if err != nil {
		pkHexes = nil
	}

	if len(pkHexes) == 0 {
		return []VisorSummary{}, nil
	}

	result := make([]VisorSummary, 0, len(pkHexes))

	for _, pkHex := range pkHexes {
		var pk cipher.PubKey
		if err := pk.UnmarshalText([]byte(pkHex)); err != nil {
			continue
		}

		// Online = service entry currently exists in Redis (UpdateService sets it
		// with a TTL; it expires when the visor stops sending heartbeats).
		// We check for any service type that uses this PK by searching for any
		// service key pattern.
		online := s.isServiceOnline(ctx, pkHex)

		// Fetch version from today's heartbeat hash.
		version := ""
		key := sdUptimeKey(pkHex, today)
		if vals, err := s.client.HMGet(ctx, key, "version").Result(); err == nil && len(vals) >= 1 {
			if v, ok := vals[0].(string); ok {
				version = v
			}
		}

		summary := VisorSummary{
			PK:      pk,
			Online:  online,
			Version: version,
		}

		if v2 || timeline {
			summary.Daily = s.getDailyUptime(ctx, pkHex, now)
		}

		if timeline {
			summary.Timeline = s.GetDailyTimeline(ctx, pkHex, now)
		}

		result = append(result, summary)
	}

	return result, nil
}

// isServiceOnline checks whether any live service entry exists for the given PK hex.
// A service entry exists when the visor has recently called UpdateService (within TTL).
//
// The previous implementation walked the entire keyspace via SCAN cursor=0
// pattern="service:*:<pk>" COUNT=10. With ~150K keys in production, an
// offline visor's check meant ~15K SCAN round-trips per call — and this is
// invoked once per visor seen today inside refreshUptimesCache, which fires
// every 5 minutes for both v1 and v2 caches. Production redis was spending
// ~33% of its CPU on SCAN alone (~3316 calls/sec).
//
// Now: consult the per-visor service-type index that UpdateService /
// UpdateServiceAndHeartbeat / DeleteService already maintain (see
// visorServicesKey + ServicesByPK). Build the actual service keys and
// resolve in a single multi-key EXISTS. The index can lag primary-key
// TTLs; an existing index member with no live primary just returns false
// here, and ServicesByPK's lazy SREM cleans it up on the next read.
// O(1) Redis round-trips per call regardless of keyspace size.
func (s *redisStore) isServiceOnline(ctx context.Context, pkHex string) bool {
	types, err := s.client.SMembers(ctx, s.visorServicesKey(pkHex)).Result()
	if err != nil || len(types) == 0 {
		return false
	}
	keys := make([]string, len(types))
	for i, t := range types {
		keys[i] = fmt.Sprintf("%s%s:%s", serviceKeyPrefix, t, pkHex)
	}
	n, err := s.client.Exists(ctx, keys...).Result()
	if err != nil {
		return false
	}
	return n > 0
}

// getDailyUptime returns the last 7 days of uptime percentages for a visor.
func (s *redisStore) getDailyUptime(ctx context.Context, pkHex string, now time.Time) map[string]string {
	daily := make(map[string]string)

	for i := 0; i < uptimeHistoryDays; i++ {
		date := now.AddDate(0, 0, -i).Format("2006-01-02")
		key := sdUptimeKey(pkHex, date)

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

// GetDailyTimeline reads the 288-bit timeline bitmap for each of the last 7 days.
// '.' = heartbeat received in that 5-minute slot, ' ' = missed.
// Only days with at least one heartbeat are included.
func (s *redisStore) GetDailyTimeline(ctx context.Context, pkHex string, now time.Time) map[string]string {
	timelines := make(map[string]string)

	for i := 0; i < uptimeHistoryDays; i++ {
		date := now.AddDate(0, 0, -i).Format("2006-01-02")
		tlKey := sdUptimeTimelineKey(pkHex, date)

		var buf [timelineSlots]byte
		pipe := s.client.Pipeline()
		cmds := make([]*redis.IntCmd, timelineSlots)
		for slot := 0; slot < timelineSlots; slot++ {
			cmds[slot] = pipe.GetBit(ctx, tlKey, int64(slot))
		}
		if _, err := pipe.Exec(ctx); err != nil {
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

		if hasAny {
			timelines[date] = string(buf[:])
		}
	}

	return timelines
}
