package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The benchmarks below isolate the per-(entry × day) hot path of
// buildTransportMetrics: the daily-bandwidth key generation and the per-edge
// counter parsing. They model the shape of the real workload (~13k transports
// over a 30-day window) without a live Redis, so the allocation delta between
// the old (fmt-based, per-iteration) and new (precomputed + concat + strconv)
// code is directly measurable with `-benchmem`.

const (
	benchEntries = 13000
	benchDays    = 30
)

func benchIDs(n int) []uuid.UUID {
	ids := make([]uuid.UUID, n)
	for i := range ids {
		// Deterministic, distinct UUIDs (no rand — unavailable in this env).
		var u uuid.UUID
		u[0] = byte(i)
		u[1] = byte(i >> 8)
		u[15] = 0x42
		ids[i] = u
	}
	return ids
}

// old key path: uuid.String() + time.Format() + fmt.Sprintf, every iteration.
func BenchmarkBWKeygen_Old(b *testing.B) {
	ids := benchIDs(benchEntries)
	now := time.Now().UTC()
	b.ReportAllocs()
	b.ResetTimer()
	var sink int
	for n := 0; n < b.N; n++ {
		for i := range ids {
			for d := 0; d < benchDays; d++ {
				date := now.AddDate(0, 0, -d).Format("2006-01-02")
				key := fmt.Sprintf("%s:bw:daily:%s:%s", serviceName, ids[i].String(), date)
				sink += len(key)
			}
		}
	}
	_ = sink
}

// new key path: precompute idStr once per entry, dateStr once per window, concat.
func BenchmarkBWKeygen_New(b *testing.B) {
	ids := benchIDs(benchEntries)
	now := time.Now().UTC()
	b.ReportAllocs()
	b.ResetTimer()
	var sink int
	for n := 0; n < b.N; n++ {
		dateStrs := make([]string, benchDays)
		for d := 0; d < benchDays; d++ {
			dateStrs[d] = now.AddDate(0, 0, -d).Format("2006-01-02")
		}
		idStrs := make([]string, len(ids))
		for i := range ids {
			idStrs[i] = ids[i].String()
		}
		for i := range ids {
			idStr := idStrs[i]
			for d := 0; d < benchDays; d++ {
				key := serviceName + ":bw:daily:" + idStr + ":" + dateStrs[d]
				sink += len(key)
			}
		}
	}
	_ = sink
}

// old parse path: fmt.Sscanf per counter field (4 per day-with-data).
func BenchmarkBWParse_Old(b *testing.B) {
	vals := []string{"1048576", "2097152", "524288", "0"}
	b.ReportAllocs()
	b.ResetTimer()
	var sink uint64
	for n := 0; n < b.N; n++ {
		for i := 0; i < benchEntries; i++ {
			for _, v := range vals {
				var x uint64
				_, _ = fmt.Sscanf(v, "%d", &x)
				sink += x
			}
		}
	}
	_ = sink
}

// new parse path: strconv.ParseUint per counter field.
func BenchmarkBWParse_New(b *testing.B) {
	vals := []string{"1048576", "2097152", "524288", "0"}
	b.ReportAllocs()
	b.ResetTimer()
	var sink uint64
	for n := 0; n < b.N; n++ {
		for i := 0; i < benchEntries; i++ {
			for _, v := range vals {
				sink += parseBWUint(v)
			}
		}
	}
	_ = sink
}
