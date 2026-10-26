// Package store pkg/deployment/tpd/store/telemetry_batch.go c4-net-discovery
//
// Telemetry applied in batches: a telemetry shard (or a flush of paced
// snapshots) carries many transports, and applying them one at a time was a
// round trip per script per transport. ApplyTelemetry does the same writes
// as UpdateBandwidth, UpdateThroughput and UpdateLatency for a whole batch in
// two pipelines: one MGET of the current throughput peaks, then everything
// else.
package store

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
)

// TelemetryUpdate is one edge's report for one transport.
type TelemetryUpdate struct {
	ID                     uuid.UUID
	Reporter               cipher.PubKey
	Sent, Recv             uint64
	ThroughputBps          float64
	LatMin, LatMax, LatAvg float64
	Type                   string
	// SameNetwork: the reporter marked the peer as on its own network, so
	// the transport's bytes that day are not paid for.
	SameNetwork bool
	// At is when the report arrived. The daily hashes are dated by it, so a
	// row held across UTC midnight counts on the day it was sent. Zero means now.
	At time.Time
}

// TelemetryBatchStore is implemented by stores that apply telemetry in
// batches.
type TelemetryBatchStore interface {
	ApplyTelemetry(ctx context.Context, updates []TelemetryUpdate) error
}

// latencyValid is UpdateLatency's gate: all three fields positive and none
// beyond the RTT a straggler pong could produce.
func latencyValid(minMS, maxMS, avgMS float64) bool {
	return minMS > 0 && maxMS > 0 && avgMS > 0 &&
		minMS <= transport.MaxReasonableRTTMs &&
		maxMS <= transport.MaxReasonableRTTMs &&
		avgMS <= transport.MaxReasonableRTTMs
}

// ApplyTelemetry implements TelemetryBatchStore.
func (s *redisStore) ApplyTelemetry(ctx context.Context, updates []TelemetryUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	err := s.applyTelemetry(ctx, updates)
	if err != nil && strings.HasPrefix(err.Error(), "NOSCRIPT") {
		// Redis restarted and lost its script cache. Both scripts are
		// idempotent against a repeat (bandwidth is a delta against the
		// stored previous counters, latency a delta against the stored
		// latest), so the batch is simply run again once loaded.
		if lerr := bandwidthBatchScript.Load(ctx, s.client).Err(); lerr != nil {
			return lerr
		}
		if lerr := latencyBatchScript.Load(ctx, s.client).Err(); lerr != nil {
			return lerr
		}
		err = s.applyTelemetry(ctx, updates)
	}
	return err
}

