// Package store pkg/deployment/tpd/store/redis_daily_totals.go c4-net-discovery
//
// Network-wide daily totals, kept as reports arrive so nothing has to scan
// every transport to answer "how much crossed the network on day D".
//
// A transport's daily bandwidth is the MAX over its reporters of
// (sent+recv) — both edges see the same bytes (transportDailyBandwidth).
// The bandwidth script keeps that max per transport per day in the
// transport's daily hash ("max") and adds only its growth to the day's
// network hash, so the network total is exactly the sum of the per-
// transport maxima without ever reading them back. Latency is the mean,
// over the transports that reported one that day, of each one's latest
// average.
//
// The totals start on the day the first report of this code lands
// (netDailySince). That day and earlier ones are read the old way,
// scanning every transport's daily hash; each settled day is read once
// (see store.OpenMetricsDays), so the fallback costs nothing after it.
package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
)

func (s *redisStore) netDailyKey(date string) string {
	return fmt.Sprintf("%s:net:daily:%s", serviceName, date)
}

// netLatencyKey maps a transport ID to its latest average latency that day.
// Kept apart from bw:daily: a daily bandwidth hash existing means the
// transport carried traffic that day, which latency-only records would
// break.
func (s *redisStore) netLatencyKey(date string) string {
	return fmt.Sprintf("%s:net:lat:%s", serviceName, date)
}

// netDailySinceKey holds the first date the running totals were kept for.
// Totals for later dates are complete; that date and earlier are not.
func (s *redisStore) netDailySinceKey() string {
	return fmt.Sprintf("%s:net:since", serviceName)
}

// bandwidthScript applies one reporter's cumulative counters: the delta
// against its previous snapshot, the transport's daily hash, the reporter's
// visor totals, and the growth of the transport's daily max into the
// network total. One round trip, atomic, where it used to be an HGETALL
// plus a pipeline.
//
// KEYS: prev, daily, visorAll, visorDaily, netDaily, netSince
// ARGV: reporter, sent, recv, now, type, prevTTL, histTTL, visorAllTTL, date,
// dailyTTL (the transport's daily hash; see bw_archive.go)
var bandwidthScript = redis.NewScript(`
local function int(v) return string.format('%d', v) end
local cs, cr = tonumber(ARGV[2]), tonumber(ARGV[3])
local ds, dr = cs, cr
local prev = redis.call('HMGET', KEYS[1], 'sent', 'recv')
if prev[1] and prev[2] then
  local ps, pr = tonumber(prev[1]), tonumber(prev[2])
  if cs >= ps then ds = cs - ps end
  if cr >= pr then dr = cr - pr end
end
redis.call('HSET', KEYS[1], 'sent', ARGV[2], 'recv', ARGV[3])
redis.call('EXPIRE', KEYS[1], ARGV[6])
local d = ds + dr
if d <= 0 then return 0 end

local rs = redis.call('HINCRBY', KEYS[2], ARGV[1] .. ':sent', int(ds))
local rr = redis.call('HINCRBY', KEYS[2], ARGV[1] .. ':recv', int(dr))
redis.call('HINCRBY', KEYS[2], 'bandwidth', int(d))
redis.call('HSET', KEYS[2], 'updated_at', ARGV[4])

local total = rs + rr
local max = tonumber(redis.call('HGET', KEYS[2], 'max') or '0')
if total > max then
  local grow = int(total - max)
  redis.call('HSET', KEYS[2], 'max', int(total))
  redis.call('SETNX', KEYS[6], ARGV[9])
  redis.call('HINCRBY', KEYS[5], 'bandwidth', grow)
  redis.call('HINCRBY', KEYS[5], 'type:' .. ARGV[5], grow)
  redis.call('EXPIRE', KEYS[5], ARGV[7])
end
redis.call('EXPIRE', KEYS[2], ARGV[10])

redis.call('SADD', KEYS[3], ARGV[1])
redis.call('EXPIRE', KEYS[3], ARGV[8])
redis.call('HINCRBY', KEYS[4], 'sent', int(ds))
redis.call('HINCRBY', KEYS[4], 'recv', int(dr))
redis.call('HINCRBY', KEYS[4], 'bandwidth', int(d))
redis.call('HSET', KEYS[4], 'updated_at', ARGV[4])
redis.call('EXPIRE', KEYS[4], ARGV[7])
return 1
`)

