// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/stats_tui.go c5-reward-server
//
// Terminal rendering of the network statistics, exported to HTML.
//
// There is ONE renderer here and it emits ANSI. Printed to a terminal it is
// the TUI; passed through ansifilter it is the HTML the reward server serves.
// That is deliberate: the reward UI has always looked like a terminal, and for
// a long time it literally was one — bash wrapped in a Go web server, whose
// output was already terminal-formatted and simply passed through. This
// restores that property on purpose instead of hand-writing HTML that
// resembles it, and it means the eventual TUI build of this page is the same
// code with a different sink.
//
// Libraries: asciigraph for the plots (already a dependency, and its
// SeriesColors/AxisColor options emit the SGR sequences ansifilter reads),
// ansifilter-go for the ANSI->HTML conversion, lolcat-go for the banner.
package clirewardsserver

import (
	"fmt"
	"strings"

	"github.com/0magnet/ansifilter-go/ansifilter"
	lolcat "github.com/0magnet/lolcat-go/lol"
	"github.com/guptarohit/asciigraph"
)

// ANSI attributes, written directly rather than through a styling library:
// ansifilter reads SGR sequences, so the producer can be this plain.
const (
	aReset  = "\x1b[0m"
	aDim    = "\x1b[2m"
	aBold   = "\x1b[1m"
	aCyan   = "\x1b[36m"
	aGreen  = "\x1b[32m"
	aYellow = "\x1b[33m"
	aRed    = "\x1b[31m"
	aBlue   = "\x1b[34m"
	aMag    = "\x1b[35m"
)

// tuiWidth is the column count the panels are drawn to. Fixed rather than
// responsive because the output must also be readable in an 80-column
// terminal, which is the narrower of the two targets.
const tuiWidth = 78

// statsTUIData is everything the renderer draws. Every field is optional: the
// page must render whatever was fetched successfully and say plainly what was
// not, rather than failing whole. Each Err field carries why its section is
// missing.
type statsTUIData struct {
	Transports   int
	UniqueVisors int
	ByType       map[string]int
	CountsErr    string

	// Daily is oldest-first. Callers reverse TPD's newest-first order.
	Daily       []statsTUIDay
	DailyErr    string
	Versions    map[string]int
	VersionsErr string

	// Liveness is visors-reporting-online per 5-minute slot, oldest-first,
	// with the label of the day each run of slots belongs to.
	Liveness    []statsTUILivenessDay
	LivenessErr string
}

type statsTUIDay struct {
	Date      string
	Bandwidth uint64
	Latency   float64
	ByType    map[string]uint64
}

type statsTUILivenessDay struct {
	Date  string
	Slots []int
}

func tuiRule(title string, width int) string {
	t := " " + title + " "
	if len(t)+4 > width {
		width = len(t) + 4
	}
	const left = 2
	right := width - len(t) - left
	if right < 0 {
		right = 0
	}
	return aDim + "┌" + strings.Repeat("─", left) + aReset + aBold + aCyan + t + aReset +
		aDim + strings.Repeat("─", right) + "┐" + aReset + "\n"
}

func tuiClose(width int) string {
	return aDim + "└" + strings.Repeat("─", width) + "┘" + aReset + "\n"
}

// tuiXAxis draws date ticks beneath a plot. asciigraph labels the Y axis and
// leaves X bare, which makes a multi-day series unreadable — the shape is
// visible but not when anything happened. leftPad is the width asciigraph
// reserved for its own labels, so ticks align to the plot area.
func tuiXAxis(labels []string, plotWidth, leftPad int) string {
	if len(labels) == 0 || plotWidth <= 0 {
		return ""
	}
	row := []rune(strings.Repeat(" ", plotWidth))
	ticks := []rune(strings.Repeat(" ", plotWidth))
	for i, lb := range labels {
		center := int((float64(i) + 0.5) / float64(len(labels)) * float64(plotWidth))
		if center < plotWidth {
			ticks[center] = '┬'
		}
		r := []rune(lb)
		start := center - len(r)/2
		if start < 0 {
			start = 0
		}
		if start+len(r) > plotWidth {
			start = plotWidth - len(r)
		}
		if start < 0 {
			continue
		}
		copy(row[start:], r)
	}
	pad := strings.Repeat(" ", leftPad)
	return aDim + pad + string(ticks) + aReset + "\n" + aDim + pad + string(row) + aReset + "\n"
}

func tuiBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// tuiBar renders a proportional bar. A column of numbers is data; a column of
// bars is a shape readable at a glance, which is the point of the aesthetic.
func tuiBar(frac float64, width int, color string) string {
	if frac < 0 {
		frac = 0
	}
	filled := int(frac*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	return color + strings.Repeat("█", filled) + aReset +
		aDim + strings.Repeat("·", width-filled) + aReset
}

var tuiTypeColors = map[string]string{
	"sudph": aYellow, "webrtc": aMag, "stcpr": aGreen,
	"squicr": aRed, "dmsg": aBlue, "swtr": aCyan, "swsr": aBlue,
}

// tuiMissing renders a section that could not be fetched. A named absence,
// never a zero — a rendered 0 reads as a measurement.
func tuiMissing(title, why string) string {
	width := tuiWidth
	return tuiRule(title, width) +
		fmt.Sprintf("  %sunavailable%s %s%s%s\n", aRed, aReset, aDim, why, aReset) +
		tuiClose(width)
}

// tuiPlot draws one labeled series panel.
func tuiPlot(title string, series []float64, labels []string, height int, precision uint, color asciigraph.AnsiColor, width int, footer string) string {
	if len(series) < 2 {
		return ""
	}
	var b strings.Builder
	b.WriteString(tuiRule(title, width))
	g := asciigraph.Plot(series,
		asciigraph.Height(height),
		asciigraph.Width(width-14),
		asciigraph.Precision(precision),
		asciigraph.SeriesColors(color),
		asciigraph.AxisColor(asciigraph.Gray),
		asciigraph.LabelColor(asciigraph.Gray),
	)
	for _, ln := range strings.Split(g, "\n") {
		b.WriteString("  " + ln + "\n")
	}
	b.WriteString(tuiXAxis(labels, width-14, 8))
	if footer != "" {
		b.WriteString("  " + aDim + footer + aReset + "\n")
	}
	b.WriteString(tuiClose(width))
	b.WriteString("\n")
	return b.String()
}

// RenderStatsANSI draws the whole page as ANSI. Exported so a TUI build can
// call it and print the result directly.
func RenderStatsANSI(d statsTUIData) string {
	var b strings.Builder
	width := tuiWidth

	opts := lolcat.DefaultOptions()
	opts.Freq = 0.20
	b.WriteString(lolcat.String("SKYWIRE NETWORK STATISTICS", opts) + "\n")
	b.WriteString(aDim + strings.Repeat("═", width) + aReset + "\n\n")

	// ---- headline ----------------------------------------------------------
	if d.CountsErr != "" {
		b.WriteString(tuiMissing("NETWORK", d.CountsErr) + "\n")
	} else {
		b.WriteString(tuiRule("NETWORK", width))
		b.WriteString(fmt.Sprintf("  %stransports%s %s%-10d%s  %svisors%s %s%-8d%s\n",
			aDim, aReset, aBold, d.Transports, aReset,
			aDim, aReset, aBold, d.UniqueVisors, aReset))

		total := 0
		for _, n := range d.Versions {
			total += n
		}
		newest, newestN := "", 0
		for v, n := range d.Versions {
			if strings.HasPrefix(v, "v1.3.94") && n > newestN {
				newest, newestN = v, n
			}
		}
		if total > 0 && newest != "" {
			pct := 100 * float64(newestN) / float64(total)
			col := aGreen
			if pct < 10 {
				col = aRed
			}
			b.WriteString(fmt.Sprintf("  %son newest%s  %s%s%s  %s%d of %d (%.1f%%)%s\n",
				aDim, aReset, aBold, newest, aReset, col, newestN, total, pct, aReset))
		}
		b.WriteString(tuiClose(width))
		b.WriteString("\n")
	}

	// ---- bandwidth / latency ----------------------------------------------
	if d.DailyErr != "" {
		b.WriteString(tuiMissing("BANDWIDTH & LATENCY", d.DailyErr) + "\n")
	} else if len(d.Daily) > 1 {
		labels := make([]string, 0, len(d.Daily))
		bw := make([]float64, 0, len(d.Daily))
		lat := make([]float64, 0, len(d.Daily))
		for _, day := range d.Daily {
			bw = append(bw, float64(day.Bandwidth)/(1<<30))
			lat = append(lat, day.Latency)
			labels = append(labels, tuiShortDate(day.Date))
		}
		b.WriteString(tuiPlot("BANDWIDTH — GB/day", bw, labels, 9, 1, asciigraph.Green, width, ""))
		b.WriteString(tuiPlot("LATENCY — ms/day", lat, labels, 7, 0, asciigraph.Yellow, width, ""))
	}

	// ---- visors online -----------------------------------------------------
	//
	// Summed from the v3 per-5-minute timelines. Labeled "visors online"
	// rather than uptime on purpose: that timeline disagrees with the same
	// endpoint's daily percentage for intermittent visors (skycoin/skywire
	// #4533), so it must not be presented as the uptime figure rewards use.
	if d.LivenessErr != "" {
		b.WriteString(tuiMissing("VISORS ONLINE", d.LivenessErr) + "\n")
	} else if len(d.Liveness) > 0 {
		var series []float64
		var labels []string
		samples := 0
		for _, day := range d.Liveness {
			if len(day.Slots) == 0 {
				continue
			}
			for _, n := range day.Slots {
				series = append(series, float64(n))
			}
			samples += len(day.Slots)
			labels = append(labels, tuiShortDate(day.Date))
		}
		footer := fmt.Sprintf("%d days · %d samples · 5-minute resolution", len(labels), samples)
		b.WriteString(tuiPlot("VISORS ONLINE — per 5 min", series, labels, 9, 0, asciigraph.Cyan, width, footer))
	}

	// ---- transports by type ------------------------------------------------
	if d.CountsErr == "" && len(d.ByType) > 0 {
		b.WriteString(tuiRule("TRANSPORTS BY TYPE", width))
		var latest map[string]uint64
		if len(d.Daily) > 0 {
			latest = d.Daily[len(d.Daily)-1].ByType
		}
		for _, t := range sortedTransportTypes(d.ByType) {
			n := d.ByType[t]
			frac := 0.0
			if d.Transports > 0 {
				frac = float64(n) / float64(d.Transports)
			}
			col := tuiTypeColors[t]
			if col == "" {
				col = aCyan
			}
			bwCol := ""
			if v, ok := latest[t]; ok {
				bwCol = fmt.Sprintf("  %s%9s/day%s", aDim, tuiBytes(v), aReset)
			}
			b.WriteString(fmt.Sprintf("  %s%-7s%s %s%6d%s %s %s%5.1f%%%s%s\n",
				col, t, aReset, aBold, n, aReset, tuiBar(frac, 28, col), aDim, 100*frac, aReset, bwCol))
		}
		b.WriteString(tuiClose(width))
		b.WriteString("\n")
	}

	// ---- version adoption --------------------------------------------------
	if d.VersionsErr != "" {
		b.WriteString(tuiMissing("VERSION ADOPTION", d.VersionsErr))
	} else if len(d.Versions) > 0 {
		b.WriteString(tuiRule("VERSION ADOPTION", width))
		total := 0
		for _, n := range d.Versions {
			total += n
		}
		vs := sortedTransportTypes(d.Versions) // same biggest-first ordering
		shown := 0
		for _, v := range vs {
			if shown >= 8 {
				break
			}
			shown++
			n := d.Versions[v]
			frac := 0.0
			if total > 0 {
				frac = float64(n) / float64(total)
			}
			col := aRed
			switch {
			case strings.HasPrefix(v, "v1.3.94"):
				col = aGreen
			case v == "unknown" || v == "null":
				col = aDim
			case strings.HasPrefix(v, "v1.3.9"):
				col = aYellow
			}
			b.WriteString(fmt.Sprintf("  %s%-26s%s %s%5d%s %s %s%5.1f%%%s\n",
				col, v, aReset, aBold, n, aReset, tuiBar(frac, 24, col), aDim, 100*frac, aReset))
		}
		if len(vs) > shown {
			b.WriteString(fmt.Sprintf("  %s… %d further builds%s\n", aDim, len(vs)-shown, aReset))
		}
		b.WriteString(tuiClose(width))
	}

	return b.String()
}

// tuiShortDate trims the year, which is constant across the window and only
// costs axis room.
func tuiShortDate(d string) string {
	if len(d) == 10 && d[4] == '-' {
		return d[5:]
	}
	return d
}

// RenderStatsHTMLFragment converts the ANSI rendering to an HTML fragment. The
// caller supplies the <pre>: fragment mode emits only the colored spans, so
// newlines collapse without one, and the page keeps ownership of its chrome.
func RenderStatsHTMLFragment(d statsTUIData) string {
	g := ansifilter.New(ansifilter.HTML)
	g.SetFragmentCode(true)
	return "<pre style='font-family:ui-monospace,Menlo,Consolas,monospace;font-size:13px;line-height:1.25;margin:0'>" +
		g.GenerateString(RenderStatsANSI(d)) + "</pre>"
}
