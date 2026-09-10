// Package store pkg/deployment/tpd/store/bandwidth_day_index.go c4-net-discovery
package store

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
)

// bwIndexMaxDays is the deepest day the index tracks: the 35-day TTL of the
// bw:daily hashes, so nothing older can exist.
const bwIndexMaxDays = 35

// bwDayIndex records, per transport, which UTC days in the history window
// have a bw:daily hash, learned from ONE SCAN of the keyspace, plus the edge
// pair recovered for every such transport. It answers two questions the
// metrics paths used to put to redis one key at a time: "which transports
// carried bandwidth in the last N days but are no longer registered" and
// "which of this transport's 30 daily hashes actually exist".
type bwDayIndex struct {
	// anchor is the UTC epoch day the scan ran on. Bit k of a transport's
	// mask means a hash exists for day anchor-k.
	anchor    int64
	byID      map[uuid.UUID]uint64
	edges     map[uuid.UUID][2]cipher.PubKey
	scannedAt time.Time
}

func epochDay(t time.Time) int64 { return t.UTC().Unix() / 86400 }

// dayBit maps a calendar day to its bit in the masks: (bit, true), or false
// when the day is after the scan (nothing was known about it) or before the
// window (nothing can exist).
func (ix *bwDayIndex) dayBit(t time.Time) (uint, bool) {
	k := ix.anchor - epochDay(t)
	if k < 0 || k >= 64 {
		return 0, false
	}
	return uint(k), true
}

// mayHave reports whether a bw:daily hash for id on day (now-offset) may
// exist: true for today and yesterday, which may have gained one since the
// scan (writes stamp the current UTC day of the writer, so a hash further back can
// only be one the scan already saw), and otherwise only when the scan saw
// it. A nil index knows nothing and answers true.
func (ix *bwDayIndex) mayHave(id uuid.UUID, day time.Time, offset int) bool {
	if ix == nil || offset <= 1 {
		return true
	}
	k, ok := ix.dayBit(day)
	return ok && ix.byID[id]&(1<<k) != 0
}

// fetchDays returns the day offsets d in [0,days) worth an HGETALL for id
// (see mayHave).
func (ix *bwDayIndex) fetchDays(id uuid.UUID, now time.Time, days int) []int {
	out := make([]int, 0, 4)
	for d := 0; d < days; d++ {
		if ix.mayHave(id, now.AddDate(0, 0, -d), d) {
			out = append(out, d)
		}
	}
	return out
}

// windowMask is the bit set covering days now-0 .. now-(days-1).
func (ix *bwDayIndex) windowMask(now time.Time, days int) uint64 {
	var m uint64
	for d := 0; d < days; d++ {
		if k, ok := ix.dayBit(now.AddDate(0, 0, -d)); ok {
			m |= 1 << k
		}
	}
	return m
}

// parseBWDailyKey splits "<prefix><id>:<date>" into its transport id and day.
func parseBWDailyKey(key, prefix string) (uuid.UUID, time.Time, bool) {
	rest := strings.TrimPrefix(key, prefix)
	i := strings.LastIndexByte(rest, ':')
	if i < 0 {
		return uuid.UUID{}, time.Time{}, false
	}
	id, err := uuid.Parse(rest[:i])
	if err != nil {
		return uuid.UUID{}, time.Time{}, false
	}
	t, err := time.Parse("2006-01-02", rest[i+1:])
	if err != nil {
		return uuid.UUID{}, time.Time{}, false
	}
	return id, t, true
}

// edgesFromDailyHash reconstructs a transport's edge public keys from a daily
// bandwidth hash whose fields are "<edgePK>:sent" / "<edgePK>:recv". Only the
// reporting edge(s) can be recovered this way; a single-reporter transport
// leaves the second edge zero-valued (its bandwidth fields simply won't match,
// contributing 0 — the total stays correct). False when the hash has only the
// legacy combined "bandwidth" field.
func edgesFromDailyHash(h map[string]string) ([2]cipher.PubKey, bool) {
	var edges [2]cipher.PubKey
	seenHex := make(map[string]bool)
	var hexes []string
	for field := range h {
		var hex string
		switch {
		case strings.HasSuffix(field, ":sent"):
			hex = strings.TrimSuffix(field, ":sent")
		case strings.HasSuffix(field, ":recv"):
			hex = strings.TrimSuffix(field, ":recv")
		default:
			continue
		}
		if hex == "" || seenHex[hex] {
			continue
		}
		seenHex[hex] = true
		hexes = append(hexes, hex)
	}
	if len(hexes) == 0 {
		return edges, false
	}
	sort.Strings(hexes)
	n := 0
	for _, hex := range hexes {
		if n >= 2 {
			break
		}
		var pk cipher.PubKey
		if err := pk.UnmarshalText([]byte(hex)); err != nil {
			continue
		}
		edges[n] = pk
		n++
	}
	return edges, n > 0
}

// bandwidthIndex returns the cached index, rebuilding it after bwIndexTTL.
func (s *redisStore) bandwidthIndex(ctx context.Context) *bwDayIndex {
	if ix, ok := s.bwIndex.get(); ok {
		return ix
	}
	ix := s.scanBandwidthIndex(ctx)
	s.bwIndex.put(ix)
	return ix
}

