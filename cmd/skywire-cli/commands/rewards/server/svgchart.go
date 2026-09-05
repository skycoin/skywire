// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/svgchart.go c4-vis-cli
package clirewardsserver

import (
	"fmt"
	"html"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Server-rendered SVG charting for the /stats pages.
//
// These pages carry no external JS or CSS — no CDN, no chart library — so the
// charts are emitted as inline SVG with a handful of elements each. The
// predecessor drew a stacked area as one absolutely-positioned 1-pixel <div>
// per pixel: 14,417 elements and 2.1 MB of HTML for the version history alone.
// A <path> per series replaces the whole column of divs.

// chartColors is the categorical palette shared by every chart here. It is the
// pie-chart palette with the same leading entries, so a version or transport
// type keeps a recognizable color across the page.
var chartColors = []string{
	"#36A2EB", "#FF6384", "#4BC0C0", "#FFCE56", "#9966FF",
	"#FF9F40", "#7BC8A4", "#C9CBCF", "#E7526B", "#45B7D1",
	"#96CEB4", "#D4A5A5", "#9B59B6", "#2ECC71", "#F39C12",
	"#3498DB", "#E74C3C", "#1ABC9C", "#E67E22", "#95A5A6",
}

// chartSeries is one named band or line, aligned index-for-index with the
// chart's label slice.
type chartSeries struct {
	Name  string
	Color string
	// Vals is the series, aligned to chartOpts.Labels. A NaN is a RECORDED GAP
	// — no measurement for that index — and the line renderer breaks rather
	// than bridging it. That distinction matters for averages like latency: a
	// line dropped to the axis reads as "measured 0 ms", which is a phantom
	// measurement, not a missing one.
	Vals []float64
	// Note is appended to the series' <title>, e.g. a peak or current value.
	Note string
}

// chartOpts controls the shared geometry and axis formatting.
type chartOpts struct {
	// Width/Height are viewBox units. The <svg> itself is width:100% so the
	// chart scales to the viewport; the viewBox fixes the aspect ratio.
	Width, Height int
	// Labels are the x-axis categories (dates), one per data index.
	Labels []string
	// FormatY renders a y-axis tick. Defaults to a plain integer.
	FormatY func(float64) string
	// Title is rendered above the chart as an <h3>.
	Title string
	// YAxisLabel is shown under the title, e.g. the unit.
	YAxisLabel string
	// MaxXLabels caps how many x labels are drawn before thinning.
	MaxXLabels int
	// XTicks names the exact indices to label. When set it overrides the
	// even-stride thinning, which is what a series sampled far finer than its
	// natural period needs — 288 five-minute slots per day want a label on each
	// day boundary, not every 252nd slot.
	XTicks []int
}

const (
	chartMarginLeft   = 62
	chartMarginRight  = 12
	chartMarginTop    = 10
	chartMarginBottom = 26
)

func (o chartOpts) plotWidth() int  { return o.Width - chartMarginLeft - chartMarginRight }
func (o chartOpts) plotHeight() int { return o.Height - chartMarginTop - chartMarginBottom }

// fmtCoord trims a coordinate to one decimal and drops a trailing ".0". Over
// thousands of points this is a material share of the document size.
func fmtCoord(v float64) string {
	s := strconv.FormatFloat(v, 'f', 1, 64)
	return strings.TrimSuffix(s, ".0")
}

// niceTicks returns up to n round-numbered gridline values spanning 0..max.
func niceTicks(max float64, n int) []float64 {
	if max <= 0 || n <= 0 {
		return []float64{0}
	}
	raw := max / float64(n)
	mag := 1.0
	for mag*10 <= raw {
		mag *= 10
	}
	for mag > raw && mag > 1e-9 {
		mag /= 10
	}
	step := mag
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if mag*m >= raw {
			step = mag * m
			break
		}
	}
	var ticks []float64
	for v := 0.0; v <= max*1.0001; v += step {
		ticks = append(ticks, v)
	}
	if len(ticks) == 0 {
		ticks = []float64{0, max}
	}
	return ticks
}

