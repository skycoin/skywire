// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/stats_tui_arch.go c5-reward-server
//
// The fleet's CPU architectures, as a pie.
//
// WHERE THE DATA COMES FROM, AND WHERE IT DOES NOT.
//
// It does not come from TPD. TPD's visor heartbeat carries one free-form
// field — the version string — and nothing else; there is no arch field in
// store.VisorSummary, in the /version or /versions endpoints, or in any of
// the three CXO stats leaves. An architecture breakdown cannot be sourced
// from the transport discovery today without first plumbing arch through
// RecordHeartbeat, which is a TPD protocol change and not this panel's job.
//
// It comes from the operator surveys the reward server already holds: each
// visor's node-info.json carries `go_arch`, and the frequency table built
// from it is what /stats has been rendering as an HTML pie since before
// this terminal panel existed. This is the same data, same parse
// (ParseFrequencyStats), drawn where the rest of the page now lives — so
// the two renderings cannot disagree.
//
// That provenance is stated on the panel rather than left implied, because
// the two populations genuinely differ: the survey set is visors that
// submitted a survey, which is a subset of the visors TPD counts. A
// percentage over the wrong denominator is exactly the kind of confident
// wrong number the rest of this file's conventions exist to prevent.
//
// WHY THE PIE IS DRAWN HERE AND NOT BY A LIBRARY.
//
// Nothing vendored draws one. asciigraph plots series; 0magnet/plot-go is a
// stream-processing pipeline with no chart types; pterm has bar, heatmap
// and tree printers and no pie. Adding a dependency for one disc is worse
// than rasterizing it: the disc below is a cell-by-cell angle test, which
// is a dozen lines and has no version to track.
package clirewardsserver

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/bitfield/script"
)

// archStats is the panel's data.
type archStats struct {
	// Items is biggest-first; the renderer never reorders.
	Items []PieChartItem
	Total int
	Src   string
	Err   string
}

// archSliceColors cycles the panel palette. Adjacent slices must differ,
// so the order matters more than the individual choices.
var archSliceColors = []string{aCyan, aGreen, aYellow, aMag, aBlue, aRed}

// archSliceGlyphs cycles alongside the colors so the disc still separates
// into slices with color stripped — a monochrome terminal, or a reader who
// cannot distinguish two of the six. A pie that needs color to be read is
// a pie that is unreadable for some of its readers.
var archSliceGlyphs = []string{"█", "▓", "▒", "░", "▚", "▞"}

// Disc geometry. Terminal cells are about twice as tall as they are wide,
// so a round disc needs twice as many columns as rows.
const (
	archPieRows = 13
	archPieCols = 27
)

// gatherArchStats reads the survey architecture frequency table. As
// everywhere else in this panel, a failure is recorded, not returned.
func gatherArchStats() archStats {
	var a archStats

	raw, err := script.File(tempStatsPath + "/arch.txt").String()
	if err != nil {
		a.Err = fmt.Sprintf("survey architecture table unreadable: %s", err)
		return a
	}
	items := ParseFrequencyStats(raw)
	if len(items) == 0 {
		a.Err = "the survey architecture table carried no architectures"
		return a
	}
	for _, it := range items {
		a.Total += it.Count
	}
	if a.Total == 0 {
		a.Err = "the survey architecture table summed to zero visors"
		return a
	}
	// Biggest first: the disc is drawn clockwise from twelve o'clock and
	// a reader follows it in that order.
	sort.SliceStable(items, func(i, j int) bool { return items[i].Count > items[j].Count })
	a.Items = items
	a.Src = fmt.Sprintf("source: operator surveys (node-info.json go_arch), %d visors — "+
		"NOT the TPD visor count; TPD carries no architecture field", a.Total)
	return a
}

