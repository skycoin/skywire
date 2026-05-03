package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
)

// getP2PTransportCounts returns a map of visor PK hex → count of p2p transports
// (stcpr, sudph). A visor is considered online when it has 2+ p2p transports,
// indicating genuine peer-to-peer network participation (not just dmsg
// infrastructure connectivity).
func (s *redisStore) getP2PTransportCounts(ctx context.Context) map[string]int {
	keys, ids, err := s.allTransportKeysFromIndex(ctx)
	if err != nil {
		return nil
	}

	counts := make(map[string]int)
	var stale []interface{}

	const mgetBatch = 10000
	for i := 0; i < len(keys); i += mgetBatch {
		end := i + mgetBatch
		if end > len(keys) {
			end = len(keys)
		}
		vals, err := s.client.MGet(ctx, keys[i:end]...).Result()
		if err != nil {
			continue
		}
		for j, val := range vals {
			raw, ok := val.(string)
			if !ok || raw == "" {
				stale = append(stale, ids[i+j])
				continue
			}
			var data TransportData
			if err := json.Unmarshal([]byte(raw), &data); err != nil {
				continue
			}
			// Only count p2p transport types (stcpr, sudph), not dmsg.
			if data.Type == "stcpr" || data.Type == "sudph" {
				counts[data.EdgeA]++
				if data.EdgeA != data.EdgeB {
					counts[data.EdgeB]++
				}
			}
		}
	}
	s.maybeReapStaleTransports(stale)

	return counts
}

func (s *redisStore) getBandwidthFromHash(ctx context.Context, key, transportID, period, periodKey string) (BandwidthAggregation, error) {
	result, err := s.client.HGetAll(ctx, key).Result()
	if err != nil || len(result) == 0 {
		return BandwidthAggregation{}, errors.New("not found")
	}

	agg := BandwidthAggregation{
		TransportID: transportID,
		Period:      period,
		PeriodKey:   periodKey,
	}

	if val, ok := result["bandwidth"]; ok {
		if _, err := fmt.Sscanf(val, "%d", &agg.Bandwidth); err != nil {
			return BandwidthAggregation{}, fmt.Errorf("failed to parse bandwidth value %q: %w", val, err)
		}
	}
	if val, ok := result["updated_at"]; ok {
		if _, err := fmt.Sscanf(val, "%d", &agg.UpdatedAt); err != nil {
			return BandwidthAggregation{}, fmt.Errorf("failed to parse updated_at value %q: %w", val, err)
		}
	}

	return agg, nil
}

