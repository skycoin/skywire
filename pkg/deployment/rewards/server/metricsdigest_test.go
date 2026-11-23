// Package rewardsserver pkg/deployment/rewards/server/metricsdigest_test.go c3-app-rewards
package rewardsserver

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	tpdstore "github.com/skycoin/skywire/pkg/deployment/tpd/store"
)

func digestRow(date string, as, ar, bs, br uint64) tpdstore.DailyEdgeBandwidth {
	return tpdstore.DailyEdgeBandwidth{
		Date: date,
		A:    &tpdstore.EdgeBandwidth{Sent: as, Recv: ar},
		B:    &tpdstore.EdgeBandwidth{Sent: bs, Recv: br},
	}
}

func digestLeaf(t *testing.T, recs ...tpdstore.TransportMetric) []byte {
	b, err := json.Marshal(recs)
	require.NoError(t, err)
	return b
}

// digestFixture is four days of feed leaves, newest first.
func digestFixture(t *testing.T) (map[string][][]byte, []string) {
	byDate := map[string][][]byte{
		// Two parts, one gzipped. tp-1 has no edges on its newest day.
		"2026-11-22": {
			cxoutils.Gzip(digestLeaf(t, tpdstore.TransportMetric{ID: "tp-1", Daily: []tpdstore.DailyEdgeBandwidth{digestRow("2026-11-22", 10, 20, 30, 40)}})),
			digestLeaf(t, tpdstore.TransportMetric{ID: "tp-2", Edges: []string{pkB, pkC}, Daily: []tpdstore.DailyEdgeBandwidth{
				{Date: "2026-11-22", A: &tpdstore.EdgeBandwidth{Sent: 5}},
			}}),
		},
		"2026-11-21": {
			digestLeaf(t,
				tpdstore.TransportMetric{ID: "tp-1", Edges: []string{pkA, pkB}, Daily: []tpdstore.DailyEdgeBandwidth{digestRow("2026-11-21", 1, 2, 3, 4)}},
				tpdstore.TransportMetric{ID: "tp-3", Edges: []string{pkA, pkC}, Daily: []tpdstore.DailyEdgeBandwidth{digestRow("2026-11-21", 0, 0, 7, 0)}},
			),
		},
		// A malformed part drops its whole day.
		"2026-11-20": {
			digestLeaf(t, tpdstore.TransportMetric{ID: "tp-9", Edges: []string{pkA, pkB}, Daily: []tpdstore.DailyEdgeBandwidth{digestRow("2026-11-20", 9, 9, 9, 9)}}),
			[]byte("{not json"),
		},
		// tp-2's older edges are ignored for the newer ones.
		"2026-11-19": {
			digestLeaf(t, tpdstore.TransportMetric{ID: "tp-2", Edges: []string{pkC, pkA}, Daily: []tpdstore.DailyEdgeBandwidth{digestRow("2026-11-19", 100, 0, 0, 1)}}),
		},
	}
	dates := []string{"2026-11-22", "2026-11-21", "2026-11-20", "2026-11-19"}
	return byDate, dates
}

// TestMetricsDigestMatchesMergedRecords checks the digest, built one day at a
// time, against the figures computed from MergeDailyMetrics over the same
// leaves, which is what the pages computed before the digest.
func TestMetricsDigestMatchesMergedRecords(t *testing.T) {
	byDate, dates := digestFixture(t)

	for _, days := range []int{1, 2, 3, 30} {
		got := digestMetricsDays(metricsDayPartsOf(byDate), dates, days)

		window := dates
		if len(window) > days {
			window = window[:days]
		}
		var perDay [][]tpdstore.TransportMetric
		for _, d := range window {
			if recs, ok := decodeMetricsDay(byDate[d]); ok {
				perDay = append(perDay, recs)
			}
		}
		merged := tpdstore.MergeDailyMetrics(perDay)
		wantEdges := make(map[string]int)
		for i := range merged {
			for _, e := range merged[i].Edges {
				wantEdges[e]++
			}
		}
		require.Equal(t, visorDaysMap(visorDailyFromMetrics(merged)), visorDaysMap(got.daily), "daily, %d days", days)
		require.Equal(t, wantEdges, got.edges, "edges, %d days", days)

		newest, ok := decodeMetricsDay(byDate[dates[0]])
		require.True(t, ok)
		var s tpdNetworkSummary
		accumulateVerifiedBandwidth(tpdstore.MergeDailyMetrics([][]tpdstore.TransportMetric{newest}), &s)
		require.Equal(t, s.TotalBandwidth, got.dayTotal)
		require.Empty(t, got.dayErr)
		require.Empty(t, got.winErr)
	}
}

