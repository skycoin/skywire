// Package store pkg/discovery/store/redis.go
package store

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"
	jsoniter "github.com/json-iterator/go"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/netutil"
)

var json = jsoniter.ConfigFastest

// Entry cache bounds. 2048 entries easily covers the hot-key working
// set (dmsg servers + active visors) at ~200KB. 5s TTL is short enough
// that changes to a visor's DelegatedServers list propagate promptly
// yet long enough to absorb the repeated lookups that dominate read
// traffic. Writes invalidate proactively so TTL is a ceiling on
// staleness, not the normal case.
const (
	entryCacheSize = 2048
	entryCacheTTL  = 5 * time.Second
)

type redisStore struct {
	client  *redis.Client
	timeout time.Duration
	cache   *entryCache
}

func newRedis(ctx context.Context, url, password string, timeout time.Duration, log *logging.Logger) (Storer, error) {
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	opt.Password = password

	client := redis.NewClient(opt)

	err = netutil.NewRetrier(log, netutil.DefaultInitBackoff, netutil.DefaultMaxBackoff, 10, netutil.DefaultFactor).Do(ctx, func() error {
		_, err = client.Ping(ctx).Result()
		return err
	})
	if err != nil {
		return nil, err
	}
	return &redisStore{
		client:  client,
		timeout: timeout,
		cache:   newEntryCache(entryCacheSize, entryCacheTTL),
	}, nil
}

// Entry implements Storer Entry method for redisdb database
func (r *redisStore) Entry(ctx context.Context, staticPubKey cipher.PubKey) (*disc.Entry, error) {
	if entry, ok := r.cache.get(staticPubKey); ok {
		return entry, nil
	}

	payload, err := r.client.Get(ctx, staticPubKey.Hex()).Bytes()
	if err != nil {
		if err == redis.Nil {
			return nil, disc.ErrKeyNotFound
		}

		log.WithError(err).WithField("pk", staticPubKey).Errorf("Failed to get entry from redis")
		return nil, disc.ErrUnexpected
	}

	var entry *disc.Entry
	if err := json.Unmarshal(payload, &entry); err != nil {
		log.WithError(err).Warnf("Failed to unmarshal payload %q", payload)
	}

	r.cache.set(staticPubKey, entry)
	return entry, nil
}

// Entry implements Storer Entry method for redisdb database
func (r *redisStore) SetEntry(ctx context.Context, entry *disc.Entry, timeout time.Duration) error {
	payload, err := json.Marshal(entry)
	if err != nil {
		return disc.ErrUnexpected
	}

	if entry.Server != nil {
		timeout = dmsg.DefaultUpdateInterval * 2
	}

	err = r.client.Set(ctx, entry.Static.Hex(), payload, timeout).Err()
	if err != nil {
		log.WithError(err).Errorf("Failed to set entry in redis")
		return disc.ErrUnexpected
	}
	// Store the just-written entry in the cache. Previously this was
	// cache.invalidate, which forced the next reader to round-trip
	// Redis to refill the slot — pathological for hot PKs (dmsg-servers
	// repost on every session count change at 5-15 Hz; their entry is
	// also among the most-queried keys, so each post dropped the next
	// hundred-or-so reads through to Redis). Since SetEntry has the
	// canonical post-write value in hand and has just confirmed Redis
	// accepted it, populating the cache directly is both correct and
	// drastically more efficient. The cache TTL still bounds staleness
	// for the case where a Redis-side TTL silently expires the key.
	r.cache.set(entry.Static, entry)

	if entry.Server != nil {
		err = r.client.SAdd(ctx, "servers", entry.Static.Hex()).Err()
		if err != nil {
			log.WithError(err).Errorf("Failed to add to servers (SAdd) from redis")
			return disc.ErrUnexpected
		}
	}
	if entry.Client != nil {
		err = r.client.SAdd(ctx, "clients", entry.Static.Hex()).Err()
		if err != nil {
			log.WithError(err).Errorf("Failed to add to clients (SAdd) from redis")
			return disc.ErrUnexpected
		}
	}
	if entry.ClientType == "visor" {
		err = r.client.SAdd(ctx, "visorClients", entry.Static.Hex()).Err()
		if err != nil {
			log.WithError(err).Errorf("Failed to add to visorClients (SAdd) from redis")
			return disc.ErrUnexpected
		}
	}

	return nil
}