// GetNetworkMetrics returns network-wide aggregate metrics.
// Uses Redis pipelining for efficient bulk fetching.
func (s *redisStore) GetNetworkMetrics(ctx context.Context, query MetricsQuery) (*NetworkMetricResponse, error) {
	days := query.Days
	if days <= 0 {
		days = 35 // All available
	}
	if days > 35 {
		days = 35
	}

	now := time.Now().UTC()
	response := &NetworkMetricResponse{
		Daily:      make([]DailyAggregate, 0, days),
		Cumulative: &CumulativeAggregate{ByType: make(map[string]*TypeMetricAggregate)},
	}

	// Get all transports to aggregate (with QoS data for latency)
	entries, err := s.getAllTransportsWithQoS(ctx, true)
	if err != nil && !errors.Is(err, ErrTransportNotFound) {
		return nil, err
	}

	if len(entries) == 0 {
		return response, nil
	}

	// Build all bandwidth keys and fetch via pipeline
	type bwKey struct {
		dayIdx   int
		entryIdx int
		tpType   string
		dateStr  string
	}
	var bwKeys []bwKey
	var bwResults []*redis.StringStringMapCmd

	pipe := s.client.Pipeline()
	for d := 0; d < days; d++ {
		t := now.AddDate(0, 0, -d)
		dateStr := t.Format("2006-01-02")
		for i, entry := range entries {
			key := s.bandwidthDailyKey(entry.ID.String(), t)
			bwKeys = append(bwKeys, bwKey{
				dayIdx:   d,
				entryIdx: i,
				tpType:   string(entry.Type),
				dateStr:  dateStr,
			})
			bwResults = append(bwResults, pipe.HGetAll(ctx, key))
		}
	}
	_, _ = pipe.Exec(ctx) //nolint:errcheck

	// Process results: aggregate by day
	type dayData struct {
		agg          DailyAggregate
		latencySum   float64
		latencyCount int
		typeLatSum   map[string]float64
		typeLatCount map[string]int
	}
	dayMap := make(map[int]*dayData)

	for i, bk := range bwKeys {
		result, err := bwResults[i].Result()
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

		// Initialize day data if needed
		if dayMap[bk.dayIdx] == nil {
			dayMap[bk.dayIdx] = &dayData{
				agg: DailyAggregate{
					Date:   bk.dateStr,
					ByType: make(map[string]*TypeMetricAggregate),
				},
				typeLatSum:   make(map[string]float64),
				typeLatCount: make(map[string]int),
			}
		}
		dd := dayMap[bk.dayIdx]

		if query.Bandwidth {
			dd.agg.Bandwidth += bw
			if dd.agg.ByType[bk.tpType] == nil {
				dd.agg.ByType[bk.tpType] = &TypeMetricAggregate{}
			}
			dd.agg.ByType[bk.tpType].Bandwidth += bw

			response.Cumulative.Bandwidth += bw
			if response.Cumulative.ByType[bk.tpType] == nil {
				response.Cumulative.ByType[bk.tpType] = &TypeMetricAggregate{}
			}
			response.Cumulative.ByType[bk.tpType].Bandwidth += bw
		}

		// Track latency for proper averaging
		if query.Latency {
			entry := entries[bk.entryIdx]
			if entry.Latency > 0 {
				dd.latencySum += entry.Latency
				dd.latencyCount++
				dd.typeLatSum[bk.tpType] += entry.Latency
				dd.typeLatCount[bk.tpType]++
			}
		}
	}

	// Compute averages and build response
	for d := 0; d < days; d++ {
		dd, ok := dayMap[d]
		if !ok {
			continue
		}

		// Compute proper latency averages
		if dd.latencyCount > 0 {
			dd.agg.Latency = dd.latencySum / float64(dd.latencyCount)
		}
		for tpType, sum := range dd.typeLatSum {
			if count := dd.typeLatCount[tpType]; count > 0 {
				if dd.agg.ByType[tpType] == nil {
					dd.agg.ByType[tpType] = &TypeMetricAggregate{}
				}
				dd.agg.ByType[tpType].Latency = sum / float64(count)
			}
		}

		if dd.agg.Bandwidth > 0 || dd.agg.Latency > 0 {
			response.Daily = append(response.Daily, dd.agg)
		}
	}

	return response, nil
}

// GetVisorAggregateMetrics returns aggregate metrics for specified visors.
func (s *redisStore) GetVisorAggregateMetrics(ctx context.Context, pks []cipher.PubKey, query MetricsQuery) (map[string]*VisorMetricResponse, error) {
	days := query.Days
	if days <= 0 {
		days = 35
	}
	if days > 35 {
		days = 35
	}

	now := time.Now().UTC()
	result := make(map[string]*VisorMetricResponse)

	for _, pk := range pks {
		pkHex := pk.Hex()
		visorResp := &VisorMetricResponse{
			Daily:      make([]DailyVisorAggregate, 0, days),
			Cumulative: &VisorCumulativeAggregate{},
		}

		var totalSent, totalRecv uint64

		// Calculate latency once (it's a point-in-time metric, not per-day)
		var avgLatency float64
		if query.Latency {
			entries, _ := s.GetTransportsByEdge(ctx, pk) //nolint:errcheck
			var latencySum float64
			var latencyCount int
			for _, entry := range entries {
				if entry.Latency > 0 {
					latencySum += entry.Latency
					latencyCount++
				}
			}
			if latencyCount > 0 {
				avgLatency = latencySum / float64(latencyCount)
			}
		}

		// Pipeline all daily bandwidth lookups for this visor
		type bwCmd struct {
			dateStr string
			cmd     *redis.StringStringMapCmd
		}
		var bwCmds []bwCmd
		if query.Bandwidth {
			pipe := s.client.Pipeline()
			for d := 0; d < days; d++ {
				t := now.AddDate(0, 0, -d)
				bwCmds = append(bwCmds, bwCmd{
					dateStr: t.Format("2006-01-02"),
					cmd:     pipe.HGetAll(ctx, s.visorBandwidthDailyKey(pkHex, t)),
				})
			}
			_, _ = pipe.Exec(ctx) //nolint:errcheck
		}

		for d := 0; d < days; d++ {
			dailyAgg := DailyVisorAggregate{}

			if d < len(bwCmds) {
				dailyAgg.Date = bwCmds[d].dateStr
				bwResult, err := bwCmds[d].cmd.Result()
				if err == nil && len(bwResult) > 0 {
					var bw uint64
					if val, ok := bwResult["bandwidth"]; ok {
						fmt.Sscanf(val, "%d", &bw) //nolint:errcheck,gosec
					}
					if bw > 0 {
						dailyAgg.Bandwidth = &VisorBandwidthAggregate{
							Sent:  bw / 2,
							Recv:  bw / 2,
							Total: bw,
						}
						totalSent += bw / 2
						totalRecv += bw / 2
					}
				}
			} else {
				t := now.AddDate(0, 0, -d)
				dailyAgg.Date = t.Format("2006-01-02")
			}

			if query.Latency {
				dailyAgg.Latency = avgLatency
			}

			if dailyAgg.Bandwidth != nil || dailyAgg.Latency > 0 {
				visorResp.Daily = append(visorResp.Daily, dailyAgg)
			}
		}

		if query.Bandwidth && (totalSent > 0 || totalRecv > 0) {
			visorResp.Cumulative.Bandwidth = &VisorBandwidthAggregate{
				Sent:  totalSent,
				Recv:  totalRecv,
				Total: totalSent + totalRecv,
			}
		}
		if query.Latency && avgLatency > 0 {
			visorResp.Cumulative.Latency = avgLatency
		}

		result[pkHex] = visorResp
	}

	return result, nil
}

