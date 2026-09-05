// Package store pkg/deployment/tpd/store/cxo_metrics_layout.go c4-net-discovery
//
// Layout of TPD's transport-metrics CXO feed, plus the pivot/merge
// pair that converts between it and the []TransportMetric window
// shape every caller already expects.
//
// The feed publishes ONE LEAF PER CALENDAR DAY at
//
//	metrics/day/<YYYY-MM-DD>
//
// rather than one leaf per day-window. A window (1, 7, 30 days) is
// assembled reader-side from the N newest day leaves. The point is
// that a past day cannot change, so its leaf hashes the same on
// every republish and CXO ships nothing for it — whereas one leaf
// per window meant a 30-day body was a new object every time any
// single byte moved, and the whole multi-megabyte window went back
// over the wire even though 29 of its 30 days were settled.
//
// Two fields are current-state, not day-scoped: Live (a transport's
// registration can expire at any moment) and Latency (a rolling
// min/max/avg that moves constantly). Carrying them in every day
// leaf would make every past day mutable again and defeat the whole
// exercise — across tens of thousands of transports, at least one
// liveness flip between cycles is a certainty, so every leaf would
// be a new object every time. They therefore live ONLY in the
// newest day's leaf, which is rewritten every cycle anyway. Older
// leaves carry the day-scoped facts and the transport's stable
// identity (ID, Type, Edges). MergeDailyMetrics walks the days
// newest-first, so the newest leaf's values win and the merged
// record has the shape it always had.
//
// The rule this implies, stated plainly: the current day's leaf
// holds exactly what a 1-day query returns — every transport with a
// latency measurement or bandwidth today — and a transport that
// appears in NO current-day record reads back Live=false with no
// latency. That is the one behavioral difference from the old
// per-window bodies, where a 30-day window reported current
// liveness for a transport last seen three weeks ago. In practice a
// registered transport has a latency record (lat:<id> outlives
// registration by 35 days), so it is in the current leaf regardless
// of whether it moved bytes today.
//
// These live in the store package rather than next to the publisher
// because they are the contract BETWEEN the publisher and its
// readers (pkg/visor, pkg/tpviz), and TransportMetric — the thing
// being pivoted — is defined right here.
package store

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// MetricsDayPrefix is the sub-tree the per-day leaves live under. It
// is also what a subscriber's path prefix must cover.
const MetricsDayPrefix = "metrics/day/"

// MetricsDateFormat is the calendar-date layout used in a day leaf's
// path. It matches the format the bw:daily:<id>:<date> Redis keys
// already use, so a leaf's name and its source rows agree.
const MetricsDateFormat = "2006-01-02"

// MetricsDayPath returns the leaf path for one calendar day. Dates
// in this format sort lexically into chronological order, so a
// reader recovers "the N newest days" from a plain string sort.
func MetricsDayPath(date string) string { return MetricsDayPrefix + date }

// MetricsDayPartPath returns the path of one part of a day leaf that
// did not fit in a single CXO object. Zero-padded so a lexical sort
// of the paths is the publication order.
func MetricsDayPartPath(date string, part int) string {
	return fmt.Sprintf("%s/part/%04d", MetricsDayPath(date), part)
}

// MetricsDayDate extracts the calendar date from a day-leaf path or
// from one of its part paths. ok is false for any path that is not
// under MetricsDayPrefix — including the legacy metrics/days/<n>
// window leaves, which a reader must not mistake for a day.
func MetricsDayDate(path string) (string, bool) {
	if !strings.HasPrefix(path, MetricsDayPrefix) {
		return "", false
	}
	rest := path[len(MetricsDayPrefix):]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	if rest == "" {
		return "", false
	}
	return rest, true
}

// MetricsWindowDates returns the calendar dates of a `days`-long
// window ending at now, NEWEST FIRST. The stepping matches
// buildTransportMetrics exactly (UTC, one AddDate per day back) so a
// pivoted row always finds its date in the window.
func MetricsWindowDates(now time.Time, days int) []string {
	if days < 1 {
		days = 1
	}
	now = now.UTC()
	out := make([]string, 0, days)
	for d := 0; d < days; d++ {
		out = append(out, now.AddDate(0, 0, -d).Format(MetricsDateFormat))
	}
	return out
}