func (s *redisStore) applyTelemetry(ctx context.Context, updates []TelemetryUpdate) error {
	now := time.Now().UTC()

	// Current throughput peaks, for the peak-preserving max.
	var tputKeys []string
	tputIdx := make(map[int]int)
	for i, u := range updates {
		if u.ThroughputBps > 0 && u.Reporter != (cipher.PubKey{}) {
			tputIdx[i] = len(tputKeys)
			tputKeys = append(tputKeys, s.throughputKey(u.ID))
		}
	}
	var peaks []interface{}
	if len(tputKeys) > 0 {
		var err error
		if peaks, err = s.client.MGet(ctx, tputKeys...).Result(); err != nil {
			return err
		}
	}

	// Rows are dated by when they arrived (TelemetryUpdate.At), and grouped
	// so each reporter's rows of one day share one bandwidth script and each
	// day's latencies one latency script.
	type bwGroup struct {
		reporter string
		day      time.Time
		rows     []int
	}
	var groups []*bwGroup
	byReporterDay := make(map[string]*bwGroup)
	latRows := make(map[string][]int)
	var latDates []string
	pipe := s.client.Pipeline()
	for i, u := range updates {
		at := u.dayOf(now)
		date := at.Format(MetricsDateFormat)
		if u.Reporter != (cipher.PubKey{}) {
			reporterHex := u.Reporter.Hex()
			gk := reporterHex + date
			g := byReporterDay[gk]
			if g == nil || len(g.rows) == maxScriptRows {
				g = &bwGroup{reporter: reporterHex, day: at}
				byReporterDay[gk] = g
				groups = append(groups, g)
			}
			g.rows = append(g.rows, i)
			if u.SameNetwork {
				// Marked on the day's hash itself, beside the counters it
				// qualifies; it lives exactly as long as they do.
				daily := s.bandwidthDailyKey(u.ID.String(), at)
				pipe.HSet(ctx, daily, sameNetworkField, "1")
				pipe.Expire(ctx, daily, time.Duration(s.bandwidthDailyTTLFor())*time.Second)
			}
		}
		if j, ok := tputIdx[i]; ok {
			bps := u.ThroughputBps
			if raw, ok := peaks[j].(string); ok && raw != "" {
				var prev ThroughputRecord
				if json.Unmarshal([]byte(raw), &prev) == nil && prev.Bps > bps {
					bps = prev.Bps
				}
			}
			if raw, err := json.Marshal(ThroughputRecord{Bps: bps, UpdatedAt: now.Unix()}); err == nil {
				pipe.Set(ctx, s.throughputKey(u.ID), string(raw), throughputTTL)
			}
		}
		if latencyValid(u.LatMin, u.LatMax, u.LatAvg) {
			rec := LatencyRecord{
				Min: int64(u.LatMin * 1000), Max: int64(u.LatMax * 1000), Avg: int64(u.LatAvg * 1000),
				UpdatedAt: now.Unix(),
			}
			if raw, err := json.Marshal(rec); err == nil {
				pipe.Set(ctx, s.latencyKey(u.ID), string(raw), latencyTTL)
				if _, ok := latRows[date]; !ok {
					latDates = append(latDates, date)
				}
				latRows[date] = append(latRows[date], i)
			}
		}
	}

	bw := make([]*redis.Cmd, len(groups))
	for gi, g := range groups {
		date := g.day.Format(MetricsDateFormat)
		keys := []string{s.visorAllKey(), s.visorBandwidthDailyKey(g.reporter, g.day), s.netDailyKey(date), s.netDailySinceKey()}
		args := []interface{}{g.reporter, now.Unix(), bandwidthPrevTTLSeconds, historyTTLSeconds,
			int64((400 * 24 * time.Hour) / time.Second), date, s.bandwidthDailyTTLFor()}
		for _, i := range g.rows {
			u := updates[i]
			idStr := u.ID.String()
			keys = append(keys, s.bandwidthPrevKey(idStr, g.reporter), s.bandwidthDailyKey(idStr, g.day))
			args = append(args, u.Sent, u.Recv, typeOrUnknown(u.Type))
		}
		bw[gi] = bandwidthBatchScript.EvalSha(ctx, pipe, keys, args...)
	}
	for _, date := range latDates {
		rows := latRows[date]
		for len(rows) > 0 {
			n := min(len(rows), maxScriptRows)
			args := []interface{}{historyTTLSeconds}
			for _, i := range rows[:n] {
				u := updates[i]
				args = append(args, u.ID.String(), strconv.FormatFloat(u.LatAvg, 'f', -1, 64), typeOrUnknown(u.Type))
				s.today.markDirty(u.ID)
			}
			latencyBatchScript.EvalSha(ctx, pipe, []string{s.netLatencyKey(date), s.netDailyKey(date)}, args...)
			rows = rows[n:]
		}
	}

	cmds, err := pipe.Exec(ctx)
	if err != nil {
		for _, c := range cmds {
			if cerr := c.Err(); cerr != nil && strings.HasPrefix(cerr.Error(), "NOSCRIPT") {
				return cerr
			}
		}
		return err
	}
	for gi, c := range bw {
		moved, err := c.Int64Slice()
		if err != nil {
			continue
		}
		for j, m := range moved {
			if m == 1 && j < len(groups[gi].rows) {
				s.today.markDirty(updates[groups[gi].rows[j]].ID)
			}
		}
	}
	for _, u := range updates {
		if latencyValid(u.LatMin, u.LatMax, u.LatAvg) {
			s.live.setLatency(u.ID, u.LatAvg, now)
		}
		if u.ThroughputBps > 0 && u.Reporter != (cipher.PubKey{}) {
			s.live.setThroughput(u.ID, u.ThroughputBps, now)
		}
	}
	return nil
}

// dayOf is the time the row is dated by: when it arrived, or now when that
// is unset or later than now.
func (u TelemetryUpdate) dayOf(now time.Time) time.Time {
	if u.At.IsZero() || u.At.After(now) {
		return now
	}
	return u.At.UTC()
}

// ApplyTelemetry implements TelemetryBatchStore with the single-item writes.
func (s *memoryStore) ApplyTelemetry(ctx context.Context, updates []TelemetryUpdate) error {
	for _, u := range updates {
		idStr := u.ID.String()
		if err := s.UpdateBandwidth(ctx, idStr, u.Reporter, u.Sent, u.Recv, u.Type); err != nil {
			return err
		}
		if u.ThroughputBps > 0 {
			if err := s.UpdateThroughput(ctx, idStr, u.Reporter, u.ThroughputBps); err != nil {
				return err
			}
		}
		if latencyValid(u.LatMin, u.LatMax, u.LatAvg) {
			if err := s.UpdateLatency(ctx, idStr, u.LatMin, u.LatMax, u.LatAvg, u.Type); err != nil {
				return err
			}
		}
	}
	return nil
}
