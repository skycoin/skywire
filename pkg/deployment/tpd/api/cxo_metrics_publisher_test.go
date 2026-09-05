// Package api pkg/deployment/tpd/api/cxo_metrics_publisher_test.go c4-net-discovery
package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
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

// A day leaf that fits is published as one body, and it is gzipped — CXO stores
// bytes verbatim, so an uncompressed body is what pushed this feed over the
// object limit in the first place.
func TestGzipPartsSingleLeafIsCompressed(t *testing.T) {
	metrics := metricsFixture(200, 1)
	parts, err := gzipParts(metrics, maxPublishBody)
	if err != nil {
		t.Fatalf("gzipParts: %v", err)
	}
	if len(parts) != 1 {
		t.Fatalf("got %d parts, want 1 for a small day", len(parts))
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

// A day with no data must publish the empty ARRAY. json.Marshal of a nil slice
// is "null", which does not unmarshal into []TransportMetric the way the
// reader's merge expects.
func TestGzipPartsEmptyDayIsAnEmptyArray(t *testing.T) {
	for name, in := range map[string][]store.TransportMetric{
		"nil":   nil,
		"empty": {},
	} {
		parts, err := gzipParts(in, maxPublishBody)
		if err != nil {
			t.Fatalf("%s: gzipParts: %v", name, err)
		}
		if len(parts) != 1 {
			t.Fatalf("%s: got %d parts, want 1", name, len(parts))
		}
		if got := string(cxoutils.Gunzip(parts[0])); got != "[]" {
			t.Errorf("%s: published %q, want %q", name, got, "[]")
		}
	}
}

// The point of the split: every part fits, and NO record is dropped. The
// pre-#4509 behavior re-fetched the window without per-edge bandwidth and
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

// opPaths flattens a batch for assertions; deletes are marked so the ordering
// checks below can read.
func opPaths(ops []treestore.PutOp) []string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		if op.Value == nil {
			out = append(out, "-"+op.Path)
			continue
		}
		out = append(out, "+"+op.Path)
	}
	return out
}

func body(n int) [][]byte {
	out := make([][]byte, n)
	for i := range out {
		out[i] = []byte{byte(i)}
	}
	return out
}

// The steady state: one put per day in the window, nothing else.
func TestPlanDayOpsSteadyState(t *testing.T) {
	dates := []string{"2026-09-04", "2026-09-03", "2026-09-02"}
	bodies := map[string][][]byte{dates[0]: body(1), dates[1]: body(1), dates[2]: body(1)}

	ops, next := planDayOps(bodies, dates, map[string]int{}, true)
	want := []string{
		"+metrics/day/2026-09-04",
		"+metrics/day/2026-09-03",
		"+metrics/day/2026-09-02",
	}
	if got := opPaths(ops); !equalStrings(got, want) {
		t.Errorf("ops = %v, want %v", got, want)
	}
	for _, d := range dates {
		if next[d] != 0 {
			t.Errorf("%s recorded %d parts, want 0 (single leaf)", d, next[d])
		}
	}
}

// The split is sized against the compressed total, because the per-part
// target and the cap both budget the gzipped part. Sizing it from the raw size
// instead over-splits by the compression ratio, which costs a filling
// subscriber a round trip per surplus leaf.
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

// A day that only refreshes "today" must leave every other day's leaf alone —
// that is the whole point of the layout, and the bookkeeping for those days has
// to survive the cycle so the next full one can still retire them.
func TestPlanDayOpsCurrentDayOnlyTouchesToday(t *testing.T) {
	prev := map[string]int{"2026-09-04": 0, "2026-09-03": 0, "2026-08-20": 3}
	ops, next := planDayOps(map[string][][]byte{"2026-09-04": body(1)}, []string{"2026-09-04"}, prev, false)

	if got, want := opPaths(ops), []string{"+metrics/day/2026-09-04"}; !equalStrings(got, want) {
		t.Errorf("ops = %v, want %v", got, want)
	}
	if len(next) != len(prev) {
		t.Errorf("next tracks %d days, want %d — a non-full cycle must not forget days", len(next), len(prev))
	}
	if next["2026-08-20"] != 3 {
		t.Errorf("part count for an untouched day became %d, want 3", next["2026-08-20"])
	}
}