// PivotDailyMetrics splits a window's metrics into one slice per
// calendar day. dates is the window newest-first (as returned by
// MetricsWindowDates); every date gets an entry, empty days
// included, so the published path set is a deterministic function of
// the window rather than of the data.
//
// dates[0] — the current day — is the only leaf that carries Live
// and Latency, and it carries a record for every transport that has
// latency at all, not just those with bandwidth today. That keeps
// pkg/tpviz's latency graph (which reads a single day) seeing
// exactly the transports the old 1-day window showed it.
//
// A record with neither latency nor a row on the current day is
// omitted from the current leaf, mirroring the store's own rule that
// a transport with no metrics data is not reported.
func PivotDailyMetrics(metrics []TransportMetric, dates []string) map[string][]TransportMetric {
	byDate := make(map[string][]TransportMetric, len(dates))
	for _, d := range dates {
		byDate[d] = []TransportMetric{}
	}
	if len(dates) == 0 {
		return byDate
	}
	current := dates[0]

	for i := range metrics {
		m := &metrics[i]
		var currentRows []DailyEdgeBandwidth
		for _, row := range m.Daily {
			if row.Date == current {
				currentRows = append(currentRows, row)
				continue
			}
			if _, inWindow := byDate[row.Date]; !inWindow {
				continue
			}
			byDate[row.Date] = append(byDate[row.Date], TransportMetric{
				ID:    m.ID,
				Type:  m.Type,
				Edges: m.Edges,
				Daily: []DailyEdgeBandwidth{row},
			})
		}
		if m.Latency == nil && len(currentRows) == 0 {
			continue
		}
		if currentRows == nil {
			currentRows = []DailyEdgeBandwidth{}
		}
		byDate[current] = append(byDate[current], TransportMetric{
			ID:      m.ID,
			Type:    m.Type,
			Live:    m.Live,
			Edges:   m.Edges,
			Latency: m.Latency,
			Daily:   currentRows,
		})
	}

	// Sorting is not cosmetic: an unsorted leaf would re-encode to
	// different bytes every cycle just because Redis handed the
	// transports back in a different order, and a body that changes
	// for no reason is exactly the retransmission this layout exists
	// to avoid.
	for d := range byDate {
		sortMetricsByID(byDate[d])
	}
	return byDate
}

// MergeDailyMetrics rebuilds the []TransportMetric window shape from
// day slices given NEWEST FIRST. Records are joined on transport ID;
// daily rows accumulate in the order the days were supplied (newest
// first, matching buildTransportMetrics), and the first day that
// mentions a transport supplies its Live/Latency/Type/Edges.
//
// The result is sorted by ID so a window assembled from the same
// leaves is byte-identical every time.
func MergeDailyMetrics(days [][]TransportMetric) []TransportMetric {
	idx := make(map[string]int)
	out := make([]TransportMetric, 0)
	for _, day := range days {
		for i := range day {
			src := &day[i]
			j, seen := idx[src.ID]
			if !seen {
				out = append(out, TransportMetric{
					ID:      src.ID,
					Type:    src.Type,
					Live:    src.Live,
					Edges:   src.Edges,
					Latency: src.Latency,
					Daily:   append([]DailyEdgeBandwidth{}, src.Daily...),
				})
				idx[src.ID] = len(out) - 1
				continue
			}
			out[j].Daily = append(out[j].Daily, src.Daily...)
			// Older leaves omit the current-state fields by design;
			// fill from a later day only if the newest one had none
			// (which is also what makes a mixed old/new body safe).
			if out[j].Type == "" {
				out[j].Type = src.Type
			}
			if len(out[j].Edges) == 0 {
				out[j].Edges = src.Edges
			}
			if out[j].Latency == nil {
				out[j].Latency = src.Latency
			}
		}
	}
	sortMetricsByID(out)
	return out
}

func sortMetricsByID(m []TransportMetric) {
	sort.Slice(m, func(i, j int) bool { return m[i].ID < m[j].ID })
}