// chartFrame emits the gridlines, y tick labels, x labels and the axis line.
// Shared by the stacked-area and line renderers so both read identically.
func chartFrame(sb *strings.Builder, o chartOpts, vmax float64) {
	iw, ih := o.plotWidth(), o.plotHeight()
	formatY := o.FormatY
	if formatY == nil {
		formatY = func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
	}
	yOf := func(v float64) float64 {
		if vmax <= 0 {
			return float64(chartMarginTop + ih)
		}
		return float64(chartMarginTop) + float64(ih) - v/vmax*float64(ih)
	}

	for _, t := range niceTicks(vmax, 4) {
		y := fmtCoord(yOf(t))
		fmt.Fprintf(sb, "<line x1='%d' x2='%d' y1='%s' y2='%s' stroke='#333' stroke-dasharray='3 3'/>",
			chartMarginLeft, chartMarginLeft+iw, y, y)
		fmt.Fprintf(sb, "<text x='%d' y='%s' text-anchor='end' fill='#888' font-size='10'>%s</text>",
			chartMarginLeft-6, fmtCoord(yOf(t)+3.5), html.EscapeString(formatY(t)))
	}
	fmt.Fprintf(sb, "<line x1='%d' x2='%d' y1='%d' y2='%d' stroke='#555'/>",
		chartMarginLeft, chartMarginLeft+iw, chartMarginTop+ih, chartMarginTop+ih)

	// X labels, thinned so they cannot overlap.
	n := len(o.Labels)
	if n == 0 {
		return
	}
	if len(o.XTicks) > 0 {
		for _, i := range o.XTicks {
			if i < 0 || i >= n {
				continue
			}
			anchor := "middle"
			if i == 0 {
				anchor = "start"
			} else if i == n-1 {
				anchor = "end"
			}
			fmt.Fprintf(sb, "<text x='%s' y='%d' text-anchor='%s' fill='#888' font-size='10'>%s</text>",
				fmtCoord(chartX(o, i)), o.Height-6, anchor, html.EscapeString(o.Labels[i]))
		}
		return
	}

	maxLabels := o.MaxXLabels
	if maxLabels <= 0 {
		maxLabels = 8
	}
	step := 1
	if n > maxLabels {
		step = (n + maxLabels - 1) / maxLabels
	}
	last := n - 1
	for i := 0; i < n; i += step {
		// Leave the right edge to the final label below, so a thinned label and
		// the last one cannot land on top of each other.
		if i != 0 && last-i < step {
			break
		}
		anchor := "middle"
		if i == 0 {
			anchor = "start"
		}
		fmt.Fprintf(sb, "<text x='%s' y='%d' text-anchor='%s' fill='#888' font-size='10'>%s</text>",
			fmtCoord(chartX(o, i)), o.Height-6, anchor, html.EscapeString(o.Labels[i]))
	}
	if n > 1 {
		fmt.Fprintf(sb, "<text x='%s' y='%d' text-anchor='end' fill='#888' font-size='10'>%s</text>",
			fmtCoord(chartX(o, last)), o.Height-6, html.EscapeString(o.Labels[last]))
	}
}

// svgOpenTag sizes the chart from its viewBox alone: width:100% with height
// auto scales uniformly, so the axis text scales with the plot. An explicit
// pixel height would let a wide viewport stretch x without y and smear the
// labels sideways.
func svgOpenTag(o chartOpts) string {
	return fmt.Sprintf("<svg viewBox='0 0 %d %d' role='img' style='width:100%%;height:auto;max-width:%dpx;background:#111;'>",
		o.Width, o.Height, o.Width*2)
}

// chartX maps a data index to a viewBox x coordinate.
func chartX(o chartOpts, i int) float64 {
	n := len(o.Labels)
	if n <= 1 {
		return float64(chartMarginLeft + o.plotWidth()/2)
	}
	return float64(chartMarginLeft) + float64(i)/float64(n-1)*float64(o.plotWidth())
}

// renderStackedAreaSVG draws series as a stacked area, one <path> per series.
//
// The bands are drawn as OVERLAPPING cumulative areas — the full stack first,
// then the stack minus the top series, and so on — rather than as closed
// ribbons. Each band therefore needs only its own upper edge plus two points to
// close along the baseline, which halves the coordinate count versus emitting
// an up-path and a reversed down-path per band. Painter order makes the visible
// result identical to a conventional stack.
func renderStackedAreaSVG(o chartOpts, series []chartSeries) string {
	n := len(o.Labels)
	if n == 0 || len(series) == 0 {
		return "<p style='color:#888;'>No data points.</p>"
	}

	// Cumulative totals, and the running sums each band's upper edge sits at.
	upper := make([][]float64, len(series))
	running := make([]float64, n)
	for si := range series {
		edge := make([]float64, n)
		for i := 0; i < n; i++ {
			if i < len(series[si].Vals) {
				running[i] += series[si].Vals[i]
			}
			edge[i] = running[i]
		}
		upper[si] = edge
	}
	vmax := 0.0
	for _, v := range running {
		if v > vmax {
			vmax = v
		}
	}
	if vmax <= 0 {
		return "<p style='color:#888;'>No data points.</p>"
	}

	ih := o.plotHeight()
	baseY := float64(chartMarginTop + ih)
	yOf := func(v float64) float64 { return baseY - v/vmax*float64(ih) }

	var sb strings.Builder
	if o.Title != "" {
		fmt.Fprintf(&sb, "<h3>%s</h3>", html.EscapeString(o.Title))
	}
	if o.YAxisLabel != "" {
		fmt.Fprintf(&sb, "<p style='color:#888;font-size:11px;margin:2px 0;'>%s</p>", html.EscapeString(o.YAxisLabel))
	}
	sb.WriteString(svgOpenTag(o))
	chartFrame(&sb, o, vmax)

	// Paint from the outermost cumulative band inward, so each successive
	// (smaller) area covers the one below it and the uncovered remainder is
	// exactly that series' contribution.
	for si := len(series) - 1; si >= 0; si-- {
		d := cumulativePath(o, upper[si], yOf, baseY)
		if d == "" {
			continue
		}
		fmt.Fprintf(&sb, "<path d='%s' fill='%s' fill-opacity='0.9' stroke='%s' stroke-width='0.4'><title>%s</title></path>",
			d, series[si].Color, series[si].Color, html.EscapeString(seriesTitle(series[si])))
	}
	sb.WriteString("</svg>")
	sb.WriteString(renderChartLegend(series))
	return sb.String()
}

