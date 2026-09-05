// Package uptimestats pkg/uptimestats/timeline_test.go c2-net-discovery
package uptimestats

import (
	"strings"
	"testing"
	"time"
)

// The five levels and their thresholds are what two existing renderings
// already draw. Hoisting them out of the CLI must not have moved a
// boundary: a visor's bar has to read identically at a terminal and on the
// reward page.
func TestShadeForCountThresholdsAreUnchanged(t *testing.T) {
	for _, tc := range []struct {
		count int
		want  string
	}{
		{0, " "}, {1, "░"}, {3, "░"}, {4, "▒"}, {6, "▒"},
		{7, "▓"}, {9, "▓"}, {10, "█"}, {12, "█"},
	} {
		if got := ShadeForCount(tc.count); got != tc.want {
			t.Errorf("ShadeForCount(%d) = %q, want %q", tc.count, got, tc.want)
		}
	}
}

// A day's bitmap always renders as exactly 24 blocks, whatever arrives —
// rows of differing widths would make the whole graph unreadable.
func TestBitmapToBlocksIsAlwaysADayWide(t *testing.T) {
	for _, in := range []string{
		"",
		strings.Repeat(".", TimelineSlots),
		strings.Repeat(" ", TimelineSlots),
		strings.Repeat(".", 100),
		strings.Repeat(".", TimelineSlots*2),
	} {
		if got := len([]rune(BitmapToBlocks(in))); got != TimelineHours {
			t.Errorf("bitmap of %d chars rendered %d blocks, want %d", len(in), got, TimelineHours)
		}
	}
	if got := BitmapToBlocks(strings.Repeat(".", TimelineSlots)); got != strings.Repeat("█", TimelineHours) {
		t.Errorf("a fully online day rendered %q", got)
	}
	if got := BitmapToBlocks(strings.Repeat(" ", TimelineSlots)); got != strings.Repeat(" ", TimelineHours) {
		t.Errorf("a fully offline day rendered %q", got)
	}
}

// The rolling window crosses UTC midnight, which is the only part of this
// that is not a per-day concatenation.
func TestRollingBarCrossesTheDayBoundary(t *testing.T) {
	now := time.Date(2026, 9, 5, 3, 0, 0, 0, time.UTC)
	// Online all of the 4th, offline all of the 5th so far.
	tl := map[string]string{
		"2026-09-04": strings.Repeat(".", TimelineSlots),
		"2026-09-05": strings.Repeat(" ", TimelineSlots),
	}
	bar := []rune(RollingBar(tl, now, 6))
	if len(bar) != 6 {
		t.Fatalf("bar is %d blocks, want 6", len(bar))
	}
	// The first three hours fall in the 4th (21:00–00:00), the last three
	// in the 5th (00:00–03:00).
	if string(bar[:3]) != "███" {
		t.Errorf("yesterday's hours rendered %q, want full", string(bar[:3]))
	}
	if string(bar[3:]) != "   " {
		t.Errorf("today's hours rendered %q, want empty", string(bar[3:]))
	}
	if n := CountOnlineSlots(tl, now.Add(-6*time.Hour), now); n != 3*TimelineSlotsPerHour {
		t.Errorf("counted %d online slots in the window, want %d", n, 3*TimelineSlotsPerHour)
	}
}