func TestMetricsDigestNamesAnUnparsedWindow(t *testing.T) {
	byDate := map[string][][]byte{"2026-11-22": {[]byte("{")}}
	got := digestMetricsDays(metricsDayPartsOf(byDate), []string{"2026-11-22"}, 30)
	require.NotEmpty(t, got.dayErr)
	require.NotEmpty(t, got.winErr)
	require.Nil(t, got.edges)
}

func visorDaysMap(v *visorDays) map[string]map[string]uint64 {
	out := make(map[string]map[string]uint64)
	for pk := range v.bytes {
		if days := v.of(pk); len(days) > 0 {
			out[pk] = days
		}
	}
	return out
}

// TestEachMetricAgreesWithDecodeMetricsDay checks the streaming decoder
// accepts and refuses the same days as the whole-body one.
func TestEachMetricAgreesWithDecodeMetricsDay(t *testing.T) {
	good := digestLeaf(t, tpdstore.TransportMetric{ID: "tp-1", Edges: []string{pkA, pkB}})
	for name, bodies := range map[string][][]byte{
		"raw":            {good},
		"gzip":           {cxoutils.Gzip(good)},
		"two parts":      {good, cxoutils.Gzip(good)},
		"null part":      {[]byte("null"), good},
		"only null":      {[]byte("null")},
		"empty array":    {[]byte("[]")},
		"object":         {[]byte(`{"id":"tp-1"}`)},
		"trailing data":  {append(append([]byte{}, good...), []byte(" []")...)},
		"truncated":      {good[:len(good)-1]},
		"bad gzip":       {[]byte{0x1f, 0x8b, 1, 2, 3}},
		"wrong type":     {[]byte(`[{"id":5}]`)},
		"one bad part":   {good, []byte("[")},
		"spaces":         {[]byte("  [ ]  ")},
		"no parts":       {},
		"string element": {[]byte(`["x"]`)},
	} {
		want, wantOK := decodeMetricsDay(bodies)
		var got []tpdstore.TransportMetric
		gotOK := eachMetric(bodies, func(m *tpdstore.TransportMetric) { got = append(got, *m) })
		require.Equal(t, wantOK, gotOK, name)
		if wantOK {
			require.Equal(t, want, got, name)
		}
	}
}

// metricsFeedLocal is transport discovery in this process over day leaves,
// counting the reads of each day.
type metricsFeedLocal struct {
	byDate map[string][][]byte
	dates  []string
	reads  map[string]int
}

func (l *metricsFeedLocal) TransportMetricsRead(context.Context, tpdstore.MetricsQuery) ([]tpdstore.TransportMetric, error) {
	return nil, errors.New("not read by the digest")
}

func (l *metricsFeedLocal) MetricsDaysRead() []string { return l.dates }

func (l *metricsFeedLocal) MetricsDayPartsRead(date string) ([][]byte, bool) {
	l.reads[date]++
	p, ok := l.byDate[date]
	// A subscriber walks a day's parts in no fixed order, so give them reversed.
	r := slices.Clone(p)
	slices.Reverse(r)
	return r, ok && len(p) > 0
}

// TestMetricsDigestLocalMatchesFeed checks that the digest built from
// transport discovery in this process equals the one built from a fill of
// the feed over the same day leaves, and that only the open days are held
// between the two passes.
func TestMetricsDigestLocalMatchesFeed(t *testing.T) {
	byDate, dates := digestFixture(t)
	local := &metricsFeedLocal{byDate: byDate, dates: dates, reads: map[string]int{}}

	fromFeed := digestMetricsDays(metricsDayPartsOf(byDate), dates, tpdDailyDays)
	fromLocal, err := metricsDigestLocal(local)
	require.NoError(t, err)

	require.Equal(t, visorDaysMap(fromFeed.daily), visorDaysMap(fromLocal.daily))
	require.Equal(t, fromFeed.edges, fromLocal.edges)
	require.Equal(t, fromFeed.dayTotal, fromLocal.dayTotal)
	require.Equal(t, fromFeed.dayErr, fromLocal.dayErr)
	require.Equal(t, fromFeed.winErr, fromLocal.winErr)
	require.NotZero(t, fromLocal.dayTotal)
	require.NotEmpty(t, fromLocal.edges)

	// The two open days are read once, settled days once per pass, and the
	// malformed day only once, since the second pass skips it.
	require.Equal(t, map[string]int{"2026-11-22": 1, "2026-11-21": 1, "2026-11-20": 1, "2026-11-19": 2}, local.reads)

	_, err = metricsDigestLocal(&metricsFeedLocal{reads: map[string]int{}})
	require.Error(t, err, "nothing published yet")
}
