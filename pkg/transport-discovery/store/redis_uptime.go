package store

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

// GetAllVisorSummaries returns uptime summaries for all visors with active transports.
// Online status is determined by having active transports.
// Version and Daily uptime fields require uptime tracker integration.
// uptimeKey returns the Redis key for a visor's heartbeat data on a given date.
func uptimeKey(pk string, date string) string {
	return fmt.Sprintf("%s:uptime:%s:%s", serviceName, pk, date)
}

// uptimeOnlineKey returns the Redis key for the set of visors seen today.
func uptimeOnlineKey(date string) string {
	return fmt.Sprintf("%s:uptime:online:%s", serviceName, date)
}

// uptimeTimelineKey returns the Redis key for a visor's timeline bitmap on a given date.
// The bitmap has 288 bits — one per 5-minute slot in the day.
func uptimeTimelineKey(pk string, date string) string {
	return fmt.Sprintf("%s:uptime:%s:%s:timeline", serviceName, pk, date)
}

// currentTimelineSlot returns the 0-based slot index for the given time (0–287).
func currentTimelineSlot(t time.Time) int64 {
	return int64(t.Hour()*12 + t.Minute()/5)
}

func (s *redisStore) GetAllVisorSummaries(ctx context.Context, v2 bool, timeline bool) ([]VisorSummary, error) {
	now := time.Now().UTC()
	today := now.Format("2006-01-02")

	// Get all visors that have been seen today (from heartbeats)
	heartbeatVisors, err := s.client.SMembers(ctx, uptimeOnlineKey(today)).Result()
	if err != nil {
		heartbeatVisors = nil
	}

	// Count p2p transports per visor for online determination.
	p2pCounts := s.getP2PTransportCounts(ctx)

	// Merge heartbeat visors and visors with p2p transports.
	allVisors := make(map[string]struct{})
	for _, pk := range heartbeatVisors {
		allVisors[pk] = struct{}{}
	}
	for pk := range p2pCounts {
		allVisors[pk] = struct{}{}
	}

	if len(allVisors) == 0 {
		return []VisorSummary{}, nil
	}

	result := make([]VisorSummary, 0, len(allVisors))

	for pkHex := range allVisors {
		var pk cipher.PubKey
		if err := pk.UnmarshalText([]byte(pkHex)); err != nil {
			continue
		}

		// Online = has 2+ p2p transports (stcpr/sudph).
		// This indicates genuine peer-to-peer network participation,
		// not just dmsg infrastructure connectivity.
		online := p2pCounts[pkHex] >= minP2PTransportsOnline

		// Get version from heartbeat data.
		version := ""
		key := uptimeKey(pkHex, today)
		vals, err := s.client.HMGet(ctx, key, "version").Result()
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

		// Add daily history for v2+
		if v2 || timeline {
			summary.Daily = s.getDailyUptime(ctx, pkHex, now)
		}

		// Add timeline bitmaps for v3
		if timeline {
			summary.Timeline = s.GetDailyTimeline(ctx, pkHex, now)
		}

		result = append(result, summary)
	}

	return result, nil
}

