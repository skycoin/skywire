// Package api pkg/deployment/tpd/api/metrics_parts_test.go c4-net-discovery
package api

import "testing"

// A day that split last tick is split from that count, and a day that shrank
// goes back to the count its compressed size needs.
func TestGzipPartsFromPreviousCount(t *testing.T) {
	const max = 64 * 1024
	day := metricsFixture(4000, 30)
	first, err := gzipParts(day, max)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) < 3 {
		t.Fatalf("got %d parts, want a split", len(first))
	}
	again, err := gzipPartsFrom(day, max, len(first))
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != len(first) {
		t.Errorf("same day split into %d parts, was %d", len(again), len(first))
	}
	grown := metricsFixture(5000, 30)
	more, err := gzipPartsFrom(grown, max, len(first))
	if err != nil {
		t.Fatal(err)
	}
	if got := decodeParts(t, more); len(got) != len(grown) {
		t.Fatalf("round-trip returned %d records, want %d", len(got), len(grown))
	}
	for i, p := range more {
		if len(p) > max {
			t.Errorf("part %d is %d bytes, over %d", i, len(p), max)
		}
	}
	shrunk := day[:len(day)/4]
	fresh, err := gzipParts(shrunk, max)
	if err != nil {
		t.Fatal(err)
	}
	less, err := gzipPartsFrom(shrunk, max, len(first))
	if err != nil {
		t.Fatal(err)
	}
	if len(less) != len(fresh) {
		t.Errorf("shrunk day kept %d parts, a fresh split needs %d", len(less), len(fresh))
	}
}

// BenchmarkGzipPartsSplitDay encodes a day that does not fit in one part, as
// the first tick of a day does and as every later tick does from the count
// the day had before.
func BenchmarkGzipPartsSplitDay(b *testing.B) {
	const max = 1 << 20
	day := metricsFixture(40000, 1)
	first, err := gzipParts(day, max)
	if err != nil || len(first) < 2 {
		b.Fatalf("want a split, got %d parts, %v", len(first), err)
	}
	for _, c := range []struct {
		name string
		prev int
	}{{"Whole", 0}, {"FromPrevious", len(first)}} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := gzipPartsFrom(day, max, c.prev); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