// DelEntry implements Storer DelEntry method for redisdb database
func (r *redisStore) DelEntry(ctx context.Context, staticPubKey cipher.PubKey) error {
	err := r.client.Del(ctx, staticPubKey.Hex()).Err()
	if err != nil {
		log.WithError(err).WithField("pk", staticPubKey).Errorf("Failed to delete entry from redis")
		return err
	}
	r.cache.invalidate(staticPubKey)
	// Delete pubkey from servers or clients set stored
	r.client.SRem(ctx, "servers", staticPubKey.Hex())
	r.client.SRem(ctx, "clients", staticPubKey.Hex())
	r.client.SRem(ctx, "visorClients", staticPubKey.Hex())
	return nil
}

// AvailableServers implements Storer AvailableServers method for redisdb database
func (r *redisStore) AvailableServers(ctx context.Context, maxCount int) ([]*disc.Entry, error) {
	var entries []*disc.Entry

	pks, err := r.client.SRandMemberN(ctx, "servers", int64(maxCount)).Result()
	if err != nil {
		log.WithError(err).Errorf("Failed to get servers (SRandMemberN) from redis")
		return nil, disc.ErrUnexpected
	}

	if len(pks) == 0 {
		return entries, nil
	}

	payloads, err := r.client.MGet(ctx, pks...).Result()
	if err != nil {
		log.WithError(err).Errorf("Failed to set servers (MGet) from redis")
		return nil, disc.ErrUnexpected
	}

	for _, payload := range payloads {
		// if there's no record for this PK, nil is returned. The below
		// type assertion will panic in this case, so we skip
		if payload == nil {
			continue
		}

		var entry *disc.Entry
		if err := json.Unmarshal([]byte(payload.(string)), &entry); err != nil {
			log.WithError(err).Warnf("Failed to unmarshal payload %s", payload.(string))
			continue
		}

		if entry.Server.AvailableSessions <= 0 {
			log.WithField("server_pk", entry.Static).
				Warn("Server is at max capacity. Skipping...")
			continue
		}

		entries = append(entries, entry)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Server.AvailableSessions > entries[j].Server.AvailableSessions
	})

	return entries, nil
}

// AllServers implements Storer AllServers method for redisdb database
func (r *redisStore) AllServers(ctx context.Context) ([]*disc.Entry, error) {
	var entries []*disc.Entry

	pks, err := r.client.SRandMemberN(ctx, "servers", r.client.SCard(ctx, "servers").Val()).Result()
	if err != nil {
		log.WithError(err).Errorf("Failed to get servers (SRandMemberN) from redis")
		return nil, disc.ErrUnexpected
	}

	if len(pks) == 0 {
		return entries, nil
	}

	payloads, err := r.client.MGet(ctx, pks...).Result()
	if err != nil {
		log.WithError(err).Errorf("Failed to set servers (MGet) from redis")
		return nil, disc.ErrUnexpected
	}

	for _, payload := range payloads {
		// if there's no record for this PK, nil is returned. The below
		// type assertion will panic in this case, so we skip
		if payload == nil {
			continue
		}

		var entry *disc.Entry
		if err := json.Unmarshal([]byte(payload.(string)), &entry); err != nil {
			log.WithError(err).Warnf("Failed to unmarshal payload %s", payload.(string))
			continue
		}

		entries = append(entries, entry)
	}

	return entries, nil
}

func (r *redisStore) CountEntries(ctx context.Context) (int64, int64, error) {
	numberOfServers, err := r.client.SCard(ctx, "servers").Result()
	if err != nil {
		log.WithError(err).Errorf("Failed to get servers count (SCard) from redis")
		return numberOfServers, int64(0), err
	}
	numberOfClients, err := r.client.SCard(ctx, "clients").Result()
	if err != nil {
		log.WithError(err).Errorf("Failed to get clients count (SCard) from redis")
		return numberOfServers, numberOfClients, err
	}

	return numberOfServers, numberOfClients, nil
}

func (r *redisStore) RemoveOldServerEntries(ctx context.Context) error {
	servers, err := r.client.SMembers(ctx, "servers").Result()
	if err != nil {
		return err
	}
	for _, server := range servers {
		if r.client.Exists(ctx, server).Val() == 0 {
			r.client.SRem(ctx, "servers", server)
		}
	}
	return nil
}

