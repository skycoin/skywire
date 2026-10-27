// Package store pkg/deployment/tpd/store/metrics_leaf_stream_test.go c4-net-discovery
package store

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
)

// leafDate is the day every fixture record has a row on.
const leafDate = "2026-10-20"

func leafFixture(t testing.TB, n int) []byte {
	const date = leafDate
	recs := make([]TransportMetric, 0, n)
	for i := 0; i < n; i++ {
		recs = append(recs, TransportMetric{
			ID:      fmt.Sprintf("%08x-1234-5678-9abc-%012x", i, i),
			Type:    []string{"stcpr", "sudph", "dmsg"}[i%3],
			Edges:   []string{fmt.Sprintf("02%064x", i), fmt.Sprintf("03%064x", i+1)},
			Latency: &TransportLatency{Min: 1, Max: int64(i), Avg: 2},
			Daily: []DailyEdgeBandwidth{
				{Date: "2000-01-01", A: &EdgeBandwidth{Sent: 1}},
				{Date: date, A: &EdgeBandwidth{Sent: uint64(i), Recv: 7}, B: &EdgeBandwidth{Sent: 3, Checked: i%2 == 0}, SameNetwork: i%5 == 0},
			},
		})
	}
	body, err := json.Marshal(recs)
	require.NoError(t, err)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err = zw.Write(body)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// oldLeafDayRows is the decode archivedDay did before it streamed.
func oldLeafDayRows(parts [][]byte, date string) (map[string]DailyEdgeBandwidth, error) {
	day := make(map[string]DailyEdgeBandwidth)
	for _, part := range parts {
		var recs []TransportMetric
		if err := json.Unmarshal(cxoutils.Gunzip(part), &recs); err != nil {
			return nil, err
		}
		for _, r := range recs {
			for _, d := range r.Daily {
				if d.Date == date {
					day[r.ID] = d
				}
			}
		}
	}
	return day, nil
}

func TestLeafDayRowsMatchesWholeDecode(t *testing.T) {
	const date = leafDate
	parts := [][]byte{leafFixture(t, 500), leafFixture(t, 3)}
	want, err := oldLeafDayRows(parts, date)
	require.NoError(t, err)
	got, err := leafDayRows(parts, date)
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Len(t, got, 500)

	n, err := CountMetricRecords(parts)
	require.NoError(t, err)
	require.Equal(t, 503, n)

	var all []TransportMetric
	require.NoError(t, EachMetricRecord(parts, func(r *TransportMetric) error {
		all = append(all, *r)
		return nil
	}))
	require.Len(t, all, 503)
	require.NotSame(t, all[0].Daily[1].A, all[1].Daily[1].A, "records must not share decoded values")
}

func TestLeafDayRowsRejectsBadLeaves(t *testing.T) {
	gz := leafFixture(t, 3)
	corrupt := append([]byte(nil), gz...)
	corrupt[len(corrupt)-6] ^= 0xff
	for name, part := range map[string][]byte{
		"crc":       corrupt,
		"truncated": gz[:len(gz)/2],
		"trailing":  []byte(`[] []`),
		"object":    []byte(`{}`),
		"empty":     nil,
	} {
		_, err := leafDayRows([][]byte{part}, "2026-10-20")
		require.Error(t, err, name)
	}
	got, err := leafDayRows([][]byte{[]byte(`[{"id":"x","daily":[{"date":"d"}]}]`)}, "d")
	require.NoError(t, err)
	require.Len(t, got, 1, "a raw body is accepted")
}

// BenchmarkLeafDayRows reads one production-sized archived day, ~15 MB of JSON.
func BenchmarkLeafDayRows(b *testing.B) {
	const date = leafDate
	parts := [][]byte{leafFixture(b, 50000)}
	for name, fn := range map[string]func([][]byte, string) (map[string]DailyEdgeBandwidth, error){
		"Whole": oldLeafDayRows, "Stream": leafDayRows,
	} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := fn(parts, date); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
