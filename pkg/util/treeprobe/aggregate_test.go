package treeprobe

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
)

// Drive the aggregator through the synthetic sampleStream from
// parser_test.go, then validate the per-cell + run-level + CSV
// shape.
func TestAggregator_FoldSampleStream(t *testing.T) {
	p := NewParser(strings.NewReader(sampleStream))
	a := NewAggregator()
	for {
		d, err := p.Next()
		if err != nil {
			break
		}
		a.Observe(d)
	}

	cells := a.Cells()
	if len(cells) != 2 {
		t.Fatalf("want 2 cells (one per peer in sample stream); got %d", len(cells))
	}

	// First cell: 02deadbeef (discovered + ping_result, live_ping)
	c0 := cells[0]
	if c0.Key.RemotePK != "02cafefeed" && c0.Key.RemotePK != "02deadbeef" {
		t.Errorf("cell 0 remote_pk unexpected: %q", c0.Key.RemotePK)
	}
	if c0.Result == nil {
		t.Errorf("cell 0 result missing")
	} else if c0.Result.LatencySource == "" {
		t.Errorf("cell 0 latency_source empty")
	}

	// Run-level + cache hit rate
	run := a.RunDone()
	if run == nil {
		t.Fatal("RunDone missing")
	}
	if run.TotalDiscovered != 20 || run.TotalPinged != 15 || run.TotalSkippedCached != 5 {
		t.Errorf("run totals wrong: %+v", run)
	}
	expectedRate := 5.0 / (15.0 + 5.0)
	if a.CacheHitRate() != expectedRate {
		t.Errorf("cache hit rate: want %f got %f", expectedRate, a.CacheHitRate())
	}

	// Status update count
	if a.StatusUpdateCount() != 1 {
		t.Errorf("status_update count: want 1 got %d", a.StatusUpdateCount())
	}
}

// CSV emit round-trips: write CSV → parse CSV → row count matches
// cell count, header row present, run-level fields populated.
func TestWriteCSV_RoundTrip(t *testing.T) {
	p := NewParser(strings.NewReader(sampleStream))
	a := NewAggregator()
	for {
		d, err := p.Next()
		if err != nil {
			break
		}
		a.Observe(d)
	}

	var buf bytes.Buffer
	n, err := WriteCSV(&buf, a)
	if err != nil {
		t.Fatalf("WriteCSV: %v", err)
	}
	if n != 2 {
		t.Errorf("WriteCSV rows: want 2 got %d", n)
	}

	cr := csv.NewReader(&buf)
	header, err := cr.Read()
	if err != nil {
		t.Fatalf("read header: %v", err)
	}
	if len(header) != len(CSVHeaders) {
		t.Errorf("header len: want %d got %d", len(CSVHeaders), len(header))
	}

	idx := map[string]int{}
	for i, h := range header {
		idx[h] = i
	}
	row, err := cr.Read()
	if err != nil {
		t.Fatalf("read row: %v", err)
	}
	if row[idx["run_termination_reason"]] != "hops_target" {
		t.Errorf("run_termination_reason: want hops_target got %q", row[idx["run_termination_reason"]])
	}
	if row[idx["latency_source"]] == "" {
		t.Errorf("latency_source empty on row 0")
	}
	if row[idx["ping_avg_ms"]] == "" {
		t.Errorf("ping_avg_ms empty on row 0")
	}
}

// Empty stream emits header-only CSV (0 data rows).
func TestWriteCSV_EmptyStream(t *testing.T) {
	a := NewAggregator()
	var buf bytes.Buffer
	n, err := WriteCSV(&buf, a)
	if err != nil {
		t.Fatalf("WriteCSV empty: %v", err)
	}
	if n != 0 {
		t.Errorf("empty stream rows: want 0 got %d", n)
	}
	// Header must still be present
	if !strings.HasPrefix(buf.String(), "level,parent_pk,remote_pk,") {
		t.Errorf("header missing: %q", buf.String())
	}
}

// nsToMs handles zero + non-zero correctly + uses 3 decimal places.
func TestNsToMs(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, ""},
		{1000000, "1.000"},
		{1500000, "1.500"},
		{287931103, "287.931"},
	}
	for _, c := range cases {
		got := nsToMs(c.in)
		if got != c.want {
			t.Errorf("nsToMs(%d): want %q got %q", c.in, c.want, got)
		}
	}
}