// scanBatch bounds one pipeline in the edge-recovery pass.
const scanBatch = 1000

// scanBandwidthIndex walks the bw:daily:* keyspace once and recovers the edge
// pair of every transport it finds: from the persisted bw:edges:<id> value
// (pipelined GETs), falling back to the field names of a daily hash the scan
// saw for transports registered before that value existed.
func (s *redisStore) scanBandwidthIndex(ctx context.Context) *bwDayIndex {
	started := time.Now()
	now := started.UTC()
	ix := &bwDayIndex{
		anchor:    epochDay(now),
		byID:      make(map[uuid.UUID]uint64),
		edges:     make(map[uuid.UUID][2]cipher.PubKey),
		scannedAt: started,
	}
	prefix := serviceName + ":bw:daily:"
	iter := s.client.Scan(ctx, 0, prefix+"*", 10000).Iterator()
	for iter.Next(ctx) {
		id, day, ok := parseBWDailyKey(iter.Val(), prefix)
		if !ok {
			continue
		}
		if k, ok := ix.dayBit(day); ok {
			ix.byID[id] |= 1 << k
		}
	}
	if err := iter.Err(); err != nil {
		s.log.WithError(err).Warn("bandwidth index scan failed; index is partial")
	}

	ids := make([]uuid.UUID, 0, len(ix.byID))
	for id := range ix.byID {
		ids = append(ids, id)
	}
	var fallback []uuid.UUID
	for start := 0; start < len(ids); start += scanBatch {
		end := start + scanBatch
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		pipe := s.client.Pipeline()
		cmds := make([]*redis.StringCmd, len(batch))
		for i, id := range batch {
			cmds[i] = pipe.Get(ctx, s.bandwidthEdgesKey(id.String()))
		}
		_, _ = pipe.Exec(ctx) //nolint:errcheck // per-command results below
		for i, id := range batch {
			if pair, err := cmds[i].Result(); err == nil {
				if e, ok := parseBandwidthEdgePair(pair); ok {
					ix.edges[id] = e
					continue
				}
			}
			fallback = append(fallback, id)
		}
	}
	// Legacy transports without a persisted pair: read the newest daily hash
	// the scan saw for each, pipelined the same way.
	for start := 0; start < len(fallback); start += scanBatch {
		end := start + scanBatch
		if end > len(fallback) {
			end = len(fallback)
		}
		batch := fallback[start:end]
		pipe := s.client.Pipeline()
		cmds := make([]*redis.StringStringMapCmd, len(batch))
		for i, id := range batch {
			day := now.AddDate(0, 0, -newestBit(ix.byID[id]))
			cmds[i] = pipe.HGetAll(ctx, s.bandwidthDailyKey(id.String(), day))
		}
		_, _ = pipe.Exec(ctx) //nolint:errcheck // per-command results below
		for i, id := range batch {
			if h, err := cmds[i].Result(); err == nil {
				if e, ok := edgesFromDailyHash(h); ok {
					ix.edges[id] = e
				}
			}
		}
	}
	s.log.WithField("transports", len(ix.byID)).WithField("edges", len(ix.edges)).
		WithField("took", time.Since(started).Round(time.Millisecond)).
		Debug("Rebuilt bandwidth day index")
	return ix
}

// newestBit is the lowest set bit's position: the most recent day with data.
func newestBit(mask uint64) int {
	for k := 0; k < 64; k++ {
		if mask&(1<<k) != 0 {
			return k
		}
	}
	return 0
}

// expiredTransportEntries returns synthetic transport.Entry values for
// transports that have daily-bandwidth records within the last `days` days but
// are no longer in the registered set. The returned set marks those IDs so
// buildTransportMetrics reports them Live=false. Their bw:daily:* keys have a
// 35-day TTL and outlive the ~5-minute registration TTL, so the bandwidth they
// carried keeps counting toward the metrics and rewards after the transport
// drops offline. The `registered` filter is applied FRESH on every call so a
// transport that just (re)registered is dropped at once rather than being
// reported as expired for up to the index TTL.
func (s *redisStore) expiredTransportEntries(ctx context.Context, registered map[uuid.UUID]bool, days int) ([]*transport.Entry, map[uuid.UUID]bool) {
	if days <= 0 || days > bwIndexMaxDays {
		days = bwIndexMaxDays
	}
	ix := s.bandwidthIndex(ctx)
	mask := ix.windowMask(time.Now().UTC(), days)
	expiredIDs := make(map[uuid.UUID]bool)
	var entries []*transport.Entry
	for id, bits := range ix.byID {
		if bits&mask == 0 || registered[id] {
			continue
		}
		edges, ok := ix.edges[id]
		if !ok {
			continue // only legacy/combined data, no per-edge fields — skip
		}
		entries = append(entries, &transport.Entry{ID: id, Edges: edges})
		expiredIDs[id] = true
	}
	if len(entries) == 0 {
		return nil, nil
	}
	return entries, expiredIDs
}