// cumulativePath builds the closed area under one cumulative edge.
//
// Coordinates are rounded to whole viewBox units — the plot is a few hundred
// units tall and sub-unit precision is invisible — and runs of points that
// round to the same y are collapsed to the run's first and last point. Keeping
// BOTH ends of a flat run matters: dropping the interior only would leave the
// path sloping across the whole run instead of running flat and then stepping,
// which silently redraws the data. Two years of daily version counts sit still
// for long stretches, so this is most of the compression.
//
// Consecutive line-tos also drop the repeated "L" command, which SVG allows.
func cumulativePath(o chartOpts, edge []float64, yOf func(float64) float64, baseY float64) string {
	n := len(o.Labels)
	if n == 0 {
		return ""
	}
	xs := make([]int, n)
	ys := make([]int, n)
	for i := 0; i < n; i++ {
		v := 0.0
		if i < len(edge) {
			v = edge[i]
		}
		xs[i] = int(chartX(o, i) + 0.5)
		ys[i] = int(yOf(v) + 0.5)
	}

	d, emitted := compactPath(xs, ys)
	if emitted == 0 {
		return ""
	}
	if emitted == 1 {
		d.WriteString("L")
	} else {
		d.WriteString(" ")
	}
	fmt.Fprintf(d, "%d %d %d %dZ", xs[n-1], int(baseY+0.5), xs[0], int(baseY+0.5))
	return d.String()
}

// gapY marks an index with no measurement. Real y coordinates are viewBox
// units, always well inside the canvas, so a large sentinel cannot collide.
const gapY = math.MinInt32

// compactPath encodes rounded points as SVG path data: runs of points sharing a
// y are collapsed to the run's two ends, consecutive line-tos drop the repeated
// "L", and a gapY lifts the pen so a missing measurement breaks the line
// instead of being bridged or drawn down to the axis. Returns the builder and
// the number of points emitted.
func compactPath(xs, ys []int) (*strings.Builder, int) {
	var d strings.Builder
	n := len(ys)
	emitted := 0
	// inSub counts points already written in the CURRENT subpath: 0 means the
	// next point opens one with "M", 1 means it needs an explicit "L", and from
	// 2 on the lineto is implicit and only a separator is written.
	inSub := 0
	for i := 0; i < n; i++ {
		if ys[i] == gapY {
			inSub = 0
			continue
		}
		// Drop the interior of a flat run, keeping the point that starts it and
		// the point that ends it. Keeping only one end would leave the line
		// sloping across the whole run instead of running flat and then
		// stepping, which redraws the data.
		if inSub > 0 && i > 0 && i < n-1 && ys[i-1] == ys[i] && ys[i+1] == ys[i] {
			continue
		}
		switch inSub {
		case 0:
			d.WriteString("M")
		case 1:
			d.WriteString("L")
		default:
			d.WriteString(" ")
		}
		fmt.Fprintf(&d, "%d %d", xs[i], ys[i])
		inSub++
		emitted++
	}
	return &d, emitted
}

