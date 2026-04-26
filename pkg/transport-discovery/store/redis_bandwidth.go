package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

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

// updateBandwidth calculates the bandwidth delta and updates per-transport and
// per-visor aggregation hashes in Redis. Sent and recv are tracked separately
// per-reporter so the metrics API can reconstruct accurate per-edge bandwidth.
// The prev snapshot is keyed per-reporter to avoid cross-contamination when both
// edges of a transport register independently.
func (s *redisStore) updateBandwidth(ctx context.Context, transportID string,
	reporterPK cipher.PubKey, currentSent, currentRecv uint64) error {

	now := time.Now().UTC()
	reporterHex := reporterPK.Hex()

	// 1. Get previous snapshot (per-reporter) to calculate deltas
	prevKey := s.bandwidthPrevKey(transportID, reporterHex)
	prevResult, err := s.client.HGetAll(ctx, prevKey).Result()

	var deltaSent, deltaRecv uint64
	if err == nil && len(prevResult) > 0 {
		var prevSent, prevRecv uint64
		fmt.Sscanf(prevResult["sent"], "%d", &prevSent) //nolint:errcheck,gosec
		fmt.Sscanf(prevResult["recv"], "%d", &prevRecv) //nolint:errcheck,gosec
		if currentSent >= prevSent {
			deltaSent = currentSent - prevSent
		} else {
			deltaSent = currentSent // Counter reset
		}
		if currentRecv >= prevRecv {
			deltaRecv = currentRecv - prevRecv
		} else {
			deltaRecv = currentRecv // Counter reset
		}
	} else {
		// First time or key expired — use full current values
		deltaSent = currentSent
		deltaRecv = currentRecv
	}

	// 2. Store current as previous for next calculation
	// TTL of 10 minutes allows for missed re-registration cycles (every 90s)
	pipe := s.client.Pipeline()
	pipe.HSet(ctx, prevKey, "sent", currentSent, "recv", currentRecv)
	pipe.Expire(ctx, prevKey, 10*time.Minute)

	// 3. Add deltas to aggregations
	delta := deltaSent + deltaRecv
	if delta > 0 {
		// Per-transport daily aggregation — store per-reporter sent/recv separately
		dailyKey := s.bandwidthDailyKey(transportID, now)
		pipe.HIncrBy(ctx, dailyKey, reporterHex+":sent", int64(deltaSent)) //nolint:gosec
		pipe.HIncrBy(ctx, dailyKey, reporterHex+":recv", int64(deltaRecv)) //nolint:gosec
		// Keep combined total for backward compatibility
		pipe.HIncrBy(ctx, dailyKey, "bandwidth", int64(delta)) //nolint:gosec
		pipe.HSet(ctx, dailyKey, "updated_at", now.Unix())
		pipe.Expire(ctx, dailyKey, 35*24*time.Hour)

		// Per-visor daily aggregation — only for the reporter
		pipe.SAdd(ctx, s.visorAllKey(), reporterHex)
		pipe.Expire(ctx, s.visorAllKey(), 400*24*time.Hour)

		vDaily := s.visorBandwidthDailyKey(reporterHex, now)
		pipe.HIncrBy(ctx, vDaily, "bandwidth", int64(delta)) //nolint:gosec
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

	if period != "daily" {
		return nil, fmt.Errorf("invalid period: %s (only 'daily' is supported)", period)
	}

	entries, err := s.GetTransportsByEdge(ctx, pk)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()

	// Pipeline all HGetAll calls: entries × days
	type bwLookup struct {
		dateStr string
	}
	var lookups []bwLookup
	var cmds []*redis.StringStringMapCmd

	pipe := s.client.Pipeline()
	for d := 0; d < limit; d++ {
		t := now.AddDate(0, 0, -d)
		dateStr := t.Format("2006-01-02")
		for _, entry := range entries {
			key := s.bandwidthDailyKey(entry.ID.String(), t)
			cmds = append(cmds, pipe.HGetAll(ctx, key))
			lookups = append(lookups, bwLookup{dateStr: dateStr})
		}
	}
	_, _ = pipe.Exec(ctx) //nolint:errcheck

	// Aggregate by date
	aggregatedByPeriod := make(map[string]*BandwidthAggregation)
	for i, cmd := range cmds {
		result, err := cmd.Result()
		if err != nil || len(result) == 0 {
			continue
		}
		var bw uint64
		if val, ok := result["bandwidth"]; ok {
			fmt.Sscanf(val, "%d", &bw) //nolint:errcheck,gosec
		}
		if bw == 0 {
			continue
		}
		var updatedAt int64
		if val, ok := result["updated_at"]; ok {
			fmt.Sscanf(val, "%d", &updatedAt) //nolint:errcheck,gosec
		}

		dateStr := lookups[i].dateStr
		if existing, ok := aggregatedByPeriod[dateStr]; ok {
			existing.Bandwidth += bw
			if updatedAt > existing.UpdatedAt {
				existing.UpdatedAt = updatedAt
			}
		} else {
			aggregatedByPeriod[dateStr] = &BandwidthAggregation{
				TransportID: pk.Hex(),
				Period:      period,
				PeriodKey:   dateStr,
				Bandwidth:   bw,
				UpdatedAt:   updatedAt,
			}
		}
	}

	var results []BandwidthAggregation
	for _, agg := range aggregatedByPeriod {
		results = append(results, *agg)
	}

	return results, nil
}

// BackupAndCleanOldBandwidth writes day-8 visor bandwidth to .txt files and deletes the Redis keys.
// It also cleans up per-transport daily bandwidth keys older than 8 days.
// The method is idempotent — it only processes data if day-8 keys exist.
func (s *redisStore) BackupAndCleanOldBandwidth(ctx context.Context, backupPath string) error {
	if backupPath == "" {
		return nil
	}

	if err := os.MkdirAll(backupPath, 0o750); err != nil {
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
			if _, err := fmt.Sscanf(val, "%d", &bw); err != nil {
				return fmt.Errorf("failed to parse bandwidth value %q: %w", val, err)
			}
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