// GetAllTransportMetrics returns metrics for all transports.
func (s *redisStore) GetAllTransportMetrics(ctx context.Context, query MetricsQuery) ([]TransportMetric, error) {
	entries, err := s.getAllTransportsWithQoS(ctx, true)
	if err != nil {
		if errors.Is(err, ErrTransportNotFound) {
			return []TransportMetric{}, nil
		}
		return nil, err
	}

	return s.buildTransportMetrics(ctx, entries, nil, query)
}

// GetTransportMetricsByIDs returns metrics for specific transports.
func (s *redisStore) GetTransportMetricsByIDs(ctx context.Context, ids []uuid.UUID, query MetricsQuery) ([]TransportMetric, error) {
	var entries []*transport.Entry
	for _, id := range ids {
		entry, err := s.GetTransportByID(ctx, id)
		if err != nil {
			continue // Skip not found
		}
		entries = append(entries, entry)
	}

	return s.buildTransportMetrics(ctx, entries, nil, query)
}

// GetTransportMetricsByVisors returns metrics for transports of specified visors.
func (s *redisStore) GetTransportMetricsByVisors(ctx context.Context, pks []cipher.PubKey, query MetricsQuery) ([]TransportMetric, error) {
	// Collect all unique transports for these visors
	seen := make(map[uuid.UUID]bool)
	var entries []*transport.Entry

	for _, pk := range pks {
		visorEntries, err := s.GetTransportsByEdge(ctx, pk)
		if err != nil {
			continue
		}
		for _, entry := range visorEntries {
			if !seen[entry.ID] {
				seen[entry.ID] = true
				entries = append(entries, entry)
			}
		}
	}

	return s.buildTransportMetrics(ctx, entries, nil, query)
}

