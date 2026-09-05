// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/stats_tui_test.go c5-reward-server
package clirewardsserver

import (
	"os"
	"strings"
	"testing"
)

func sampleTUIData() statsTUIData {
	return statsTUIData{
		Transports:   10807,
		UniqueVisors: 936,
		ByType:       map[string]int{"sudph": 3713, "webrtc": 3459, "stcpr": 2918, "squicr": 705},
		Daily: []statsTUIDay{
			{Date: "2026-08-30", Bandwidth: 12 << 30, Latency: 300, ByType: map[string]uint64{"sudph": 4 << 30}},
			{Date: "2026-08-31", Bandwidth: 18 << 30, Latency: 340, ByType: map[string]uint64{"sudph": 6 << 30}},
			{Date: "2026-09-01", Bandwidth: 22 << 30, Latency: 360, ByType: map[string]uint64{"sudph": 7 << 30}},
		},
		Versions: map[string]int{"v1.3.93": 610, "v1.3.91": 197, "v1.3.94-0": 17},
		Liveness: []statsTUILivenessDay{
			{Date: "2026-08-30", Slots: []int{700, 710, 690}},
			{Date: "2026-08-31", Slots: []int{720, 730, 740}},
		},
	}
}

// The whole design rests on one renderer feeding two sinks, so the ANSI has to
// actually carry SGR sequences — without them ansifilter produces plain text
// and the HTML is colorless.
func TestRenderStatsANSIEmitsColor(t *testing.T) {
	out := RenderStatsANSI(sampleTUIData())
	if !strings.Contains(out, "\x1b[") {
		t.Fatal("no ANSI escape sequences in the rendering")
	}
	for _, want := range []string{
		"NETWORK", "BANDWIDTH", "LATENCY", "VISORS ONLINE", "TRANSPORTS BY TYPE",
		"VERSION ADOPTION", "TRANSPORTS PER VISOR", "ARCHITECTURE", "UPTIME TIMELINE",
		"ROUTE SETUP NODES",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("panel %q missing", want)
		}
	}
}

// The three panels added alongside the originals obey the same rule as the
// originals: with nothing gathered they name themselves absent rather than
// vanishing. sampleTUIData leaves all three zero, which is exactly the
// "nothing even attempted to fetch it" case.
func TestRenderStatsANSINamesTheNewPanelsWhenUnpopulated(t *testing.T) {
	out := RenderStatsANSI(sampleTUIData())
	for _, title := range []string{"TRANSPORTS PER VISOR", "ARCHITECTURE", "UPTIME TIMELINE", "ROUTE SETUP NODES"} {
		i := strings.Index(out, title)
		if i < 0 {
			t.Fatalf("panel %q vanished instead of reporting itself absent", title)
		}
		// The rule that follows the title is 78 box-drawing runes at three
		// bytes each, so the window has to be generous in bytes.
		end := i + 600
		if end > len(out) {
			end = len(out)
		}
		if !strings.Contains(out[i:end], "unavailable") {
			t.Errorf("panel %q rendered without data and without saying so", title)
		}
	}
}

// A failed source must be named, and must never render as a zero — a zero
// reads as a measurement. This is the property the previous page lacked: one
// dead fetch took the entire summary down.
func TestRenderStatsANSINamesMissingSections(t *testing.T) {
	d := sampleTUIData()
	d.CountsErr = "request failed: EOF"
	d.DailyErr = "status 502"
	out := RenderStatsANSI(d)

	if !strings.Contains(out, "unavailable") {
		t.Error("a failed section did not say it was unavailable")
	}
	if !strings.Contains(out, "EOF") || !strings.Contains(out, "502") {
		t.Error("the reason a section failed was not surfaced")
	}
	// Liveness and versions still succeeded, so they must still be drawn.
	if !strings.Contains(out, "VISORS ONLINE") || !strings.Contains(out, "VERSION ADOPTION") {
		t.Error("a failure in one section suppressed the sections that worked")
	}
}

// Everything failing must still produce a page rather than an empty body.
func TestRenderStatsANSISurvivesTotalFailure(t *testing.T) {
	out := RenderStatsANSI(statsTUIData{
		CountsErr: "x", DailyErr: "y", VersionsErr: "z", LivenessErr: "w",
	})
	if len(out) == 0 {
		t.Fatal("rendered nothing")
	}
	if strings.Count(out, "unavailable") < 3 {
		t.Errorf("expected each dead section to be named, got:\n%s", out)
	}
}