// A rolling window has to retire the days that fall out of it, leaf AND parts,
// or the tree grows by a day forever and a prefix Walk keeps serving them.
func TestPlanDayOpsRetiresDaysOutOfWindow(t *testing.T) {
	prev := map[string]int{"2026-09-04": 0, "2026-09-03": 0, "2026-08-05": 0, "2026-08-04": 2}
	dates := []string{"2026-09-04", "2026-09-03"}
	bodies := map[string][][]byte{dates[0]: body(1), dates[1]: body(1)}

	ops, next := planDayOps(bodies, dates, prev, true)
	want := []string{
		"-metrics/day/2026-08-04/part/0000",
		"-metrics/day/2026-08-04/part/0001",
		"-metrics/day/2026-08-05",
		"+metrics/day/2026-09-04",
		"+metrics/day/2026-09-03",
	}
	if got := opPaths(ops); !equalStrings(got, want) {
		t.Errorf("ops = %v, want %v", got, want)
	}
	if _, still := next["2026-08-05"]; still {
		t.Error("a retired day is still tracked")
	}
	if _, still := next["2026-08-04"]; still {
		t.Error("a retired split day is still tracked")
	}
}

// A path cannot be a leaf and a sub-tree at once, and PutBatch applies ops in
// order — so a day changing form has to retire the old form FIRST.
func TestPlanDayOpsDeletesBeforePutsWhenFormChanges(t *testing.T) {
	// Single leaf -> split.
	ops, next := planDayOps(map[string][][]byte{"2026-09-04": body(2)}, []string{"2026-09-04"}, map[string]int{"2026-09-04": 0}, false)
	want := []string{
		"-metrics/day/2026-09-04",
		"+metrics/day/2026-09-04/part/0000",
		"+metrics/day/2026-09-04/part/0001",
	}
	if got := opPaths(ops); !equalStrings(got, want) {
		t.Errorf("leaf->split ops = %v, want %v", got, want)
	}
	if next["2026-09-04"] != 2 {
		t.Errorf("recorded %d parts, want 2", next["2026-09-04"])
	}

	// Split -> single leaf: every old part must go before the leaf lands.
	ops, next = planDayOps(map[string][][]byte{"2026-09-04": body(1)}, []string{"2026-09-04"}, map[string]int{"2026-09-04": 3}, false)
	want = []string{
		"-metrics/day/2026-09-04/part/0000",
		"-metrics/day/2026-09-04/part/0001",
		"-metrics/day/2026-09-04/part/0002",
		"+metrics/day/2026-09-04",
	}
	if got := opPaths(ops); !equalStrings(got, want) {
		t.Errorf("split->leaf ops = %v, want %v", got, want)
	}
	if next["2026-09-04"] != 0 {
		t.Errorf("recorded %d parts, want 0", next["2026-09-04"])
	}

	// Shrinking split: the leftover high-index parts must be retired.
	ops, _ = planDayOps(map[string][][]byte{"2026-09-04": body(2)}, []string{"2026-09-04"}, map[string]int{"2026-09-04": 4}, false)
	want = []string{
		"-metrics/day/2026-09-04",
		"-metrics/day/2026-09-04/part/0002",
		"-metrics/day/2026-09-04/part/0003",
		"+metrics/day/2026-09-04/part/0000",
		"+metrics/day/2026-09-04/part/0001",
	}
	if got := opPaths(ops); !equalStrings(got, want) {
		t.Errorf("shrinking-split ops = %v, want %v", got, want)
	}
}

// equalStrings compares op sequences; joining is enough here and keeps the
// comparison free of index arithmetic.
func equalStrings(a, b []string) bool {
	return strings.Join(a, "\n") == strings.Join(b, "\n")
}