// RemoveStaleClientEntries scans the "clients" set and:
// - removes members whose Redis key no longer exists (expired via TTL)
// - applies defaultTTL to keys that have no expiration (pre-TTL legacy data)
// This is called periodically from RunBackgroundTasks to clean up entries
// that were written before the TTL feature was deployed.
func (r *redisStore) RemoveStaleClientEntries(ctx context.Context, defaultTTL time.Duration) (removed, ttlSet int, err error) {
	clients, err := r.client.SMembers(ctx, "clients").Result()
	if err != nil {
		return 0, 0, err
	}
	for _, pk := range clients {
		ttl, err := r.client.TTL(ctx, pk).Result()
		if err != nil {
			continue
		}
		switch {
		case ttl == -2:
			// Key does not exist — remove from set.
			r.client.SRem(ctx, "clients", pk) //nolint:errcheck
			removed++
		case ttl == -1 && defaultTTL > 0:
			// Key exists but has no expiration — set one.
			r.client.Expire(ctx, pk, defaultTTL) //nolint:errcheck
			ttlSet++
		}
	}
	return removed, ttlSet, nil
}

func (r *redisStore) AllEntries(ctx context.Context) ([]string, error) {
	clients, err := r.client.SMembers(ctx, "clients").Result()
	if err != nil {
		return nil, err
	}
	return clients, err
}

func (r *redisStore) AllVisorEntries(ctx context.Context) ([]string, error) {
	clients, err := r.client.SMembers(ctx, "visorClients").Result()
	if err != nil {
		return nil, err
	}
	return clients, err
}

func (r *redisStore) AllClientEntries(ctx context.Context) ([]*disc.Entry, error) {
	pks, err := r.client.SMembers(ctx, "clients").Result()
	if err != nil {
		log.WithError(err).Errorf("Failed to get clients (SMembers) from redis")
		return nil, disc.ErrUnexpected
	}

	if len(pks) == 0 {
		return nil, nil
	}

	payloads, err := r.client.MGet(ctx, pks...).Result()
	if err != nil {
		log.WithError(err).Errorf("Failed to get client entries (MGet) from redis")
		return nil, disc.ErrUnexpected
	}

	var entries []*disc.Entry
	for _, payload := range payloads {
		if payload == nil {
			continue
		}

		var entry *disc.Entry
		if err := json.Unmarshal([]byte(payload.(string)), &entry); err != nil {
			log.WithError(err).Warnf("Failed to unmarshal payload %s", payload.(string))
			continue
		}

		if entry.Client != nil {
			entries = append(entries, entry)
		}
	}

	return entries, nil
}

/*
	<<< Integrated uptime tracking >>>
*/

const (
	uptimeHistoryDays        = 7
	expectedHeartbeatsPerDay = float64(24*60) / float64(5) // 288 (5-min client update interval)
	timelineSlots            = 24 * 60 / 5                 // 288
)

func uptimeKey(pk string, date string) string {
	return "dmsgd:uptime:" + pk + ":" + date
}

func uptimeOnlineKey(date string) string {
	return "dmsgd:uptime:online:" + date
}

func uptimeTimelineKey(pk string, date string) string {
	return "dmsgd:uptime:" + pk + ":" + date + ":timeline"
}

func currentTimelineSlot(t time.Time) int64 {
	return int64(t.Hour()*12 + t.Minute()/5)
}

func (r *redisStore) RecordHeartbeat(ctx context.Context, pk cipher.PubKey, version string) error {
	now := time.Now().UTC()
	date := now.Format("2006-01-02")
	pkHex := pk.Hex()
	key := uptimeKey(pkHex, date)

	pipe := r.client.Pipeline()

	pipe.HIncrBy(ctx, key, "count", 1)
	pipe.HSet(ctx, key, "version", version)
	pipe.HSet(ctx, key, "last_seen", now.Unix())
	pipe.Expire(ctx, key, 8*24*time.Hour)

	pipe.SAdd(ctx, uptimeOnlineKey(date), pkHex)
	pipe.Expire(ctx, uptimeOnlineKey(date), 8*24*time.Hour)

	tlKey := uptimeTimelineKey(pkHex, date)
	pipe.SetBit(ctx, tlKey, currentTimelineSlot(now), 1)
	pipe.Expire(ctx, tlKey, 8*24*time.Hour)

	_, err := pipe.Exec(ctx)
	return err
}

