// Package api pkg/deployment/tpd/api/metrics_gzip_bench_test.go c4-net-discovery
package api

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
)

// benchRecords builds a day's worth of records shaped like the real feed:
// a transport id, a type, edges, and one day of per-edge bandwidth.
func benchRecords(n int) []store.TransportMetric {
	out := make([]store.TransportMetric, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, store.TransportMetric{
			ID:    fmt.Sprintf("%08x-1234-5678-9abc-%012x", i, i),
			Type:  []string{"stcpr", "sudph", "dmsg", "squicr"}[i%4],
			Live:  i%3 != 0,
			Edges: []string{fmt.Sprintf("02%064x", i), fmt.Sprintf("03%064x", i+1)},
			Daily: []store.DailyEdgeBandwidth{{
				Date: "2026-09-11",
				A:    &store.EdgeBandwidth{Sent: uint64(i) * 1013, Recv: uint64(i) * 971},
				B:    &store.EdgeBandwidth{Sent: uint64(i) * 877, Recv: uint64(i) * 643},
			}},
		})
	}
	return out
}

// gzipRecordsAtLevel is the pre-change encoder — stdlib json at the default
// level — kept here so the benchmark compares before against after rather than
// against a remembered number.
func gzipRecordsAtLevel(metrics []store.TransportMetric, level int) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, level)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write([]byte{'['}); err != nil {
		return nil, err
	}
	for i := range metrics {
		if i > 0 {
			if _, err := zw.Write([]byte{','}); err != nil {
				return nil, err
			}
		}
		b, err := json.Marshal(&metrics[i])
		if err != nil {
			return nil, err
		}
		if _, err := zw.Write(b); err != nil {
			return nil, err
		}
	}
	if _, err := zw.Write([]byte{']'}); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// The body must still be valid gzipped JSON, and the size cost of dropping to
// BestSpeed must be small enough to be worth the CPU.
func TestGzipRecords_StillValidAndNotMuchBigger(t *testing.T) {
	recs := benchRecords(13000) // the live transport count, near enough

	fast, err := gzipRecords(recs)
	require.NoError(t, err)
	slow, err := gzipRecordsAtLevel(recs, gzip.DefaultCompression)
	require.NoError(t, err)

	// Round-trips to the same records either way.
	for _, body := range [][]byte{fast, slow} {
		zr, err := gzip.NewReader(bytes.NewReader(body))
		require.NoError(t, err)
		var got []store.TransportMetric
		require.NoError(t, json.NewDecoder(zr).Decode(&got))
		require.Len(t, got, len(recs))
		require.Equal(t, recs[0].ID, got[0].ID)
	}

	grow := float64(len(fast))/float64(len(slow)) - 1
	t.Logf("level 6: %d bytes, level 1: %d bytes, %+.1f%%", len(slow), len(fast), grow*100)
	require.Less(t, grow, 0.35, "BestSpeed must not blow up the body")
	require.Less(t, len(fast), maxPublishBody, "a day still fits one part")
}

func BenchmarkGzipRecords(b *testing.B) {
	recs := benchRecords(13000)
	b.Run("level6", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := gzipRecordsAtLevel(recs, gzip.DefaultCompression); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("level1", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := gzipRecords(recs); err != nil {
				b.Fatal(err)
			}
		}
	})
}