// archPieDisc rasterizes the pie into rows of cells, and reports how many
// cells each slice got.
//
// Angles run clockwise from twelve o'clock, which is where a reader
// expects a pie to start: with screen coordinates (x right, y down),
// atan2(x, -y) is 0 at north, π/2 at east, π at south.
//
// The cell counts are returned because a disc this size quantizes to
// roughly a percent, so a slice under that threshold draws NOTHING. It is
// still in the legend with its real count, and the panel says how many
// slices fell below the disc's resolution — an architecture silently
// absent from a picture of all architectures is the failure this avoids.
func archPieDisc(items []PieChartItem, total int) ([]string, []int) {
	if total <= 0 || len(items) == 0 {
		return nil, nil
	}
	cells := make([]int, len(items))
	// Cumulative slice boundaries as fractions of a turn.
	bounds := make([]float64, len(items))
	acc := 0.0
	for i, it := range items {
		acc += float64(it.Count) / float64(total)
		bounds[i] = acc
	}
	bounds[len(bounds)-1] = 1.0

	cx := float64(archPieCols-1) / 2
	cy := float64(archPieRows-1) / 2
	rows := make([]string, 0, archPieRows)
	for r := 0; r < archPieRows; r++ {
		var line strings.Builder
		lastColor := ""
		for c := 0; c < archPieCols; c++ {
			x := (float64(c) - cx) / cx
			y := (float64(r) - cy) / cy
			if x*x+y*y > 1.0 {
				if lastColor != "" {
					line.WriteString(aReset)
					lastColor = ""
				}
				line.WriteByte(' ')
				continue
			}
			frac := math.Atan2(x, -y) / (2 * math.Pi)
			if frac < 0 {
				frac++
			}
			idx := len(items) - 1
			for i, bnd := range bounds {
				if frac < bnd {
					idx = i
					break
				}
			}
			col := archSliceColors[idx%len(archSliceColors)]
			if col != lastColor {
				line.WriteString(col)
				lastColor = col
			}
			line.WriteString(archSliceGlyphs[idx%len(archSliceGlyphs)])
			cells[idx]++
		}
		if lastColor != "" {
			line.WriteString(aReset)
		}
		rows = append(rows, line.String())
	}
	return rows, cells
}

// renderArchPanelANSI draws the disc with its legend beside it.
func renderArchPanelANSI(a archStats) string {
	const title = "ARCHITECTURE"
	width := tuiWidth

	if a.Err != "" {
		return tuiMissing(title, a.Err) + "\n"
	}
	if len(a.Items) == 0 || a.Total == 0 {
		return tuiMissing(title, "no data returned") + "\n"
	}

	disc, cells := archPieDisc(a.Items, a.Total)

	// Legend rows, one per architecture. Built first so the two columns
	// can be zipped to whichever is taller.
	legend := make([]string, 0, len(a.Items))
	for i, it := range a.Items {
		col := archSliceColors[i%len(archSliceColors)]
		glyph := archSliceGlyphs[i%len(archSliceGlyphs)]
		pct := 100 * float64(it.Count) / float64(a.Total)
		label := it.Label
		if len(label) > 9 {
			label = label[:9]
		}
		legend = append(legend, fmt.Sprintf("%s%s%s %s%-9s%s %s%5d%s %s%5.1f%%%s",
			col, glyph, aReset, col, label, aReset, aBold, it.Count, aReset, aDim, pct, aReset))
	}

	var b strings.Builder
	b.WriteString(tuiRule(title+" — share of surveyed visors", width))
	rows := len(disc)
	if len(legend) > rows {
		rows = len(legend)
	}
	for i := 0; i < rows; i++ {
		left := strings.Repeat(" ", archPieCols)
		if i < len(disc) {
			left = disc[i]
		}
		right := ""
		if i < len(legend) {
			right = legend[i]
		}
		b.WriteString("  " + left + "  " + right + "\n")
	}
	tooSmall := 0
	for i := range a.Items {
		if i < len(cells) && cells[i] == 0 {
			tooSmall++
		}
	}
	note := ""
	if tooSmall > 0 {
		note = fmt.Sprintf(" · %d below the disc's resolution, legend only", tooSmall)
	}
	b.WriteString(tuiWrap(fmt.Sprintf("  %d architectures · %d surveyed visors%s",
		len(a.Items), a.Total, note)))
	b.WriteString(tuiSource(a.Src))
	b.WriteString(tuiClose(width))
	b.WriteString("\n")
	return b.String()
}
