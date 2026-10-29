// Package api pkg/deployment/tpd/api/metrics_leaf_test.go c4-net-discovery
package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// BenchmarkEmptyLeaf checks a production-sized saved day, ~15 MB of JSON.
// The Whole case is the old check, which decoded the day in one piece.
func BenchmarkEmptyLeaf(b *testing.B) {
	gz, err := gzipRecords(benchRecords(60000))
	require.NoError(b, err)
	parts := [][]byte{gz}
	b.Run("Whole", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			recs, err := decodeMetricsParts(parts)
			if err != nil || len(recs) == 0 {
				b.Fatal("empty")
			}
		}
	})
	b.Run("Stream", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if emptyLeaf(parts) {
				b.Fatal("empty")
			}
		}
	})
}

func TestEmptyLeafRejectsBadLeaves(t *testing.T) {
	gz, err := gzipRecords(benchRecords(3))
	require.NoError(t, err)
	empty, err := gzipRecords(nil)
	require.NoError(t, err)
	corrupt := append([]byte(nil), gz...)
	corrupt[len(corrupt)-5] ^= 0xff // inside the CRC trailer

	require.False(t, emptyLeaf([][]byte{gz}))
	require.False(t, emptyLeaf([][]byte{empty, gz}))
	require.False(t, emptyLeaf([][]byte{[]byte(`[{"id":"a","daily":[]}]`)}), "a raw body is accepted")
	require.True(t, emptyLeaf(nil))
	require.True(t, emptyLeaf([][]byte{empty}))
	require.True(t, emptyLeaf([][]byte{corrupt}))
	require.True(t, emptyLeaf([][]byte{gz[:len(gz)/2]}))
	require.True(t, emptyLeaf([][]byte{[]byte(`[{"id":"a"}] x`)}))
	require.True(t, emptyLeaf([][]byte{[]byte(`{"id":"a"}`)}))
}