// renderLineSVG draws each series as one <polyline>, with a transparent hit
// rect per x column carrying the full cross-series readout for that column as
// a <title>. That is one element per date rather than one per pixel, so hover
// detail survives without the document size.
func renderLineSVG(o chartOpts, series []chartSeries, hover []string) string {
	n := len(o.Labels)
	if n == 0 || len(series) == 0 {
		return "<p style='color:#888;'>No data points.</p>"
	}
	vmax := 0.0
	for _, s := range series {
		for _, v := range s.Vals {
			if !math.IsNaN(v) && v > vmax {
				vmax = v
			}
		}
	}
	if vmax <= 0 {
		return "<p style='color:#888;'>No data points.</p>"
	}

	ih := o.plotHeight()
	baseY := float64(chartMarginTop + ih)
	yOf := func(v float64) float64 { return baseY - v/vmax*float64(ih) }

	var sb strings.Builder
	if o.Title != "" {
		fmt.Fprintf(&sb, "<h3>%s</h3>", html.EscapeString(o.Title))
	}
	if o.YAxisLabel != "" {
		fmt.Fprintf(&sb, "<p style='color:#888;font-size:11px;margin:2px 0;'>%s</p>", html.EscapeString(o.YAxisLabel))
	}
	sb.WriteString(svgOpenTag(o))
	chartFrame(&sb, o, vmax)

	for _, s := range series {
		// A <path> rather than a <polyline> so the pen can lift over a gap.
		xs, ys := make([]int, n), make([]int, n)
		measured := 0
		for i := 0; i < n; i++ {
			v := math.NaN()
			if i < len(s.Vals) {
				v = s.Vals[i]
			}
			xs[i] = int(chartX(o, i) + 0.5)
			if math.IsNaN(v) {
				ys[i] = gapY
				continue
			}
			ys[i] = int(yOf(v) + 0.5)
			measured++
		}
		d, emitted := compactPath(xs, ys)
		if emitted == 0 {
			continue
		}
		fmt.Fprintf(&sb, "<path d='%s' fill='none' stroke='%s' stroke-width='2' stroke-linejoin='round' stroke-linecap='round'><title>%s</title></path>",
			d.String(), s.Color, html.EscapeString(seriesTitle(s)))
		// A single measured point draws no stroke at all, so mark it.
		if measured == 1 {
			for i := 0; i < n; i++ {
				if ys[i] != gapY {
					fmt.Fprintf(&sb, "<circle cx='%d' cy='%d' r='3' fill='%s'/>", xs[i], ys[i], s.Color)
					break
				}
			}
		}
	}
	writeHoverRects(&sb, o, hover)
	sb.WriteString("</svg>")
	sb.WriteString(renderChartLegend(series))
	return sb.String()
}

// writeHoverRects emits one transparent column per data index whose <title> is
// the readout for that column. Skipped when there is no readout to show.
func writeHoverRects(sb *strings.Builder, o chartOpts, hover []string) {
	n := len(o.Labels)
	if len(hover) == 0 || n == 0 {
		return
	}
	w := float64(o.plotWidth()) / float64(n)
	if w <= 0 {
		return
	}
	for i := 0; i < n && i < len(hover); i++ {
		if hover[i] == "" {
			continue
		}
		x := chartX(o, i) - w/2
		if x < float64(chartMarginLeft) {
			x = float64(chartMarginLeft)
		}
		fmt.Fprintf(sb, "<rect x='%s' y='%d' width='%s' height='%d' fill='transparent'><title>%s</title></rect>",
			fmtCoord(x), chartMarginTop, fmtCoord(w), o.plotHeight(), html.EscapeString(hover[i]))
	}
}

func seriesTitle(s chartSeries) string {
	if s.Note != "" {
		return s.Name + " — " + s.Note
	}
	return s.Name
}

// renderChartLegend renders the swatch list under a chart.
func renderChartLegend(series []chartSeries) string {
	var sb strings.Builder
	sb.WriteString("<div style='margin-top:8px;display:flex;flex-wrap:wrap;gap:6px 14px;font-size:12px;'>")
	for _, s := range series {
		fmt.Fprintf(&sb, "<span style='display:inline-flex;align-items:center;gap:4px;white-space:nowrap;'><span style='display:inline-block;width:11px;height:11px;background:%s;flex-shrink:0;'></span>%s</span>",
			s.Color, html.EscapeString(s.Name))
	}
	sb.WriteString("</div>")
	return sb.String()
}

// shortDate turns 2026-09-04 into 09-04, keeping the year only when it changes
// across the label set.
func shortDates(dates []string) []string {
	out := make([]string, len(dates))
	prevYear := ""
	for i, d := range dates {
		parts := strings.Split(d, "-")
		if len(parts) != 3 {
			out[i] = d
			continue
		}
		if parts[0] != prevYear {
			out[i] = d
			prevYear = parts[0]
			continue
		}
		out[i] = parts[1] + "-" + parts[2]
	}
	return out
}

// sortedMapKeys returns a map's keys ordered by descending value, ties broken
// by name. Ranging a Go map directly reshuffles rendered output between
// requests, which makes unchanged data look like it moved.
func sortedMapKeys[V int | uint64 | float64](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	return keys
}
