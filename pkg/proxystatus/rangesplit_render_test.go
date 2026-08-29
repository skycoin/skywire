// Package proxystatus pkg/proxystatus/rangesplit_render_test.go
package proxystatus

import (
	"strings"
	"testing"
)

// TestRenderRangeSplit_Active: an in-flight split renders an ACTIVE pill with the
// count and the cumulative shape, in the live region (so it also rides the ~1s
// WebSocket fragment push).
func TestRenderRangeSplit_Active(t *testing.T) {
	snap := Snapshot{
		Surface: SurfaceSkysocks,
		RangeSplit: &RangeSplit{
			Enabled: true, ActiveSplits: 2, TotalSplits: 5,
			TotalChunks: 100, TotalBytes: 20 << 20, StreamsPerSplit: 8, ChunkSize: 4 << 20,
		},
	}
	page := string(Render(snap))
	frag := string(RenderFragment(snap))
	for _, out := range []string{page, frag} {
		if !strings.Contains(out, "range-split") {
			t.Error("missing range-split label")
		}
		if !strings.Contains(out, "rs-active") || !strings.Contains(out, "active") {
			t.Error("active split must render the ACTIVE pill")
		}
		if !strings.Contains(out, "5 splits") {
			t.Error("must render cumulative split count")
		}
	}
}

// TestRenderRangeSplit_IdleAndAbsent: enabled-but-idle renders an idle pill;
// a nil RangeSplit omits the section entirely.
func TestRenderRangeSplit_IdleAndAbsent(t *testing.T) {
	idle := string(Render(Snapshot{Surface: SurfaceSkysocks, RangeSplit: &RangeSplit{Enabled: true, StreamsPerSplit: 8, ChunkSize: 4 << 20}}))
	if !strings.Contains(idle, "rs-idle") {
		t.Error("idle range-split must render the idle pill")
	}
	absent := string(Render(Snapshot{Surface: SurfaceSkysocks}))
	if strings.Contains(absent, "range-split") {
		t.Error("nil RangeSplit must omit the section")
	}
}
