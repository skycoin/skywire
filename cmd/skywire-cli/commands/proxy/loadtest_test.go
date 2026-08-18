package skysocksc

import (
	"testing"
	"time"
)

// TestLoadtestSample pins the slice accounting: goodput = bytes*8/sliceSeconds,
// and a zero-byte slice is a gap (so a stalled read is unambiguous, never
// smeared into a neighboring slice).
func TestLoadtestSample(t *testing.T) {
	// 1 MB in a 0.5s slice → 16 Mbps, not a gap.
	r := loadtestSample(2*time.Second, 1_000_000, 0.5, false, "")
	if r.GoodputBps != 16_000_000 {
		t.Errorf("goodput = %v, want 16e6", r.GoodputBps)
	}
	if r.Gap {
		t.Error("non-zero slice must not be a gap")
	}
	if r.TMs != 2000 {
		t.Errorf("t_ms = %d, want 2000", r.TMs)
	}
	// Zero-byte slice → gap, goodput 0.
	g := loadtestSample(3*time.Second, 0, 0.5, true, "")
	if !g.Gap || g.GoodputBps != 0 {
		t.Errorf("zero slice: gap=%v goodput=%v, want gap=true goodput=0", g.Gap, g.GoodputBps)
	}
	// Degenerate slice duration never divides by zero.
	if z := loadtestSample(time.Second, 100, 0, true, ""); z.GoodputBps != 0 {
		t.Errorf("zero-duration slice goodput = %v, want 0", z.GoodputBps)
	}
}
