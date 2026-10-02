package visor

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReduceTransportMetrics_FoldsAndCaps(t *testing.T) {
	body := `[
	 {"id":"a","type":"stcpr","live":true,"edges":["pk1","pk2"],
	  "latency":{"min":1,"max":3,"avg":2},
	  "daily":[{"date":"d1","a":{"sent":100,"recv":10},"b":{"sent":5,"recv":60}}]},
	 {"id":"b","type":"dmsg","live":false,"edges":["pk3","pk4"],
	  "daily":[{"date":"d1","a":{"sent":7,"recv":0},"b":null}]},
	 {"id":"skipped-one-edge","type":"stcpr","live":true,"edges":["pk5"],
	  "daily":[{"date":"d1","a":{"sent":999,"recv":999},"b":null}]},
	 {"id":"skipped-idle","type":"stcpr","live":true,"edges":["pk6","pk7"],
	  "daily":[{"date":"d1","a":{"sent":0,"recv":0},"b":{"sent":0,"recv":0}}]}
	]`

	got, err := reduceTransportMetrics([]byte(body), 0, false)
	require.NoError(t, err)

	// Every record TPD sent is counted, including the ones not rendered.
	require.Equal(t, 4, got.Total)
	require.Equal(t, 2, got.Returned)
	require.False(t, got.Partial)

	// Where both edges reported, the smaller figure wins: a says it sent 100,
	// b says it received 60, so 60 is the believable number.
	require.Equal(t, "a", got.Metrics[0].ID)
	require.Equal(t, uint64(60), got.Metrics[0].Sent)
	require.Equal(t, uint64(5), got.Metrics[0].Recv)

	// Only one edge reported: take its figures as-is.
	require.Equal(t, "b", got.Metrics[1].ID)
	require.Equal(t, uint64(7), got.Metrics[1].Sent)

	// Sorted by bandwidth, descending.
	require.Greater(t, got.Metrics[0].Sent+got.Metrics[0].Recv,
		got.Metrics[1].Sent+got.Metrics[1].Recv)

	// The daily array is gone from the wire; the fold replaced it.
	require.NotContains(t, fmt.Sprintf("%+v", got.Metrics[0]), "daily")
}

// A truncated body is the normal case on a large deployment: TPD's response is
// cut off mid-record upstream. Everything that arrived must survive.
func TestReduceTransportMetrics_TruncatedBodyKeepsWhatArrived(t *testing.T) {
	var b strings.Builder
	b.WriteString("[")
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&b, `{"id":"tp%02d","type":"stcpr","live":true,"edges":["pk%d","pk%d"],`, i, i, i+1)
		fmt.Fprintf(&b, `"daily":[{"date":"d1","a":{"sent":%d,"recv":0},"b":null}]},`, (i+1)*10)
	}
	// Cut mid-record, exactly as the wire does.
	b.WriteString(`{"id":"tp50","type":"stcpr","live":true,"edges":["pkA","pk`)

	got, err := reduceTransportMetrics([]byte(b.String()), 0, false)
	require.NoError(t, err, "a truncated array must not be an error")
	require.True(t, got.Partial, "truncation must be reported")
	require.Equal(t, 50, got.Total)
	require.Equal(t, 50, got.Returned)

	// Highest bandwidth first, so the cap keeps the interesting rows.
	require.Equal(t, "tp49", got.Metrics[0].ID)
	require.Equal(t, uint64(500), got.Metrics[0].Sent)
}

func TestReduceTransportMetrics_CapBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString("[")
	for i := 0; i < 30; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"id":"tp%02d","type":"stcpr","live":true,"edges":["pk%d","pk%d"],`, i, i, i+1)
		fmt.Fprintf(&b, `"daily":[{"date":"d1","a":{"sent":%d,"recv":0},"b":null}]}`, (i+1)*10)
	}
	b.WriteString("]")

	got, err := reduceTransportMetrics([]byte(b.String()), 5, false)
	require.NoError(t, err)
	require.Equal(t, 30, got.Total, "total counts everything, not just what fits")
	require.Equal(t, 5, got.Returned)
	require.Len(t, got.Metrics, 5)

	// A caller cannot lift the cap past the point the browser chokes.
	got, err = reduceTransportMetrics([]byte(b.String()), maxMetricsLimit*10, false)
	require.NoError(t, err)
	require.Equal(t, 30, got.Returned, "all 30 fit under the ceiling")
}

func TestReduceTransportMetrics_NonArrayIsAnError(t *testing.T) {
	_, err := reduceTransportMetrics([]byte(`{"error":"tpd exploded"}`), 0, false)
	require.ErrorIs(t, err, errNotAnArray)
}

func TestReduceTransportMetrics_EmptyArray(t *testing.T) {
	got, err := reduceTransportMetrics([]byte(`[]`), 0, false)
	require.NoError(t, err)
	require.Equal(t, 0, got.Total)
	require.NotNil(t, got.Metrics, "must marshal as [] and not null")
}

// TPD keeps dead transports' bandwidth history, and on the real mesh they
// outnumber the living ten to one and carry the larger totals. Filtering has
// to precede the cap, or "live only" returns the handful of live rows that
// happened to fall inside the top N by bandwidth.
func TestReduceTransportMetrics_LiveFilterPrecedesTheCap(t *testing.T) {
	var b strings.Builder
	b.WriteString("[")
	// 20 dead transports with the HIGHEST bandwidth...
	for i := 0; i < 20; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"id":"dead%02d","type":"","live":false,"edges":["pkD%d","pkE%d"],`, i, i, i)
		fmt.Fprintf(&b, `"daily":[{"date":"d1","a":{"sent":%d,"recv":0},"b":null}]}`, 100000+i)
	}
	// ...and 5 live ones with much less.
	for i := 0; i < 5; i++ {
		fmt.Fprintf(&b, `,{"id":"live%02d","type":"stcpr","live":true,"edges":["pkL%d","pkM%d"],`, i, i, i)
		fmt.Fprintf(&b, `"daily":[{"date":"d1","a":{"sent":%d,"recv":0},"b":null}]}`, 10+i)
	}
	b.WriteString("]")

	// Unfiltered with a cap of 3: all three are dead, because they carried more.
	all, err := reduceTransportMetrics([]byte(b.String()), 3, false)
	require.NoError(t, err)
	require.Equal(t, 25, all.Total)
	require.Equal(t, 5, all.Live)
	for _, row := range all.Metrics {
		require.False(t, row.Live, "top-by-bandwidth is dead history on this data")
	}

	// Live only, same cap: three LIVE rows, not zero.
	live, err := reduceTransportMetrics([]byte(b.String()), 3, true)
	require.NoError(t, err)
	require.Equal(t, 25, live.Total, "total still counts the whole mesh")
	require.Equal(t, 5, live.Live)
	require.Equal(t, 3, live.Returned)
	for _, row := range live.Metrics {
		require.True(t, row.Live)
	}
	require.Equal(t, "live04", live.Metrics[0].ID, "highest-bandwidth live one first")
}
