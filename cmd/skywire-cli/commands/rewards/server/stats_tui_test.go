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
	for _, want := range []string{"NETWORK", "BANDWIDTH", "LATENCY", "VISORS ONLINE", "TRANSPORTS BY TYPE", "VERSION ADOPTION"} {
		if !strings.Contains(out, want) {
			t.Errorf("panel %q missing", want)
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
