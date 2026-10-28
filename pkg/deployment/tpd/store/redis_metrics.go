// Package store pkg/deployment/tpd/store/redis_metrics.go c4-net-discovery
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// transportDailyBandwidth returns a transport's true daily throughput in
// bytes (both directions) from its daily bandwidth hash.
//
// Each edge of a transport reports the FULL transport throughput
// independently — a managed transport counts both the bytes it sends and
// the bytes it receives (pkg/transport/managed_transport.go logSent/logRecv).
// So edge A's (sent+recv) ≈ edge B's (sent+recv) ≈ the real bytes that
// crossed the wire. Summing both reporters (as the legacy combined
// "bandwidth" field does) double-counts. We instead take the MAX across
// reporters of (sent+recv): exact for a single-reporter transport, and the
// most-complete single observation when both edges report. Falls back to the
// legacy combined field only for old redis data lacking per-reporter fields.
func transportDailyBandwidth(result map[string]string) uint64 {
	perReporter := make(map[string]uint64)
	for k, v := range result {
		var suffix string
		switch {
		case strings.HasSuffix(k, ":sent"):
			suffix = ":sent"
		case strings.HasSuffix(k, ":recv"):
			suffix = ":recv"
		default:
			continue
		}
		var n uint64
		fmt.Sscanf(v, "%d", &n) //nolint:errcheck,gosec
		perReporter[strings.TrimSuffix(k, suffix)] += n
	}
	if len(perReporter) > 0 {
		var best uint64
		for _, total := range perReporter {
			if total > best {
				best = total
			}
		}
		return best
	}
	// Legacy fallback: the combined field may double-count two-edge
	// transports written before per-reporter accounting existed.
	var bw uint64
	if val, ok := result["bandwidth"]; ok {
		fmt.Sscanf(val, "%d", &bw) //nolint:errcheck,gosec
	}
	return bw
}