// Fragment mode emits spans without a <pre>, so the wrapper is not optional:
// without it every newline collapses and the layout is destroyed.
func TestRenderStatsHTMLFragmentWrapsInPre(t *testing.T) {
	h := RenderStatsHTMLFragment(sampleTUIData())
	if !strings.HasPrefix(h, "<pre") || !strings.HasSuffix(h, "</pre>") {
		t.Error("fragment is not wrapped in <pre>")
	}
	if !strings.Contains(h, "<span") {
		t.Error("no colored spans — ansifilter did not see the SGR sequences")
	}
	if strings.Contains(h, "\x1b[") {
		t.Error("raw escape sequences leaked into the HTML")
	}
}

// The x axis is the reason the multi-day charts are readable at all.
func TestTUIXAxisPlacesLabels(t *testing.T) {
	out := tuiXAxis([]string{"08-30", "08-31", "09-01"}, 60, 8)
	if !strings.Contains(out, "08-30") || !strings.Contains(out, "09-01") {
		t.Error("axis labels missing")
	}
	if !strings.Contains(out, "┬") {
		t.Error("axis ticks missing")
	}
	if got := tuiXAxis(nil, 60, 8); got != "" {
		t.Errorf("expected empty axis for no labels, got %q", got)
	}
}

// A token wider than the panel has no space to break at. Wrapping must split
// it rather than let it run past the rule — and must not drop the overflow,
// because the token that does this in practice is a dmsg URL carrying a whole
// public key.
func TestTUIWrapBreaksATokenWiderThanThePanel(t *testing.T) {
	pk := "0324579f003e6b4048bae2def4365e634d8e0e3054a20fc7af49daf2a179658557"
	out := tuiWrap(`      Get "dmsg://` + pk + `:80/stats": EOF`)
	var joined string
	for _, line := range strings.Split(out, "\n") {
		plain := stripANSI(line)
		if len([]rune(plain)) > tuiWidth {
			t.Errorf("line runs to %d columns: %q", len([]rune(plain)), plain)
		}
		joined += strings.TrimSpace(plain)
	}
	if !strings.Contains(joined, pk) {
		t.Error("the public key did not survive the wrap intact")
	}
	if !strings.Contains(joined, "EOF") {
		t.Error("text after the over-long token was dropped")
	}
}

// Optional live check against the real deployment. Off by default so the suite
// stays hermetic; run with STATS_TUI_LIVE=1 to exercise the real fetches.
func TestGatherStatsTUILive(t *testing.T) {
	if os.Getenv("STATS_TUI_LIVE") == "" {
		t.Skip("set STATS_TUI_LIVE=1 to fetch from the live deployment")
	}
	d := gatherStatsTUI()
	t.Logf("transports=%d visors=%d daily=%d versions=%d",
		d.Transports, d.UniqueVisors, len(d.Daily), len(d.Versions))
	t.Logf("errs: counts=%q daily=%q versions=%q", d.CountsErr, d.DailyErr, d.VersionsErr)
	if h := RenderStatsHTMLFragment(d); len(h) == 0 {
		t.Fatal("rendered nothing from live data")
	}
}

// A section with neither data nor an error must still be named. This shipped
// broken once: the Liveness field existed and the renderer drew it, but
// gatherStatsTUI never populated it, so the panel vanished silently — the one
// failure mode the whole design exists to prevent.
func TestRenderStatsANSINeverSilentlyOmitsAPanel(t *testing.T) {
	d := sampleTUIData()
	d.Liveness = nil // no data, and crucially no error either
	out := RenderStatsANSI(d)
	if !strings.Contains(out, "VISORS ONLINE") {
		t.Fatal("the panel disappeared entirely instead of reporting itself absent")
	}
	if !strings.Contains(out, "unavailable") {
		t.Error("an unpopulated panel was drawn without saying it had no data")
	}
}

// The flat slot series regroups into per-day runs for the x-axis labels.
func TestLivenessToTUIDaysGroupsByDate(t *testing.T) {
	got := livenessToTUIDays(&livenessSeries{
		Counts: []int{1, 2, 3, 4, 5},
		Dates:  []string{"2026-09-04", "2026-09-04", "2026-09-05", "2026-09-05", "2026-09-05"},
	})
	if len(got) != 2 {
		t.Fatalf("got %d days, want 2", len(got))
	}
	if got[0].Date != "2026-09-04" || len(got[0].Slots) != 2 {
		t.Errorf("day 0 = %+v", got[0])
	}
	if got[1].Date != "2026-09-05" || len(got[1].Slots) != 3 {
		t.Errorf("day 1 = %+v", got[1])
	}
	if livenessToTUIDays(nil) != nil {
		t.Error("nil series should produce no days")
	}
}
