// Package uptimestats pkg/uptimestats/timeline.go c2-net-discovery
//
// The shaded-block timeline glyphs, hoisted out of the CLI.
//
// `skywire cli ut <svc> graph` renders the v3 per-5-minute bitmaps as
// hourly blocks at five density levels. That rendering lived entirely
// inside cmd/skywire-cli/cliuptime as unexported helpers, so the second
// consumer (cmd/skywire-cli/commands/visor/info.go) copy-pasted
// shadeForCount verbatim with a comment saying pkg-level helpers were not
// exported. This file is that comment's fix: the glyph mapping and the
// bar builders are pure functions of the timeline map, so they belong
// beside the types they read rather than beside one of the printers.
//
// The package doc for cliuptime already anticipated this — "keep this
// thin ... so the hypervisor UI / RPC layer can eventually render the
// same data without going through a CLI subprocess". A third consumer
// (the reward server's statistics panel) is what forced it.
package uptimestats

import (
	"strings"
	"time"
)

const (
	// TimelineSlotsPerHour is the v3 bitmap's resolution: one character
	// per five minutes.
	TimelineSlotsPerHour = 12
	// TimelineHours is how many hours one day's bitmap covers.
	TimelineHours = 24
	// TimelineSlots is the full length of a day's bitmap.
	TimelineSlots = TimelineHours * TimelineSlotsPerHour
	// TimelineOnline is the character the tracker writes for a slot the
	// visor was seen in. Anything else means not seen.
	TimelineOnline = '.'
)

// ShadeForCount maps a count of online five-minute slots within one hour
// to a block glyph. Five levels, so an hour that was half up is visibly
// different from one that was fully up or fully down — a two-state
// on/off glyph would render an intermittent visor identically to a
// solid one.
func ShadeForCount(count int) string {
	switch {
	case count == 0:
		return " "
	case count <= 3:
		return "░"
	case count <= 6:
		return "▒"
	case count <= 9:
		return "▓"
	default:
		return "█"
	}
}

// BitmapToBlocks turns one day's bitmap into TimelineHours hourly blocks.
// A bitmap longer than a full day is truncated and a shorter one is
// scaled, so a publisher that changes resolution still renders 24 blocks
// rather than a bar of a different width than every other row.
func BitmapToBlocks(bitmap string) string {
	counts := make([]int, TimelineHours)
	if len(bitmap) == 0 {
		return strings.Repeat(" ", TimelineHours)
	}
	step := len(bitmap)
	if step > TimelineSlots {
		step = TimelineSlots
	}
	for i := 0; i < step; i++ {
		if bitmap[i] == TimelineOnline {
			hr := i * TimelineHours / step
			if hr >= TimelineHours {
				hr = TimelineHours - 1
			}
			counts[hr]++
		}
	}
	var b strings.Builder
	b.Grow(TimelineHours)
	for _, c := range counts {
		b.WriteString(ShadeForCount(c))
	}
	return b.String()
}

// CountOnlineSlots returns how many five-minute slots in [from, to) are
// marked online across the per-day bitmaps.
func CountOnlineSlots(timelines map[string]string, from, to time.Time) int {
	n := 0
	for t := from; t.Before(to); t = t.Add(5 * time.Minute) {
		day := t.UTC().Format("2006-01-02")
		bmp, ok := timelines[day]
		if !ok {
			continue
		}
		slot := t.Hour()*TimelineSlotsPerHour + t.Minute()/5
		if slot >= 0 && slot < len(bmp) && bmp[slot] == TimelineOnline {
			n++
		}
	}
	return n
}

// RollingBar renders the hoursBack hours ending at now as one block per
// hour, oldest on the left. Crossing a day boundary is handled by
// CountOnlineSlots, which is why this is not just a concatenation of
// BitmapToBlocks over whole days.
func RollingBar(timelines map[string]string, now time.Time, hoursBack int) string {
	if hoursBack <= 0 {
		return ""
	}
	var b strings.Builder
	b.Grow(hoursBack)
	for i := hoursBack - 1; i >= 0; i-- {
		hourStart := now.Add(-time.Duration(i+1) * time.Hour)
		hourEnd := now.Add(-time.Duration(i) * time.Hour)
		b.WriteString(ShadeForCount(CountOnlineSlots(timelines, hourStart, hourEnd)))
	}
	return b.String()
}