// getP2PTransportCounts returns a map of visor PK hex → count of its
// transports of every type but dmsg. A visor is considered online when it has
// 2+ of them, peer-to-peer participation rather than dmsg infrastructure
// connectivity alone. Counted from the live set when this store keeps one,
// which is what every other transport read answers from.
func (s *redisStore) getP2PTransportCounts(ctx context.Context) (map[string]int, error) {
	if entries, ok := s.live.snapshot(false, false, time.Now()); ok {
		by := make(map[cipher.PubKey]int)
		for _, e := range entries {
			if e.Type == tptypes.DMSG {
				continue
			}
			by[e.Edges[0]]++
			by[e.Edges[1]]++
		}
		counts := make(map[string]int, len(by))
		for pk, n := range by {
			counts[pk.Hex()] = n
		}
		return counts, nil
	}
	keys, ids, err := s.allTransportKeysFromIndex(ctx)
	if err != nil {
		return nil, err
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
			// These counts decide online status — GetAllVisorSummaries does
			// `online := p2pCounts[pk] >= minP2PTransportsOnline` — and uptime
			// feeds the reward threshold. A batch skipped silently removes up
			// to mgetBatch transports, pushing visors below the bar and
			// marking them offline when they are not. Refuse to answer rather
			// than answer wrongly.
			return nil, fmt.Errorf("p2p transport counts are incomplete: batch %d-%d failed: %w", i, end, err)
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
			if data.Type != string(tptypes.DMSG) {
				counts[data.EdgeA]++
				if data.EdgeA != data.EdgeB {
					counts[data.EdgeB]++
				}
			}
		}
	}
	s.maybeReapStaleTransports(stale)

	return counts, nil
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

	// De-double-counted total: both edges report the full throughput, so
	// take the max across reporters rather than the summed combined field.
	agg.Bandwidth = transportDailyBandwidth(result)
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

	// The running totals answer every day they are complete for (see
	// redis_daily_totals.go); only the rest is scanned transport by
	// transport.
	dates := MetricsWindowDates(now, days)
	totals, err := s.dailyTotals(ctx, dates, query)
	if err != nil {
		return nil, err
	}

	var entries []*transport.Entry
	if len(totals) < days {
		entries, err = s.getAllTransportsWithQoS(ctx, true)
		if err != nil && !errors.Is(err, ErrTransportNotFound) {
			return nil, err
		}
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

	ix := s.bwIndex.peek()
	pipe := s.batchedPipe(ctx)
	for d := 0; d < days; d++ {
		t := now.AddDate(0, 0, -d)
		dateStr := t.Format("2006-01-02")
		if _, done := totals[dateStr]; done {
			continue
		}
		for i, entry := range entries {
			if !ix.mayHave(entry.ID, t, d) {
				continue
			}
			key := s.bandwidthDailyKey(entry.ID.String(), t)
			bwKeys = append(bwKeys, bwKey{
				dayIdx:   d,
				entryIdx: i,
				tpType:   string(entry.Type),
				dateStr:  dateStr,
			})
			bwResults = append(bwResults, pipe.next().HGetAll(ctx, key))
		}
	}
	if err := pipe.exec(); err != nil {
		return nil, err
	}

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

		bw := transportDailyBandwidth(result)
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
		if agg, done := totals[dates[d]]; done {
			if agg != nil {
				response.Daily = append(response.Daily, *agg)
				addCumulative(response.Cumulative, agg)
			}
			continue
		}
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
					var bw, sent, recv uint64
					if val, ok := bwResult["bandwidth"]; ok {
						fmt.Sscanf(val, "%d", &bw) //nolint:errcheck,gosec
					}
					if val, ok := bwResult["sent"]; ok {
						fmt.Sscanf(val, "%d", &sent) //nolint:errcheck,gosec
					}
					if val, ok := bwResult["recv"]; ok {
						fmt.Sscanf(val, "%d", &recv) //nolint:errcheck,gosec
					}
					// Legacy rows have only the combined field; split evenly.
					if sent == 0 && recv == 0 && bw > 0 {
						sent, recv = bw/2, bw/2
					}
					if total := sent + recv; total > 0 {
						dailyAgg.Bandwidth = &VisorBandwidthAggregate{
							Sent:  sent,
							Recv:  recv,
							Total: total,
						}
						totalSent += sent
						totalRecv += recv
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
	if err != nil && !errors.Is(err, ErrTransportNotFound) {
		return nil, err
	}

	// Include transports that still have bandwidth history within the query
	// window but are no longer registered (deregistered via `cli tp rm`, or
	// expired because a visor stopped re-registering). Their bw:daily:* keys
	// have a 35-day TTL and outlive the ~5-minute registration TTL, so the
	// bandwidth they carried must keep counting toward the metrics + rewards
	// even after the transport drops offline. Without this, a removed/offline
	// transport's entire daily history vanished from /metrics — which made the
	// bandwidth-history chart shrink over time and would undercount rewards.
	// These transports are reported with Live=false.
	registered := make(map[uuid.UUID]bool, len(entries))
	for _, e := range entries {
		registered[e.ID] = true
	}
	expiredEntries, expiredIDs, err := s.expiredTransportEntries(ctx, registered, query.Days)
	if err != nil {
		return nil, err
	}
	entries = append(entries, expiredEntries...)

	return s.buildTransportMetrics(ctx, entries, expiredIDs, query)
}

// bandwidthEdgesValue is the bw:edges value for a transport: its edge pair,
// in entry order, and its type.
func bandwidthEdgesValue(e *transport.Entry) string {
	return e.Edges[0].Hex() + "," + e.Edges[1].Hex() + "," + string(e.Type)
}

// parseBandwidthEdgePair parses a bw:edges value back into the edge pair and
// type. A value written before the type was kept has no type.
func parseBandwidthEdgePair(v string) ([2]cipher.PubKey, string, bool) {
	parts := strings.Split(v, ",")
	if len(parts) != 2 && len(parts) != 3 {
		return [2]cipher.PubKey{}, "", false
	}
	var edges [2]cipher.PubKey
	for i, p := range parts[:2] {
		if err := edges[i].UnmarshalText([]byte(p)); err != nil {
			return [2]cipher.PubKey{}, "", false
		}
	}
	typ := ""
	if len(parts) == 3 {
		typ = internType(parts[2])
	}
	return edges, typ, true
}

// internType returns the canonical string for a known transport type, or a copy
// of an unknown one. A substring of the bw:edges value would keep the whole
// value alive in the bandwidth index, one per transport.
var knownTypes = tptypes.Known()

func internType(s string) string {
	for _, t := range knownTypes {
		if string(t) == s {
			return string(t)
		}
	}
	return strings.Clone(s)
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

// parseBWUint parses a decimal bandwidth counter, returning 0 on malformed
// input. It replaces fmt.Sscanf in the hot per-(entry×day) loop, which
// allocated a reflect-based scan state on every call.
func parseBWUint(s string) uint64 {
	v, _ := strconv.ParseUint(s, 10, 64) //nolint:errcheck // malformed counter reads as 0 by design (see doc comment)
	return v
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

	// Precompute uuid.String() once per entry — it was re-run per (entry × day)
	// building the daily-bandwidth keys below AND again per entry at the end.
	// On the 30-day window over ~13k transports that is ~390k → ~13k calls.
	idStrs := make([]string, len(filtered))
	for i := range filtered {
		idStrs[i] = filtered[i].entry.ID.String()
	}

	// Fetch latency data via pipeline. Reads the lat:<id> key rather than
	// the tp:<id> registration blob, which a missed re-registration drops;
	// it lives for latencyTTL after the transport's last report.
	var latencyResults []*redis.StringCmd
	if query.Latency {
		pipe := s.batchedPipe(ctx)
		latencyResults = make([]*redis.StringCmd, len(filtered))
		for i, f := range filtered {
			latencyResults[i] = pipe.next().Get(ctx, s.latencyKey(f.entry.ID))
		}
		if err := pipe.exec(); err != nil {
			return nil, err
		}
	}

	// Bandwidth is read a chunk of transports at a time and folded into
	// bwByEntry before the next chunk is fetched, so only one chunk of
	// HGETALL replies is ever held as maps.
	bwByEntry := make([][]DailyEdgeBandwidth, len(filtered))
	// dateStrs[d] is the "2006-01-02" string for day d; shared across all
	// entries so it is formatted `days` times, not once per (entry × day).
	var dateStrs []string

	// A visor is an edge of many transports; encode each key once and share it.
	hexes := make(map[cipher.PubKey]string)
	hexOf := func(pk cipher.PubKey) string {
		h, ok := hexes[pk]
		if !ok {
			h = pk.Hex()
			hexes[pk] = h
		}
		return h
	}

	if query.Bandwidth {
		dateStrs = make([]string, days)
		for d := 0; d < days; d++ {
			dateStrs[d] = now.AddDate(0, 0, -d).Format("2006-01-02")
		}
		ix := s.bwIndex.peek()
		for lo := 0; lo < len(filtered); {
			// Same key as bandwidthDailyKey ("<svc>:bw:daily:<id>:<date>")
			// but built by concat (single alloc, no fmt reflection) with the
			// id + date precomputed above rather than re-formatting per day.
			var bwDays [][]int
			queued := 0
			hi := lo
			for hi < len(filtered) && queued < metricsChunkCmds {
				var fetch []int
				for _, d := range ix.fetchDays(filtered[hi].entry.ID, now, days) {
					if s.liveBandwidthDay(d) { // the rest is from the archive below
						fetch = append(fetch, d)
					}
				}
				bwDays = append(bwDays, fetch)
				queued += len(fetch)
				hi++
			}
			cmds := make([]*redis.StringStringMapCmd, 0, queued)
			pipe := s.batchedPipe(ctx)
			for i := lo; i < hi; i++ {
				for _, d := range bwDays[i-lo] {
					key := serviceName + ":bw:daily:" + idStrs[i] + ":" + dateStrs[d]
					cmds = append(cmds, pipe.next().HGetAll(ctx, key))
				}
			}
			if err := pipe.exec(); err != nil {
				return nil, err
			}
			n := 0
			for i := lo; i < hi; i++ {
				f := filtered[i]
				// The four field keys depend only on the entry.
				edgeAHex := hexOf(f.entry.Edges[0])
				edgeBHex := hexOf(f.entry.Edges[1])
				kASent, kARecv := edgeAHex+":sent", edgeAHex+":recv"
				kBSent, kBRecv := edgeBHex+":sent", edgeBHex+":recv"
				for _, d := range bwDays[i-lo] {
					result, err := cmds[n].Result()
					cmds[n] = nil
					n++
					if err != nil || len(result) == 0 {
						continue
					}
					if m, ok := dailyBandwidth(result, dateStrs[d], kASent, kARecv, kBSent, kBRecv); ok {
						bwByEntry[i] = append(bwByEntry[i], m)
					}
				}
			}
			lo = hi
		}
	}

	// Days past the live window come from the archived leaves, newest first
	// after the live ones, as the redis rows were.
	if query.Bandwidth && s.leafArchive != "" {
		for d := bandwidthDailyLiveDays; d < days; d++ {
			day := s.archivedDay(ctx, dateStrs[d])
			for i := range filtered {
				if row, ok := day[idStrs[i]]; ok {
					bwByEntry[i] = append(bwByEntry[i], row)
				}
			}
		}
	}

	// Build results
	results := make([]TransportMetric, 0, len(filtered))
	for i, f := range filtered {
		metric := TransportMetric{
			ID:    idStrs[i],
			Type:  string(f.entry.Type),
			Live:  f.isLive,
			Daily: bwByEntry[i],
		}
		if metric.Daily == nil {
			metric.Daily = []DailyEdgeBandwidth{}
		}

		if query.Edges {
			metric.Edges = []string{hexOf(f.entry.Edges[0]), hexOf(f.entry.Edges[1])}
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

// metricsChunkCmds is how many daily-hash reads buildTransportMetrics holds
// at once. Each reply is a map, so the whole window at once is the startup
// memory peak.
const metricsChunkCmds = 2000

// dailyBandwidth reads one transport's day from its hash. The k* arguments are
// the sent and recv fields of its two edges, A then B. It reports false when
// the day carries no bandwidth.
func dailyBandwidth(result map[string]string, dateStr, kASent, kARecv, kBSent, kBRecv string) (DailyEdgeBandwidth, bool) {
	var aSent, aRecv, bSent, bRecv uint64
	hasPerEdge := false
	if val, ok := result[kASent]; ok {
		aSent = parseBWUint(val)
		hasPerEdge = true
	}
	if val, ok := result[kARecv]; ok {
		aRecv = parseBWUint(val)
		hasPerEdge = true
	}
	if val, ok := result[kBSent]; ok {
		bSent = parseBWUint(val)
		hasPerEdge = true
	}
	if val, ok := result[kBRecv]; ok {
		bRecv = parseBWUint(val)
		hasPerEdge = true
	}
	if hasPerEdge && (aSent+aRecv+bSent+bRecv) > 0 {
		_, sameNet := result[sameNetworkField]
		m := DailyEdgeBandwidth{
			Date:        dateStr,
			A:           &EdgeBandwidth{Sent: aSent, Recv: aRecv},
			B:           &EdgeBandwidth{Sent: bSent, Recv: bRecv},
			SameNetwork: sameNet,
		}
		verifiedFields(result, kASent, m.A)
		verifiedFields(result, kBSent, m.B)
		return m, true
	}
	// Old data keeps one combined total, split equally.
	if bw := parseBWUint(result["bandwidth"]); bw > 0 {
		half := bw / 2
		return DailyEdgeBandwidth{
			Date: dateStr,
			A:    &EdgeBandwidth{Sent: half, Recv: half},
			B:    &EdgeBandwidth{Sent: half, Recv: half},
		}, true
	}
	return DailyEdgeBandwidth{}, false
}