// latencyScript records a transport's latest average latency for the day
// and moves the day's network latency sums by the change.
//
// KEYS: netLat, netDaily
// ARGV: transportID, avgMS, type, histTTL
var latencyScript = redis.NewScript(`
local v = tonumber(ARGV[2])
local old = redis.call('HGET', KEYS[1], ARGV[1])
redis.call('HSET', KEYS[1], ARGV[1], ARGV[2])
redis.call('EXPIRE', KEYS[1], ARGV[4])
local dv = v
if old then
  dv = v - tonumber(old)
else
  redis.call('HINCRBY', KEYS[2], 'lat_count', 1)
  redis.call('HINCRBY', KEYS[2], 'type_lat_count:' .. ARGV[3], 1)
end
redis.call('HINCRBYFLOAT', KEYS[2], 'lat_sum', dv)
redis.call('HINCRBYFLOAT', KEYS[2], 'type_lat_sum:' .. ARGV[3], dv)
redis.call('EXPIRE', KEYS[2], ARGV[4])
return 1
`)

// typeOrUnknown keeps a hash field name out of an empty type.
func typeOrUnknown(t string) string {
	if t == "" {
		return "unknown"
	}
	return t
}

// dailyFromTotals turns a day's network hash into its aggregate.
func dailyFromTotals(date string, h map[string]string, query MetricsQuery) (DailyAggregate, bool) {
	agg := DailyAggregate{Date: date, ByType: make(map[string]*TypeMetricAggregate)}
	byType := func(t string) *TypeMetricAggregate {
		if agg.ByType[t] == nil {
			agg.ByType[t] = &TypeMetricAggregate{}
		}
		return agg.ByType[t]
	}
	latSum, _ := strconv.ParseFloat(h["lat_sum"], 64)     //nolint:errcheck
	latCount, _ := strconv.ParseFloat(h["lat_count"], 64) //nolint:errcheck
	typeLatSum := make(map[string]float64)
	for k, v := range h {
		switch {
		case k == "bandwidth" && query.Bandwidth:
			agg.Bandwidth, _ = strconv.ParseUint(v, 10, 64) //nolint:errcheck
		case strings.HasPrefix(k, "type:") && query.Bandwidth:
			n, _ := strconv.ParseUint(v, 10, 64) //nolint:errcheck
			byType(strings.TrimPrefix(k, "type:")).Bandwidth = n
		case strings.HasPrefix(k, "type_lat_sum:") && query.Latency:
			typeLatSum[strings.TrimPrefix(k, "type_lat_sum:")], _ = strconv.ParseFloat(v, 64) //nolint:errcheck
		}
	}
	if query.Latency {
		if latCount > 0 {
			agg.Latency = latSum / latCount
		}
		for t, sum := range typeLatSum {
			if n, _ := strconv.ParseFloat(h["type_lat_count:"+t], 64); n > 0 { //nolint:errcheck
				byType(t).Latency = sum / n
			}
		}
	}
	return agg, agg.Bandwidth > 0 || agg.Latency > 0
}

// dailyTotals reads the running totals for the dates they are complete
// for. The map holds those dates only; the rest must be scanned.
func (s *redisStore) dailyTotals(ctx context.Context, dates []string, query MetricsQuery) (map[string]*DailyAggregate, error) {
	since, err := s.client.Get(ctx, s.netDailySinceKey()).Result()
	if err == redis.Nil {
		return map[string]*DailyAggregate{}, nil
	}
	if err != nil {
		return nil, err
	}
	pipe := s.client.Pipeline()
	cmds := make(map[string]*redis.StringStringMapCmd, len(dates))
	for _, date := range dates {
		if date > since {
			cmds[date] = pipe.HGetAll(ctx, s.netDailyKey(date))
		}
	}
	if len(cmds) == 0 {
		return map[string]*DailyAggregate{}, nil
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, err
	}
	out := make(map[string]*DailyAggregate, len(cmds))
	for date, cmd := range cmds {
		agg, ok := dailyFromTotals(date, cmd.Val(), query)
		if ok {
			out[date] = &agg
		} else {
			// Complete and empty: nothing crossed the network that day.
			out[date] = nil
		}
	}
	return out, nil
}

// historyTTLSeconds is the retention of the daily keys, in the unit the
// scripts take.
var historyTTLSeconds = int64(bandwidthHistoryTTL / time.Second)

// addCumulative adds a day's bandwidth to the window's cumulative totals.
func addCumulative(c *CumulativeAggregate, d *DailyAggregate) {
	c.Bandwidth += d.Bandwidth
	for t, a := range d.ByType {
		if c.ByType[t] == nil {
			c.ByType[t] = &TypeMetricAggregate{}
		}
		c.ByType[t].Bandwidth += a.Bandwidth
	}
}