// buildTransportMetrics builds TransportMetric slice from entries.
// Uses Redis pipelining for efficient bulk fetching.
func (s *redisStore) buildTransportMetrics(ctx context.Context, entries []*transport.Entry, expiredIDs map[uuid.UUID]bool, query MetricsQuery) ([]TransportMetric, error) {
	days := query.Days
	if days <= 0 {
		days = 35
	}
	if days > 35 {
		days = 35
	}

	now := time.Now().UTC()

	// First pass: filter entries and prepare for bulk fetch
	type filteredEntry struct {
		entry  *transport.Entry
		isLive bool
	}
	var filtered []filteredEntry

	for _, entry := range entries {
		// Apply type filter
		if query.Type != "" && string(entry.Type) != query.Type {
			continue
		}

		// Apply live filter
		isLive := expiredIDs == nil || !expiredIDs[entry.ID]
		switch query.Live {
		case "true":
			if !isLive {
				continue
			}
		case "false":
			if isLive {
				continue
			}
		}

		filtered = append(filtered, filteredEntry{entry: entry, isLive: isLive})
	}

	if len(filtered) == 0 {
		return []TransportMetric{}, nil
	}

	// Fetch latency data via pipeline. Reads the durable lat:<id> key
	// (35-day TTL) rather than the tp:<id> registration blob — survives
	// the 5-minute registration churn that bandwidth has always survived.
	var latencyResults []*redis.StringCmd
	if query.Latency {
		pipe := s.client.Pipeline()
		latencyResults = make([]*redis.StringCmd, len(filtered))
		for i, f := range filtered {
			latencyResults[i] = pipe.Get(ctx, s.latencyKey(f.entry.ID))
		}
		_, _ = pipe.Exec(ctx) //nolint:errcheck // Errors handled per-command via Result()
	}

	// Fetch bandwidth data via pipeline
	type bwKey struct {
		idx     int
		dayIdx  int
		dateStr string
	}
	var bwKeys []bwKey
	var bwResults []*redis.StringStringMapCmd

	if query.Bandwidth {
		pipe := s.client.Pipeline()
		for i, f := range filtered {
			for d := 0; d < days; d++ {
				t := now.AddDate(0, 0, -d)
				dateStr := t.Format("2006-01-02")
				key := s.bandwidthDailyKey(f.entry.ID.String(), t)
				bwKeys = append(bwKeys, bwKey{idx: i, dayIdx: d, dateStr: dateStr})
				bwResults = append(bwResults, pipe.HGetAll(ctx, key))
			}
		}
		_, _ = pipe.Exec(ctx) //nolint:errcheck // Errors handled per-command via Result()
	}

	// Build bandwidth lookup map: entryIdx -> []DailyEdgeBandwidth
	bwByEntry := make(map[int][]DailyEdgeBandwidth)
	for i, bk := range bwKeys {
		result, err := bwResults[i].Result()
		if err != nil || len(result) == 0 {
			continue
		}

		f := filtered[bk.idx]
		edgeAHex := f.entry.Edges[0].Hex()
		edgeBHex := f.entry.Edges[1].Hex()

		// Try to read per-reporter sent/recv fields (new format)
		var aSent, aRecv, bSent, bRecv uint64
		hasPerEdge := false
		if val, ok := result[edgeAHex+":sent"]; ok {
			fmt.Sscanf(val, "%d", &aSent) //nolint:errcheck,gosec
			hasPerEdge = true
		}
		if val, ok := result[edgeAHex+":recv"]; ok {
			fmt.Sscanf(val, "%d", &aRecv) //nolint:errcheck,gosec
			hasPerEdge = true
		}
		if val, ok := result[edgeBHex+":sent"]; ok {
			fmt.Sscanf(val, "%d", &bSent) //nolint:errcheck,gosec
			hasPerEdge = true
		}
		if val, ok := result[edgeBHex+":recv"]; ok {
			fmt.Sscanf(val, "%d", &bRecv) //nolint:errcheck,gosec
			hasPerEdge = true
		}

		if hasPerEdge && (aSent+aRecv+bSent+bRecv) > 0 {
			dailyMetric := DailyEdgeBandwidth{
				Date: bk.dateStr,
				A:    &EdgeBandwidth{Sent: aSent, Recv: aRecv},
				B:    &EdgeBandwidth{Sent: bSent, Recv: bRecv},
			}
			bwByEntry[bk.idx] = append(bwByEntry[bk.idx], dailyMetric)
		} else {
			// Fallback for old data: split combined total equally
			var bw uint64
			if val, ok := result["bandwidth"]; ok {
				fmt.Sscanf(val, "%d", &bw) //nolint:errcheck,gosec
			}
			if bw > 0 {
				halfBW := bw / 2
				dailyMetric := DailyEdgeBandwidth{
					Date: bk.dateStr,
					A:    &EdgeBandwidth{Sent: halfBW, Recv: halfBW},
					B:    &EdgeBandwidth{Sent: halfBW, Recv: halfBW},
				}
				bwByEntry[bk.idx] = append(bwByEntry[bk.idx], dailyMetric)
			}
		}
	}

	// Build results
	var results []TransportMetric
	for i, f := range filtered {
		metric := TransportMetric{
			ID:    f.entry.ID.String(),
			Type:  string(f.entry.Type),
			Live:  f.isLive,
			Daily: bwByEntry[i],
		}
		if metric.Daily == nil {
			metric.Daily = []DailyEdgeBandwidth{}
		}

		if query.Edges {
			metric.Edges = []string{f.entry.Edges[0].Hex(), f.entry.Edges[1].Hex()}
		}

		// Process latency result from the durable lat:<id> key.
		if query.Latency && latencyResults != nil {
			dataJSON, err := latencyResults[i].Result()
			if err == nil {
				var rec LatencyRecord
				if json.Unmarshal([]byte(dataJSON), &rec) == nil && rec.Avg > 0 {
					metric.Latency = &TransportLatency{
						Min: rec.Min,
						Max: rec.Max,
						Avg: rec.Avg,
					}
				}
			}
		}

		// Skip transports without any metrics data
		if metric.Latency == nil && len(metric.Daily) == 0 {
			continue
		}

		results = append(results, metric)
	}

	if results == nil {
		return []TransportMetric{}, nil
	}
	return results, nil
}
