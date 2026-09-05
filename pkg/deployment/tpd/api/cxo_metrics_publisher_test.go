// Package api pkg/deployment/tpd/api/cxo_metrics_publisher_test.go c4-net-discovery
package api

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
)

// metricsFixture builds n records shaped like the production ones: a uuid-ish
// id, two 66-char edge keys and a day of per-edge bandwidth. Sizes here track
// the real thing closely enough that the split arithmetic is exercised.
func metricsFixture(n, days int) []store.TransportMetric {
	out := make([]store.TransportMetric, 0, n)
	for i := 0; i < n; i++ {
		daily := make([]store.DailyEdgeBandwidth, 0, days)
		for d := 0; d < days; d++ {
			daily = append(daily, store.DailyEdgeBandwidth{
				Date: fmt.Sprintf("2026-09-%02d", 1+d%28),
				A:    &store.EdgeBandwidth{Sent: uint64(i*7919 + d), Recv: uint64(i*104729 + d)},
				B:    &store.EdgeBandwidth{Sent: uint64(i*15485863 + d), Recv: uint64(i*32452843 + d)},
			})
		}
		out = append(out, store.TransportMetric{
			ID:      fmt.Sprintf("%08x-4660-0f05-aff8-8b006cc4c9%02x", i, i%256),
			Type:    []string{"stcpr", "sudph", "dmsg", "webrtc"}[i%4],
			Live:    i%3 != 0,
			Edges:   []string{fmt.Sprintf("02%064x", i), fmt.Sprintf("03%064x", i+1)},
			Latency: &store.TransportLatency{Min: int64(1000 + i), Max: int64(90000 + i), Avg: int64(14000 + i)},
			Daily:   daily,
		})
	}
	return out
}

// decodeParts is the reader half: gunzip each part and concatenate the records.
func decodeParts(t *testing.T, parts [][]byte) []store.TransportMetric {
	t.Helper()
	var all []store.TransportMetric
	for i, p := range parts {
		var chunk []store.TransportMetric
		if err := json.Unmarshal(cxoutils.Gunzip(p), &chunk); err != nil {
			t.Fatalf("part %d did not decode: %v", i, err)
		}
		all = append(all, chunk...)
	}
	return all
}

// A window that fits is published as one leaf, and it is gzipped — CXO stores
// bytes verbatim, so an uncompressed body is what pushed this feed over the
// object limit in the first place.
func TestGzipPartsSingleLeafIsCompressed(t *testing.T) {
	metrics := metricsFixture(200, 1)
	parts, err := gzipParts(metrics, maxPublishBody)
	if err != nil {
		t.Fatalf("gzipParts: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("got %d parts, want 1 for a small window", len(parts))
	}
	if len(parts[0]) < 2 || parts[0][0] != 0x1f || parts[0][1] != 0x8b {
		t.Error("the published body is not gzipped")
	}

	raw, err := json.Marshal(metrics)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts[0]) >= len(raw) {
		t.Errorf("gzipped body (%d) is not smaller than the raw one (%d)", len(parts[0]), len(raw))
	}
	if got := decodeParts(t, parts); len(got) != len(metrics) {
		t.Errorf("round-trip returned %d records, want %d", len(got), len(metrics))
	}
}

// The point of the split: every part fits, and NO record is dropped. The
// previous behavior re-fetched the window without per-edge bandwidth and
// published that instead, which silently lost data.
func TestGzipPartsSplitsWithoutLosingRecords(t *testing.T) {
	metrics := metricsFixture(4000, 30)

	// A cap far below the real one so the fixture reliably splits.
	const max = 64 * 1024

	parts, err := gzipParts(metrics, max)
	if err != nil {
		t.Fatalf("gzipParts: %v", err)
	}
	if len(parts) < 2 {
		t.Fatalf("got %d parts, want a split", len(parts))
	}
	for i, p := range parts {
		if len(p) > max {
			t.Errorf("part %d is %d bytes, over the %d cap", i, len(p), max)
		}
	}

	got := decodeParts(t, parts)
	if len(got) != len(metrics) {
		t.Fatalf("round-trip returned %d records, want %d", len(got), len(metrics))
	}
	// Order is preserved, and the per-day bandwidth survives intact.
	for i := range metrics {
		if got[i].ID != metrics[i].ID {
			t.Fatalf("record %d is %q, want %q — parts were reassembled out of order", i, got[i].ID, metrics[i].ID)
		}
		if len(got[i].Daily) != len(metrics[i].Daily) {
			t.Fatalf("record %d kept %d daily rows, want %d", i, len(got[i].Daily), len(metrics[i].Daily))
		}
	}
}

