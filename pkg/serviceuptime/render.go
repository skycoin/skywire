// Package serviceuptime — pkg/serviceuptime/render.go: shared text
// renderer for the CLI. Both `skywire cli visor uptime` and
// `skywire cli svc <name> uptime` go through here so the output is
// uniform across a deployment.
package serviceuptime

import (
	"fmt"
	"strings"
	"time"
)

// FormatHistory renders sessions as a chronological table. Gaps
// between adjacent sessions where the next StartedAt is more than
// gapThreshold after the previous LastSeen surface as "(down: <dur>)"
// rows so an operator can see when the service wasn't running.
//
// Output shape:
//
//	2026-04-28 12:00:11Z  →  2026-04-28 12:00:14Z   3.0s   v1.3.46  (restart loop?)
//	2026-04-28 12:00:18Z  →  2026-04-28 12:00:21Z   3.0s   v1.3.46  (restart loop?)
//	2026-04-28 12:00:25Z  →  2026-04-29 04:31:07Z  16.5h   v1.3.46
//	                                  (down: 4m12s)
//	2026-04-29 04:35:19Z  →  2026-05-04 21:00:00Z   5.7d   v1.3.47
//
// Sessions whose Duration is below restartLoopThreshold are tagged.
func FormatHistory(sessions []SessionRecord, gapThreshold, restartLoopThreshold time.Duration) string {
	if len(sessions) == 0 {
		return "(no sessions recorded)\n"
	}
	var sb strings.Builder
	for i, s := range sessions {
		if i > 0 {
			gap := s.StartedAt.Sub(sessions[i-1].LastSeen)
			if gap > gapThreshold {
				fmt.Fprintf(&sb, "%-72s(down: %s)\n", "", humanDuration(gap))
			}
		}
		dur := s.Duration()
		ver := s.Version
		if ver == "" {
			ver = "?"
		}
		tag := ""
		if dur > 0 && dur < restartLoopThreshold {
			tag = "  (restart loop?)"
		}
		fmt.Fprintf(&sb, "%s  →  %s   %-7s  %s%s\n",
			s.StartedAt.UTC().Format("2006-01-02 15:04:05Z"),
			s.LastSeen.UTC().Format("2006-01-02 15:04:05Z"),
			humanDuration(dur),
			ver,
			tag,
		)
	}
	return sb.String()
}

// FormatBitmap renders a 36-byte bitmap as a 288-character ASCII line
// — '#' for set bits, '.' for unset — matching TPD's v3 timeline
// shape. For dashboards that want a one-liner per day.
func FormatBitmap(bm []byte) string {
	if len(bm) < BitmapSize {
		return strings.Repeat(".", SlotsPerDay)
	}
	out := make([]byte, SlotsPerDay)
	for i := 0; i < SlotsPerDay; i++ {
		if GetSlot(bm, i) {
			out[i] = '#'
		} else {
			out[i] = '.'
		}
	}
	return string(out)
}

// humanDuration is a compact short form ("3.0s", "5m12s", "16.5h",
// "5.7d") suitable for table cells.
func humanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Hour:
		m := int(d / time.Minute)
		s := int((d % time.Minute) / time.Second)
		return fmt.Sprintf("%dm%02ds", m, s)
	case d < 24*time.Hour:
		return fmt.Sprintf("%.1fh", d.Hours())
	default:
		return fmt.Sprintf("%.1fd", d.Hours()/24)
	}
}