// getDailyUptime computes the last 7 days of uptime percentages for a visor.
func (s *redisStore) getDailyUptime(ctx context.Context, pkHex string, now time.Time) map[string]string {
	daily := make(map[string]string)

	for i := 0; i < uptimeHistoryDays; i++ {
		date := now.AddDate(0, 0, -i).Format("2006-01-02")
		key := uptimeKey(pkHex, date)

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

// getDailyTimeline reads the timeline bitmap for each of the last 7 days and
// converts it to a 288-char string per day. '.' = heartbeat received in that
// 5-minute slot, ' ' = missed.
func (s *redisStore) GetDailyTimeline(ctx context.Context, pkHex string, now time.Time) map[string]string {
	timelines := make(map[string]string)

	for i := 0; i < uptimeHistoryDays; i++ {
		date := now.AddDate(0, 0, -i).Format("2006-01-02")
		tlKey := uptimeTimelineKey(pkHex, date)

		// Read all 288 bits. Redis GETBIT returns 0 for unset bits
		// and for keys that don't exist, so this is safe.
		var buf [timelineSlots]byte
		pipe := s.client.Pipeline()
		cmds := make([]*redis.IntCmd, timelineSlots)
		for slot := 0; slot < timelineSlots; slot++ {
			cmds[slot] = pipe.GetBit(ctx, tlKey, int64(slot))
		}
		_, err := pipe.Exec(ctx)
		if err != nil {
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

		// Only include days that have at least one heartbeat.
		if hasAny {
			timelines[date] = string(buf[:])
		}
	}

	return timelines
}

func tpUptimeKey(tpID string, date string) string {
	return fmt.Sprintf("%s:tp-uptime:%s:%s", serviceName, tpID, date)
}

func tpUptimeOnlineKey(date string) string {
	return fmt.Sprintf("%s:tp-uptime:online:%s", serviceName, date)
}

func tpUptimeTimelineKey(tpID string, date string) string {
	return fmt.Sprintf("%s:tp-uptime:%s:%s:timeline", serviceName, tpID, date)
}

func (s *redisStore) RecordTransportHeartbeat(ctx context.Context, tpID uuid.UUID, tpType string) error {
	// Only track p2p transport types.
	if tpType != "stcpr" && tpType != "sudph" {
		return nil
	}

	now := time.Now().UTC()
	date := now.Format("2006-01-02")
	idStr := tpID.String()
	key := tpUptimeKey(idStr, date)

	pipe := s.client.Pipeline()

	pipe.HIncrBy(ctx, key, "count", 1)
	pipe.HSet(ctx, key, "type", tpType)
	pipe.HSet(ctx, key, "last_seen", now.Unix())
	pipe.Expire(ctx, key, 8*24*time.Hour)

	pipe.SAdd(ctx, tpUptimeOnlineKey(date), idStr)
	pipe.Expire(ctx, tpUptimeOnlineKey(date), 8*24*time.Hour)

	tlKey := tpUptimeTimelineKey(idStr, date)
	pipe.SetBit(ctx, tlKey, currentTimelineSlot(now), 1)
	pipe.Expire(ctx, tlKey, 8*24*time.Hour)

	_, err := pipe.Exec(ctx)
	return err
}

func (s *redisStore) GetTransportUptimeSummaries(ctx context.Context, tpIDs []uuid.UUID, v2 bool, timeline bool) ([]TransportUptimeSummary, error) {
	now := time.Now().UTC()
	today := now.Format("2006-01-02")

	// If no specific IDs requested, get all seen today.
	if len(tpIDs) == 0 {
		members, err := s.client.SMembers(ctx, tpUptimeOnlineKey(today)).Result()
		if err != nil {
			return []TransportUptimeSummary{}, nil
		}
		for _, m := range members {
			if id, perr := uuid.Parse(m); perr == nil {
				tpIDs = append(tpIDs, id)
			}
		}
	}

	result := make([]TransportUptimeSummary, 0, len(tpIDs))
	for _, id := range tpIDs {
		idStr := id.String()
		key := tpUptimeKey(idStr, today)

		// Check online: has a heartbeat today and the transport entry still exists.
		online := false
		tpType := ""
		vals, err := s.client.HMGet(ctx, key, "last_seen", "type").Result()
		if err == nil && len(vals) >= 2 {
			if lastStr, ok := vals[0].(string); ok {
				if lastSeen, perr := strconv.ParseInt(lastStr, 10, 64); perr == nil {
					if now.Sub(time.Unix(lastSeen, 0)) < onlineThresholdTP {
						online = true
					}
				}
			}
			if t, ok := vals[1].(string); ok {
				tpType = t
			}
		}

		summary := TransportUptimeSummary{
			ID:     id,
			Online: online,
			Type:   tpType,
		}

		if v2 || timeline {
			summary.Daily = s.getTransportDailyUptime(ctx, idStr, now)
		}
		if timeline {
			summary.Timeline = s.GetTransportDailyTimeline(ctx, idStr, now)
		}

		result = append(result, summary)
	}

	return result, nil
}

func (s *redisStore) GetTransportUptimeByVisor(ctx context.Context, pk cipher.PubKey, v2 bool, timeline bool) ([]TransportUptimeSummary, error) {
	// Get all transport IDs for this visor from the edge index.
	entries, err := s.GetTransportsByEdge(ctx, pk)
	if err != nil {
		return nil, err
	}

	ids := make([]uuid.UUID, 0, len(entries))
	for _, e := range entries {
		if e.Type == "stcpr" || e.Type == "sudph" {
			ids = append(ids, e.ID)
		}
	}

	return s.GetTransportUptimeSummaries(ctx, ids, v2, timeline)
}

func (s *redisStore) getTransportDailyUptime(ctx context.Context, tpID string, now time.Time) map[string]string {
	daily := make(map[string]string)
	for i := 0; i < uptimeHistoryDays; i++ {
		date := now.AddDate(0, 0, -i).Format("2006-01-02")
		countStr, err := s.client.HGet(ctx, tpUptimeKey(tpID, date), "count").Result()
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

func (s *redisStore) GetTransportDailyTimeline(ctx context.Context, tpID string, now time.Time) map[string]string {
	timelines := make(map[string]string)
	for i := 0; i < uptimeHistoryDays; i++ {
		date := now.AddDate(0, 0, -i).Format("2006-01-02")
		tlKey := tpUptimeTimelineKey(tpID, date)

		pipe := s.client.Pipeline()
		cmds := make([]*redis.IntCmd, timelineSlots)
		for slot := 0; slot < timelineSlots; slot++ {
			cmds[slot] = pipe.GetBit(ctx, tlKey, int64(slot))
		}
		_, err := pipe.Exec(ctx)
		if err != nil {
			continue
		}

		var buf [timelineSlots]byte
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