// A cap so small that no single record fits must not spin or drop records: the
// halving bottoms out at one record per part and lets the Put fail loudly.
func TestGzipPartsBottomsOutAtOneRecord(t *testing.T) {
	metrics := metricsFixture(8, 4)
	parts, err := gzipParts(metrics, 1)
	if err != nil {
		t.Fatalf("gzipParts: %v", err)
	}
	if len(parts) != len(metrics) {
		t.Fatalf("got %d parts, want one per record (%d)", len(parts), len(metrics))
	}
	if got := decodeParts(t, parts); len(got) != len(metrics) {
		t.Errorf("round-trip returned %d records, want %d", len(got), len(metrics))
	}
}

func TestSplitEvenlyCoversEveryRecordOnce(t *testing.T) {
	for _, tc := range []struct{ records, n int }{{100, 1}, {100, 3}, {100, 7}, {5, 10}, {1, 4}} {
		metrics := metricsFixture(tc.records, 1)
		var total int
		for _, part := range splitEvenly(metrics, tc.n) {
			if len(part) == 0 {
				t.Errorf("records=%d n=%d: produced an empty part", tc.records, tc.n)
			}
			total += len(part)
		}
		if total != tc.records {
			t.Errorf("records=%d n=%d: parts hold %d records, want %d", tc.records, tc.n, total, tc.records)
		}
	}
	if got := splitEvenly(nil, 4); got != nil {
		t.Errorf("splitEvenly(nil) = %v, want nil", got)
	}
}

// Part paths must sort lexically into publication order, because the reader
// recovers ordering from a sort of the paths a map-ordered Walk hands it.
func TestMetricsPartPathSortsNumerically(t *testing.T) {
	if a, b := metricsPartPath(30, 9), metricsPartPath(30, 10); !(a < b) {
		t.Errorf("%q should sort before %q", a, b)
	}
	if got, want := metricsPartPath(7, 3), "metrics/days/7/part/0003"; got != want {
		t.Errorf("metricsPartPath(7,3) = %q, want %q", got, want)
	}
	// The part paths must live under the window path so the subscriber's
	// "metrics/days/" prefix keeps covering them.
	if base := metricsPath(30); len(metricsPartPath(30, 0)) <= len(base) ||
		metricsPartPath(30, 0)[:len(base)] != base {
		t.Error("part paths do not sit under the window path")
	}
}

// The split is sized against the compressed total, because partTargetBody
// budgets the gzipped part. Sizing it from the raw size instead over-splits by
// the compression ratio, which costs a filling subscriber a round trip per
// surplus leaf.
func TestGzipPartsDoesNotOverSplit(t *testing.T) {
	metrics := metricsFixture(4000, 30)
	const max = 64 * 1024

	parts, err := gzipParts(metrics, max)
	if err != nil {
		t.Fatalf("gzipParts: %v", err)
	}
	if len(parts) < 2 {
		t.Fatalf("got %d parts, want a split", len(parts))
	}

	// The floor: how many parts the compressed payload actually requires.
	whole, err := json.Marshal(metrics)
	if err != nil {
		t.Fatal(err)
	}
	need := len(cxoutils.Gzip(whole))/max + 1

	// Some slack for per-part framing and uneven compression, but nowhere
	// near the ~4x the raw-size bug produced.
	if limit := need * 2; len(parts) > limit {
		t.Errorf("split into %d parts; a compressed payload needing ~%d should not exceed %d", len(parts), need, limit)
	}

	// Over-splitting must not be "fixed" by dropping records.
	if got := decodeParts(t, parts); len(got) != len(metrics) {
		t.Errorf("round-trip returned %d records, want %d", len(got), len(metrics))
	}
}
