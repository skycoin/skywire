// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/stats_tui_uptime_test.go c5-reward-server
package clirewardsserver

import (
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/uptimestats"
)

// sampleUptimeGraph builds a panel with one solid visor, one intermittent
// and one dark, so every density level is exercised.
func sampleUptimeGraph() uptimeGraphStats {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	solid := map[string]string{}
	patchy := map[string]string{}
	for d := 0; d < 4; d++ {
		day := now.Add(-time.Duration(d) * 24 * time.Hour).Format("2006-01-02")
		solid[day] = strings.Repeat(".", uptimestats.TimelineSlots)
		var b strings.Builder
		for i := 0; i < uptimestats.TimelineSlots; i++ {
			if i%4 == 0 {
				b.WriteByte('.')
			} else {
				b.WriteByte(' ')
			}
		}
		patchy[day] = b.String()
	}
	dark := map[string]string{now.Format("2006-01-02"): strings.Repeat(" ", uptimestats.TimelineSlots)}

	rows := []uptimeGraphRow{}
	for _, in := range []struct {
		pk string
		tl map[string]string
	}{
		{"02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd", solid},
		{"03b1e5f457ebce276fe666efecb5b8f5e29897ff8f166f673498de6213cee8f9ee", patchy},
		{"024c9c4b2a6a1f3d5e7c8b9a0d1e2f3a4b5c6d7e8f90a1b2c3d4e5f60718293a4b", dark},
	} {
		rows = append(rows, uptimeGraphRow{
			PK:          in.pk,
			Version:     "v1.3.94-0",
			Online:      true,
			Bar:         uptimestats.RollingBar(in.tl, now, uptimeGraphHours),
			OnlineSlots: uptimestats.CountOnlineSlots(in.tl, now.Add(-uptimeGraphHours*time.Hour), now),
			TotalSlots:  uptimeGraphHours * uptimestats.TimelineSlotsPerHour,
		})
	}
	return uptimeGraphStats{
		Rows: rows, Hours: uptimeGraphHours, EndedAt: now,
		Src: "source: CXO uptimes/days/30",
	}
}

// An operator matching a row against their own visor needs the whole key.
// This is why the row is split over two lines at all.
func TestUptimeGraphPanelPrintsFullPublicKeys(t *testing.T) {
	s := sampleUptimeGraph()
	out := renderUptimeGraphPanelANSI(s)
	for _, r := range s.Rows {
		if !strings.Contains(out, r.PK) {
			t.Errorf("public key %s is not present in full", r.PK)
		}
	}
}

// The bars must be the CLI's bars. If this panel ever grew its own glyph
// mapping, the same visor would read differently here and at a terminal.
func TestUptimeGraphBarsAreTheCLIGlyphs(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	tl := map[string]string{now.Format("2006-01-02"): strings.Repeat(".", uptimestats.TimelineSlots)}
	want := uptimestats.RollingBar(tl, now, uptimeGraphHours)
	if len([]rune(want)) != uptimeGraphHours {
		t.Fatalf("bar is %d blocks, want %d", len([]rune(want)), uptimeGraphHours)
	}
	out := renderUptimeGraphPanelANSI(uptimeGraphStats{
		Rows:  []uptimeGraphRow{{PK: strings.Repeat("a", 66), Bar: want, OnlineSlots: 1, TotalSlots: 1}},
		Hours: uptimeGraphHours, EndedAt: now,
	})
	if !strings.Contains(out, want) {
		t.Error("the rendered bar is not the shared-glyph bar")
	}
}

// A fully dark visor and a fully up one must not render alike, and the
// panel must carry the density legend that makes the middle states
// readable at all.
func TestUptimeGraphPanelSeparatesUpFromDark(t *testing.T) {
	out := renderUptimeGraphPanelANSI(sampleUptimeGraph())
	if !strings.Contains(out, "█") {
		t.Error("no full-density blocks; a visor that was up all window did not render as up")
	}
	if !strings.Contains(out, "density:") {
		t.Error("the block-density legend is missing, so the shades are unreadable")
	}
	if !strings.Contains(out, "  0%") {
		t.Error("the dark visor's window uptime was not reported")
	}
}

// The bar is 72 blocks and the public key is 66 characters; neither may be
// shortened, so the row is split and every line must still fit the box.
func TestUptimeGraphPanelFitsThePanelWidth(t *testing.T) {
	for _, line := range strings.Split(renderUptimeGraphPanelANSI(sampleUptimeGraph()), "\n") {
		if got := len([]rune(stripANSI(line))); got > tuiWidth+2 {
			t.Errorf("line runs to %d columns: %q", got, stripANSI(line))
		}
	}
}

// The panel says which visors were up, and must not be mistaken for the
// uptime figure the reward calculation uses (#4533).
func TestUptimeGraphPanelDisclaimsBeingUptime(t *testing.T) {
	out := renderUptimeGraphPanelANSI(sampleUptimeGraph())
	if !strings.Contains(out, "#4533") {
		t.Error("the liveness-not-uptime caveat is missing")
	}
}

// The ruler is what turns a bad block into a time. Without it the bar
// shows that something happened and never when.
func TestUptimeGraphRulerMarksTheWindow(t *testing.T) {
	r := tuiHourRuler(uptimeGraphHours)
	if !strings.Contains(r, "now") {
		t.Error("the ruler does not mark the right-hand end")
	}
	if !strings.Contains(r, "-72h") {
		t.Error("the ruler does not mark the start of the window")
	}
	for _, line := range strings.Split(strings.TrimRight(r, "\n"), "\n") {
		if got := len([]rune(stripANSI(line))); got > tuiWidth {
			t.Errorf("ruler line runs to %d columns: %q", got, stripANSI(line))
		}
	}
}

// A failed fetch is named; an empty panel never silently vanishes.
func TestUptimeGraphPanelNamesFailures(t *testing.T) {
	out := renderUptimeGraphPanelANSI(uptimeGraphStats{Err: "empty response (every fetch path failed)"})
	if !strings.Contains(out, "unavailable") || !strings.Contains(out, "every fetch path failed") {
		t.Error("a failed fetch was not reported")
	}
	out = renderUptimeGraphPanelANSI(uptimeGraphStats{})
	if !strings.Contains(out, "UPTIME TIMELINE") || !strings.Contains(out, "unavailable") {
		t.Error("an empty panel disappeared instead of reporting itself absent")
	}
}
