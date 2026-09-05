// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/stats_tui_arch_test.go c5-reward-server
package clirewardsserver

import (
	"sort"
	"strings"
	"testing"
)

// sampleArch is the shape the survey frequency table produces: a count
// and a go_arch value per line, biggest first after gathering.
func sampleArch() archStats {
	items := []PieChartItem{
		{Label: "amd64", Count: 520},
		{Label: "arm64", Count: 260},
		{Label: "arm", Count: 130},
		{Label: "386", Count: 60},
		{Label: "riscv64", Count: 20},
		{Label: "wasm", Count: 10},
	}
	total := 0
	for _, it := range items {
		total += it.Count
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Count > items[j].Count })
	return archStats{Items: items, Total: total, Src: "source: operator surveys (node-info.json go_arch)"}
}

// The survey table is the same text /stats already renders as an HTML
// pie. Parsing it here rather than re-deriving it is what keeps the two
// renderings from disagreeing.
func TestArchParsesTheSurveyFrequencyTable(t *testing.T) {
	items := ParseFrequencyStats("Survey architecture statistics:\n    520 amd64\n    260 arm64\n      7 riscv64\n")
	if len(items) != 3 {
		t.Fatalf("parsed %d architectures, want 3", len(items))
	}
	if items[0].Label != "amd64" || items[0].Count != 520 {
		t.Errorf("first row is %+v, want amd64/520", items[0])
	}
}

// Every architecture must appear in the legend. A pie that silently drops
// its smallest slices reports a fleet that does not exist.
func TestArchPanelNamesEveryArchitecture(t *testing.T) {
	a := sampleArch()
	out := renderArchPanelANSI(a)
	for _, it := range a.Items {
		if !strings.Contains(out, it.Label) {
			t.Errorf("architecture %q is missing from the legend", it.Label)
		}
	}
	if !strings.Contains(out, "ARCHITECTURE") {
		t.Error("the panel title is missing")
	}
}

// The disc must be proportional: a slice's area is its share. Counting
// cells is the only way to assert that the angle arithmetic is right
// rather than merely producing a circle.
func TestArchPieSlicesAreProportional(t *testing.T) {
	a := sampleArch()
	rows, _ := archPieDisc(a.Items, a.Total)
	if len(rows) != archPieRows {
		t.Fatalf("disc is %d rows, want %d", len(rows), archPieRows)
	}

	cells := make(map[string]int)
	filled := 0
	for _, r := range rows {
		for i, g := range archSliceGlyphs {
			if i >= len(a.Items) {
				break
			}
			n := strings.Count(r, g)
			cells[a.Items[i].Label] += n
			filled += n
		}
	}
	if filled < 200 {
		t.Fatalf("only %d cells were filled; the disc is not being drawn", filled)
	}
	for _, it := range a.Items {
		want := float64(it.Count) / float64(a.Total)
		got := float64(cells[it.Label]) / float64(filled)
		// A disc of this size holds a few hundred cells, so a slice is
		// quantized to roughly a percent. Five points of slack keeps the
		// assertion about proportionality rather than about rasterization.
		if got-want > 0.05 || want-got > 0.05 {
			t.Errorf("%s covers %.1f%% of the disc, want %.1f%%", it.Label, 100*got, 100*want)
		}
	}
}

// The slices must separate without color: a monochrome terminal, or a
// reader who cannot tell two of the six apart, still has to read the pie.
// Slices too small to occupy a cell are legend-only by construction, so
// the assertion is over the slices that were actually drawn.
func TestArchPieSlicesDifferWithoutColor(t *testing.T) {
	a := sampleArch()
	rows, cells := archPieDisc(a.Items, a.Total)

	drawn := 0
	for _, n := range cells {
		if n > 0 {
			drawn++
		}
	}
	if drawn < 2 {
		t.Fatalf("only %d slices were drawn; the disc is not being rasterized", drawn)
	}
	seen := make(map[rune]bool)
	for _, r := range rows {
		for _, ch := range stripANSI(r) {
			if ch != ' ' {
				seen[ch] = true
			}
		}
	}
	if len(seen) < drawn {
		t.Errorf("the disc uses %d distinct glyphs for %d drawn slices; slices merge without color",
			len(seen), drawn)
	}
}

// A slice too small to draw must still be accounted for in words, or the
// picture claims to cover every architecture while omitting one.
func TestArchPanelAccountsForUndrawableSlices(t *testing.T) {
	a := archStats{Items: []PieChartItem{
		{Label: "amd64", Count: 9990},
		{Label: "riscv64", Count: 1},
	}, Total: 9991}
	_, cells := archPieDisc(a.Items, a.Total)
	if cells[1] != 0 {
		t.Skip("the disc grew enough resolution to draw a 0.01% slice")
	}
	out := renderArchPanelANSI(a)
	if !strings.Contains(out, "below the disc's resolution") {
		t.Error("a slice too small to draw was omitted with no mention")
	}
	if !strings.Contains(out, "riscv64") {
		t.Error("the undrawable slice is missing from the legend too")
	}
}

// The provenance is the point: the survey population is not the TPD visor
// count, and a percentage over the wrong denominator is a confident wrong
// number.
func TestArchPanelStatesItsProvenance(t *testing.T) {
	a := sampleArch()
	a.Src = "source: operator surveys (node-info.json go_arch), 1000 visors — " +
		"NOT the TPD visor count; TPD carries no architecture field"
	out := renderArchPanelANSI(a)
	if !strings.Contains(out, "surveys") || !strings.Contains(out, "TPD carries no architecture field") {
		t.Error("the panel does not say where its population came from")
	}
}

// A failed read is named; an empty panel never silently vanishes.
func TestArchPanelNamesFailures(t *testing.T) {
	out := renderArchPanelANSI(archStats{Err: "survey architecture table unreadable: no such file"})
	if !strings.Contains(out, "unavailable") || !strings.Contains(out, "no such file") {
		t.Error("a failed read was not reported")
	}
	out = renderArchPanelANSI(archStats{})
	if !strings.Contains(out, "ARCHITECTURE") || !strings.Contains(out, "unavailable") {
		t.Error("an empty panel disappeared instead of reporting itself absent")
	}
}

// The panel is drawn beside a legend inside a 78-column rule.
func TestArchPanelFitsThePanelWidth(t *testing.T) {
	for _, line := range strings.Split(renderArchPanelANSI(sampleArch()), "\n") {
		if got := len([]rune(stripANSI(line))); got > tuiWidth+2 {
			t.Errorf("line runs to %d columns: %q", got, stripANSI(line))
		}
	}
}
