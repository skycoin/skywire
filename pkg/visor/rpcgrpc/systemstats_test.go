// Package rpcgrpc — pkg/visor/rpcgrpc/systemstats_test.go: exercises the
// gopsutil-backed SystemStatsCollector against the real host. These are
// best-effort collectors — every sub-collector swallows its error in
// Collect — so the assertions stay loose (the host may not expose
// temperatures, per-partition disk usage, etc.). The value here is
// coverage of the collection paths plus the second-call rate-calculation
// branches (CPU/network deltas) that only fire once prior samples exist.
package rpcgrpc

import (
	"context"
	"testing"
)

func TestNewSystemStatsCollector(t *testing.T) {
	c := NewSystemStatsCollector()
	if c == nil {
		t.Fatal("NewSystemStatsCollector returned nil")
	}
	if c.lastNetStats != nil || c.lastCPUTimes != nil {
		t.Error("fresh collector should have nil prior samples")
	}
}

func TestSystemStatsCollect(t *testing.T) {
	c := NewSystemStatsCollector()
	ctx := context.Background()

	// First call: no prior samples, so CPU takes the gopsutil Percent
	// path and network skips rate calculation.
	stats, err := c.Collect(ctx, true, 5)
	if err != nil {
		t.Fatalf("first Collect: unexpected error %v", err)
	}
	if stats == nil {
		t.Fatal("first Collect returned nil stats")
	}
	if stats.TimestampNs == 0 {
		t.Error("TimestampNs should be set")
	}

	// Second call: prior CPU + network samples exist, so this exercises
	// the delta/rate branches in collectCPU and collectNetwork.
	stats2, err := c.Collect(ctx, true, 5)
	if err != nil {
		t.Fatalf("second Collect: unexpected error %v", err)
	}
	if stats2.TimestampNs < stats.TimestampNs {
		t.Error("second snapshot timestamp should not go backwards")
	}

	// After two calls the rate-tracking state must be populated.
	if c.lastCPUTimes == nil {
		t.Error("lastCPUTimes should be populated after Collect")
	}
}

func TestSystemStatsCollectWithoutProcesses(t *testing.T) {
	c := NewSystemStatsCollector()
	stats, err := c.Collect(context.Background(), false, 0)
	if err != nil {
		t.Fatalf("Collect without processes: %v", err)
	}
	if len(stats.Processes) != 0 {
		t.Errorf("expected no processes when includeProcesses=false, got %d", len(stats.Processes))
	}
}

func TestSystemStatsProcessLimit(t *testing.T) {
	c := NewSystemStatsCollector()
	// processLimit <= 0 inside Collect defaults to 10; pass an explicit
	// small positive limit to exercise the truncation branch.
	stats, err := c.Collect(context.Background(), true, 3)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(stats.Processes) > 3 {
		t.Errorf("process list = %d, want <= 3 (limit honored)", len(stats.Processes))
	}
}
