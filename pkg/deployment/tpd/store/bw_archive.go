// Package store pkg/deployment/tpd/store/bw_archive.go c4-net-discovery
package store

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
)

// bandwidthDailyLiveDays is how many days, today included, a transport's
// daily bandwidth stays in redis when the settled-day archive is kept. An
// older day is read from that day's archived metrics leaf, which carries the
// same per-transport rows; the reward system reads only the leaves. It was
// two weeks, about 1.1 GB of the TPD host's redis.
const bandwidthDailyLiveDays = 3

// bandwidthDailyTTL is the life of a transport's daily bandwidth hash.
const bandwidthDailyTTL = bandwidthDailyLiveDays * 24 * time.Hour

var bandwidthDailyTTLSeconds = int64(bandwidthDailyTTL / time.Second)

// archivedDayCacheSize is how many decoded archived days are kept, so a run
// of queries over the same window decodes each day once.
const archivedDayCacheSize = 2

type archivedDayCache struct {
	mu    sync.Mutex
	days  map[string]map[string]DailyEdgeBandwidth
	order []string
}

// liveBandwidthDay reports whether day d (0 is today) is still in redis.
func (s *redisStore) liveBandwidthDay(d int) bool {
	return s.leafArchive == "" || d < bandwidthDailyLiveDays
}

// archivedDay returns the archived per-transport bandwidth of date, by
// transport ID, or nil when that day is not archived.
func (s *redisStore) archivedDay(ctx context.Context, date string) map[string]DailyEdgeBandwidth {
	c := &s.archived
	c.mu.Lock()
	if day, ok := c.days[date]; ok {
		c.mu.Unlock()
		return day
	}
	c.mu.Unlock()

	leaves, err := s.LoadMetricsLeaves(ctx, []string{date})
	if err != nil || len(leaves[date]) == 0 {
		return nil
	}
	day := make(map[string]DailyEdgeBandwidth)
	for _, part := range leaves[date] {
		var recs []TransportMetric
		if json.Unmarshal(cxoutils.Gunzip(part), &recs) != nil {
			return nil
		}
		for _, r := range recs {
			for _, d := range r.Daily {
				if d.Date == date {
					day[r.ID] = d
				}
			}
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.days == nil {
		c.days = make(map[string]map[string]DailyEdgeBandwidth)
	}
	if _, ok := c.days[date]; !ok {
		c.order = append(c.order, date)
		if len(c.order) > archivedDayCacheSize {
			delete(c.days, c.order[0])
			c.order = c.order[1:]
		}
	}
	c.days[date] = day
	return day
}

// edgeDayTotal is a transport's bytes for the day: the larger of its two
// reporters' sent+recv, as transportDailyBandwidth takes it from redis.
func edgeDayTotal(d DailyEdgeBandwidth) uint64 {
	var a, b uint64
	if d.A != nil {
		a = d.A.Sent + d.A.Recv
	}
	if d.B != nil {
		b = d.B.Sent + d.B.Recv
	}
	return max(a, b)
}

// bandwidthDailyTTLFor is the TTL a transport's daily hash is written with:
// the live window when the archive holds older days, the full history
// otherwise.
func (s *redisStore) bandwidthDailyTTLFor() int64 {
	if s.leafArchive == "" {
		return historyTTLSeconds
	}
	return bandwidthDailyTTLSeconds
}

// cleanOldBandwidthDaily queues the deletion of every transport's daily hash
// older than the live window whose day is archived, or, without an archive,
// older than eight days. It used to delete only the day exactly eight days
// back, so a day the cleanup missed lived out its full TTL.
func (s *redisStore) cleanOldBandwidthDaily(ctx context.Context, pipe redis.Pipeliner, now time.Time) {
	keep := 8
	if s.leafArchive != "" {
		keep = bandwidthDailyLiveDays
	}
	cutoff := now.AddDate(0, 0, -keep).Format(MetricsDateFormat)
	archived := map[string]bool{}
	prefix := serviceName + ":bw:daily:"
	iter := s.client.Scan(ctx, 0, prefix+"*", 10000).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		i := strings.LastIndexByte(key, ':')
		if i < 0 {
			continue
		}
		date := key[i+1:]
		if len(date) != len(MetricsDateFormat) || date > cutoff {
			continue
		}
		if s.leafArchive != "" {
			ok, seen := archived[date]
			if !seen {
				_, err := os.Stat(s.leafFile(date))
				ok = err == nil
				archived[date] = ok
			}
			if !ok {
				continue
			}
		}
		pipe.Del(ctx, key)
	}
}