func (r *redisStore) GetAllVisorSummaries(ctx context.Context, v2 bool, timeline bool) ([]VisorSummary, error) {
	now := time.Now().UTC()
	today := now.Format("2006-01-02")

	heartbeatVisors, err := r.client.SMembers(ctx, uptimeOnlineKey(today)).Result()
	if err != nil {
		heartbeatVisors = nil
	}

	if len(heartbeatVisors) == 0 {
		return []VisorSummary{}, nil
	}

	result := make([]VisorSummary, 0, len(heartbeatVisors))

	for _, pkHex := range heartbeatVisors {
		var pk cipher.PubKey
		if err := pk.UnmarshalText([]byte(pkHex)); err != nil {
			continue
		}

		// Online = has 1+ delegated servers in current dmsg discovery entry.
		online := false
		entry, err := r.Entry(ctx, pk)
		if err == nil && entry.Client != nil && len(entry.Client.DelegatedServers) > 0 {
			online = true
		}

		version := ""
		key := uptimeKey(pkHex, today)
		vals, err := r.client.HMGet(ctx, key, "version").Result()
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

		if v2 || timeline {
			summary.Daily = r.getDailyUptime(ctx, pkHex, now)
		}
		if timeline {
			summary.Timeline = r.GetDailyTimeline(ctx, pkHex, now)
		}

		result = append(result, summary)
	}

	return result, nil
}

func (r *redisStore) getDailyUptime(ctx context.Context, pkHex string, now time.Time) map[string]string {
	daily := make(map[string]string)

	// Pipeline all history days' HGet into ONE round-trip instead of
	// uptimeHistoryDays sequential calls (per-visor cost across ~1375 visors).
	dates := make([]string, uptimeHistoryDays)
	cmds := make([]*redis.StringCmd, uptimeHistoryDays)
	pipe := r.client.Pipeline()
	for i := 0; i < uptimeHistoryDays; i++ {
		dates[i] = now.AddDate(0, 0, -i).Format("2006-01-02")
		cmds[i] = pipe.HGet(ctx, uptimeKey(pkHex, dates[i]), "count")
	}
	// Missing keys surface as redis.Nil on the individual command; handled per
	// command below, so a non-nil Exec error (e.g. redis.Nil) is not fatal here.
	_, _ = pipe.Exec(ctx) //nolint:errcheck

	for i, cmd := range cmds {
		countStr, err := cmd.Result()
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
		daily[dates[i]] = fmt.Sprintf("%.2f", pct)
	}
	return daily
}

func (r *redisStore) GetDailyTimeline(ctx context.Context, pkHex string, now time.Time) map[string]string {
	timelines := make(map[string]string)

	// Fetch every history day's timeline bitmap in ONE MGET, then read the bits
	// locally, instead of uptimeHistoryDays × timelineSlots (288) individual
	// GETBIT commands. Each timeline is a Redis string bitmap (written with
	// SETBIT); GET/MGET returns the raw bytes and we extract the 288 bits in Go.
	// For the full network (~1375 visors × 7 days) the old path issued ~2.7M
	// GETBIT ops across ~9.6k pipeline round-trips and made GET /uptimes?v=v3
	// hang past 60s; this collapses it to one MGET per visor.
	keys := make([]string, uptimeHistoryDays)
	dates := make([]string, uptimeHistoryDays)
	for i := 0; i < uptimeHistoryDays; i++ {
		dates[i] = now.AddDate(0, 0, -i).Format("2006-01-02")
		keys[i] = uptimeTimelineKey(pkHex, dates[i])
	}

	vals, err := r.client.MGet(ctx, keys...).Result()
	if err != nil {
		return timelines
	}

	for i, v := range vals {
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		raw := []byte(s)
		var buf [timelineSlots]byte
		hasAny := false
		for slot := 0; slot < timelineSlots; slot++ {
			// Redis bit ordering: offset 0 is the MSB of byte 0 (matches GETBIT).
			byteIdx := slot / 8
			var bit byte
			if byteIdx < len(raw) {
				bit = (raw[byteIdx] >> (7 - uint(slot%8))) & 1
			}
			if bit == 1 {
				buf[slot] = '.'
				hasAny = true
			} else {
				buf[slot] = ' '
			}
		}
		if hasAny {
			timelines[dates[i]] = string(buf[:])
		}
	}
	return timelines
}
