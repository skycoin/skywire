package charts

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"html"
	"math"
	"strconv"
	"strings"
	"time"
)

// Kind is how a chart draws its series.
type Kind int

const (
	// Lines draws each series as its own line.
	Lines Kind = iota
	// Stacked draws the series as stacked areas, so the top edge is the total.
	Stacked
)

// Series is one named series aligned with its chart's Times. NaN is a gap.
type Series struct {
	Name string
	// Title is shown on hover over the legend entry, such as a full key.
	Title string
	// Cells fill the columns named by the chart's Legend.
	Cells []string
	Vals  []float64
}

// Chart is one chart on a page.
type Chart struct {
	Title  string
	Note   string
	Kind   Kind
	From   time.Time
	To     time.Time
	Times  []time.Time
	Series []Series
	// Format renders a value. Count is used when it is nil.
	Format func(float64) string
	// Dates labels the x axis and readout with dates only.
	Dates bool
	// Binary rounds the y axis to powers of 1024, for byte counts.
	Binary bool
	// Legend, when set, draws the legend as a table with these column headers.
	// The first column is the series name and the last is its latest value.
	Legend []string

	colors map[string]string
}

const (
	svgW, svgH                    = 960, 300
	padL, padR, padT, padB        = 56, 16, 12, 28
	plotW                         = svgW - padL - padR
	plotH                         = svgH - padT - padB
	maxXTicks                     = 8
	timeFmt, dateFmt, dateTimeFmt = "15:04", "Jan 2", "Jan 2 15:04"
)

var palette = [...]string{
	"#4e79a7", "#f28e2b", "#e15759", "#76b7b2", "#59a14f",
	"#edc948", "#b07aa1", "#ff9da7", "#9c755f", "#86bcb6",
	"#d37295", "#8cd17d", "#a0cbe8", "#ffbe7d", "#bab0ac",
}

var fixedColors = map[string]string{
	"total": "#4e79a7", "other": "#bab0ac",
	"dmsg": "#4e79a7", "stcpr": "#f28e2b", "sudph": "#e15759", "stcp": "#76b7b2",
	"webrtc": "#59a14f", "quic": "#edc948", "swtr": "#b07aa1", "stun": "#ff9da7",
}

// Color returns a stable color for a series name.
func Color(name string) string {
	if c, ok := fixedColors[name]; ok {
		return c
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(name)) //nolint:errcheck
	return palette[h.Sum32()%uint32(len(palette))]
}

// Count formats a count compactly: 950, 9.5k, 1.2M.
func Count(v float64) string {
	a := math.Abs(v)
	switch {
	case a >= 1e6:
		return trimZero(strconv.FormatFloat(v/1e6, 'f', 1, 64)) + "M"
	case a >= 1e4:
		return trimZero(strconv.FormatFloat(v/1e3, 'f', 1, 64)) + "k"
	case a >= 100 || a == math.Trunc(a):
		return strconv.FormatFloat(v, 'f', 0, 64)
	default:
		return strconv.FormatFloat(v, 'f', 1, 64)
	}
}

// Bytes formats a byte count with binary units.
func Bytes(v float64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	i := 0
	for math.Abs(v) >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return strconv.FormatFloat(v, 'f', 0, 64) + " B"
	}
	return trimZero(strconv.FormatFloat(v, 'f', 1, 64)) + " " + units[i]
}

func trimZero(s string) string { return strings.TrimSuffix(s, ".0") }

func niceStep(max float64, n int) float64 {
	raw := max / float64(n)
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if mag*m >= raw {
			return mag * m
		}
	}
	return mag * 10
}

func (c *Chart) format(v float64) string {
	if c.Format != nil {
		return c.Format(v)
	}
	return Count(v)
}

func (c *Chart) xOf(t time.Time) float64 {
	span := c.To.Sub(c.From)
	if span <= 0 {
		return padL
	}
	return padL + float64(t.Sub(c.From))/float64(span)*plotW
}

// tops returns the value each series is drawn at: its own value for lines,
// the running total for a stack.
func (c *Chart) tops() [][]float64 {
	out := make([][]float64, len(c.Series))
	run := make([]float64, len(c.Times))
	for si, s := range c.Series {
		out[si] = make([]float64, len(c.Times))
		for i := range c.Times {
			v := math.NaN()
			if i < len(s.Vals) {
				v = s.Vals[i]
			}
			if c.Kind == Stacked {
				run[i] += v
				v = run[i]
			}
			out[si][i] = v
		}
	}
	return out
}

// SVG renders the chart, followed by its legend and the readout data.
func (c *Chart) SVG(id string) string {
	tops := c.tops()
	vmax := 0.0
	for _, s := range tops {
		for _, v := range s {
			if !math.IsNaN(v) && v > vmax {
				vmax = v
			}
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "<figure class='chart' id='%s'><figcaption><h2>%s</h2>", id, html.EscapeString(c.Title))
	if c.Note != "" {
		fmt.Fprintf(&sb, "<p>%s</p>", html.EscapeString(c.Note))
	}
	sb.WriteString("</figcaption>")
	if len(c.Times) == 0 {
		sb.WriteString("<p class='empty'>No samples in this range yet.</p></figure>")
		return sb.String()
	}
	if vmax <= 0 {
		vmax = 1
	}
	step := niceStep(vmax, 4)
	if c.Binary {
		unit := math.Pow(1024, math.Floor(math.Log(vmax)/math.Log(1024)))
		step = niceStep(vmax/unit, 4) * unit
	}
	top := math.Ceil(vmax/step) * step
	yOf := func(v float64) float64 { return padT + plotH - v/top*plotH }

	fmt.Fprintf(&sb, "<div class='plot'><svg viewBox='0 0 %d %d' role='img' aria-label='%s'>", svgW, svgH, html.EscapeString(c.Title))
	for v := 0.0; v <= top*1.0001; v += step {
		y := yOf(v)
		fmt.Fprintf(&sb, "<line class='grid' x1='%d' x2='%d' y1='%.1f' y2='%.1f'/>", padL, padL+plotW, y, y)
		fmt.Fprintf(&sb, "<text class='ytick' x='%d' y='%.1f'>%s</text>", padL-8, y+4, html.EscapeString(c.format(v)))
	}
	c.xAxis(&sb)

	if c.Kind == Stacked {
		for si := range c.Series {
			var lower []float64
			if si > 0 {
				lower = tops[si-1]
			}
			for _, seg := range segments(tops[si]) {
				var d strings.Builder
				for i := seg[0]; i < seg[1]; i++ {
					cmd := "L"
					if i == seg[0] {
						cmd = "M"
					}
					fmt.Fprintf(&d, "%s%.1f %.1f", cmd, c.xOf(c.Times[i]), yOf(tops[si][i]))
				}
				for i := seg[1] - 1; i >= seg[0]; i-- {
					lo := 0.0
					if lower != nil && !math.IsNaN(lower[i]) {
						lo = lower[i]
					}
					fmt.Fprintf(&d, "L%.1f %.1f", c.xOf(c.Times[i]), yOf(lo))
				}
				fmt.Fprintf(&sb, "<path class='area' d='%sZ' fill='%s'/>", d.String(), c.color(c.Series[si].Name))
			}
		}
	}
	for si := range c.Series {
		var d strings.Builder
		for _, seg := range segments(tops[si]) {
			for i := seg[0]; i < seg[1]; i++ {
				cmd := "L"
				if i == seg[0] {
					cmd = "M"
				}
				fmt.Fprintf(&d, "%s%.1f %.1f", cmd, c.xOf(c.Times[i]), yOf(tops[si][i]))
			}
			if seg[1]-seg[0] == 1 {
				fmt.Fprintf(&sb, "<circle cx='%.1f' cy='%.1f' r='2.5' fill='%s'/>",
					c.xOf(c.Times[seg[0]]), yOf(tops[si][seg[0]]), c.color(c.Series[si].Name))
			}
		}
		if d.Len() > 0 {
			fmt.Fprintf(&sb, "<path class='line' d='%s' stroke='%s'/>", d.String(), c.color(c.Series[si].Name))
		}
	}
	fmt.Fprintf(&sb, "<line class='cursor' x1='0' x2='0' y1='%d' y2='%d'/>", padT, padT+plotH)
	sb.WriteString("</svg><div class='tip' hidden></div></div>")
	c.legend(&sb)
	c.data(&sb)
	sb.WriteString("</figure>")
	return sb.String()
}

// segments returns the [start, end) runs of vals without NaN.
func segments(vals []float64) [][2]int {
	var out [][2]int
	start := -1
	for i, v := range vals {
		if math.IsNaN(v) {
			if start >= 0 {
				out = append(out, [2]int{start, i})
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, [2]int{start, len(vals)})
	}
	return out
}

func (c *Chart) xAxis(sb *strings.Builder) {
	span := c.To.Sub(c.From)
	steps := []time.Duration{time.Hour, 2 * time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour,
		24 * time.Hour, 48 * time.Hour, 7 * 24 * time.Hour, 14 * 24 * time.Hour, 28 * 24 * time.Hour,
		56 * 24 * time.Hour}
	step := steps[len(steps)-1]
	for _, s := range steps {
		if span/s <= maxXTicks {
			step = s
			break
		}
	}
	if step >= 28*24*time.Hour {
		c.monthTicks(sb)
		return
	}
	layout := dateFmt
	if step < 24*time.Hour {
		layout = timeFmt
	}
	t := c.From.Truncate(step)
	if step >= 24*time.Hour {
		t = time.Date(c.From.Year(), c.From.Month(), c.From.Day(), 0, 0, 0, 0, time.UTC)
	}
	for ; !t.After(c.To); t = t.Add(step) {
		if t.Before(c.From) {
			continue
		}
		x := c.xOf(t)
		label := t.Format(layout)
		if layout == timeFmt && t.Hour() == 0 {
			label = t.Format(dateFmt)
		}
		fmt.Fprintf(sb, "<line class='xgrid' x1='%.1f' x2='%.1f' y1='%d' y2='%d'/>", x, x, padT, padT+plotH)
		fmt.Fprintf(sb, "<text class='xtick' x='%.1f' y='%d'>%s</text>", x, svgH-8, label)
	}
	fmt.Fprintf(sb, "<line class='axis' x1='%d' x2='%d' y1='%d' y2='%d'/>", padL, padL+plotW, padT+plotH, padT+plotH)
}

func (c *Chart) monthTicks(sb *strings.Builder) {
	months := int(c.To.Sub(c.From)/(30*24*time.Hour)) + 1
	every := (months + maxXTicks - 1) / maxXTicks
	for t := time.Date(c.From.Year(), c.From.Month(), 1, 0, 0, 0, 0, time.UTC); !t.After(c.To); t = t.AddDate(0, every, 0) {
		if t.Before(c.From) {
			continue
		}
		label := t.Format("Jan")
		if t.Month() == time.January {
			label = t.Format("Jan 2006")
		}
		x := c.xOf(t)
		fmt.Fprintf(sb, "<line class='xgrid' x1='%.1f' x2='%.1f' y1='%d' y2='%d'/>", x, x, padT, padT+plotH)
		fmt.Fprintf(sb, "<text class='xtick' x='%.1f' y='%d'>%s</text>", x, svgH-8, label)
	}
	fmt.Fprintf(sb, "<line class='axis' x1='%d' x2='%d' y1='%d' y2='%d'/>", padL, padL+plotW, padT+plotH, padT+plotH)
}

func (c *Chart) legend(sb *strings.Builder) {
	if len(c.Legend) > 0 {
		c.legendTable(sb)
		return
	}
	sb.WriteString("<ul class='legend'>")
	for i := range c.Series {
		s := c.Series[i]
		if c.Kind == Stacked {
			s = c.Series[len(c.Series)-1-i]
		}
		latest := ""
		for j := len(s.Vals) - 1; j >= 0; j-- {
			if !math.IsNaN(s.Vals[j]) {
				latest = c.format(s.Vals[j])
				break
			}
		}
		title := ""
		if s.Title != "" {
			title = " title='" + html.EscapeString(s.Title) + "'"
		}
		fmt.Fprintf(sb, "<li%s><i style='background:%s'></i>%s <b>%s</b></li>",
			title, c.color(s.Name), html.EscapeString(s.Name), html.EscapeString(latest))
	}
	sb.WriteString("</ul>")
}

// data embeds what the hover readout needs: x positions, labels and the
// formatted value of every series at every point.
func (c *Chart) data(sb *strings.Builder) {
	type ser struct {
		N string   `json:"n"`
		C string   `json:"c"`
		V []string `json:"v"`
	}
	d := struct {
		X  []float64 `json:"x"`
		L  []string  `json:"l"`
		W  int       `json:"w"`
		S  []ser     `json:"s"`
		St bool      `json:"st"`
	}{W: svgW, St: c.Kind == Stacked}
	layout := dateTimeFmt
	if c.Dates {
		layout = dateFmt
	}
	for _, t := range c.Times {
		d.X = append(d.X, math.Round(c.xOf(t)*10)/10)
		d.L = append(d.L, t.UTC().Format(layout))
	}
	order := make([]int, len(c.Series))
	for i := range order {
		order[i] = i
		if c.Kind == Stacked {
			order[i] = len(c.Series) - 1 - i
		}
	}
	for _, si := range order {
		s := c.Series[si]
		vs := make([]string, len(c.Times))
		for i := range c.Times {
			if i < len(s.Vals) && !math.IsNaN(s.Vals[i]) {
				vs[i] = c.format(s.Vals[i])
			}
		}
		d.S = append(d.S, ser{N: s.Name, C: c.color(s.Name), V: vs})
	}
	if c.Kind == Stacked {
		tot := make([]string, len(c.Times))
		for i := range c.Times {
			sum, any := 0.0, false
			for _, s := range c.Series {
				if i < len(s.Vals) && !math.IsNaN(s.Vals[i]) {
					sum += s.Vals[i]
					any = true
				}
			}
			if any {
				tot[i] = c.format(sum)
			}
		}
		d.S = append([]ser{{N: "total", V: tot}}, d.S...)
	}
	b, err := json.Marshal(d)
	if err != nil {
		return
	}
	fmt.Fprintf(sb, "<script type='application/json'>%s</script>", strings.ReplaceAll(string(b), "</", "<\\/"))
}

func (c *Chart) latest(s Series) string {
	for j := len(s.Vals) - 1; j >= 0; j-- {
		if !math.IsNaN(s.Vals[j]) {
			return c.format(s.Vals[j])
		}
	}
	return ""
}

func (c *Chart) legendTable(sb *strings.Builder) {
	sb.WriteString("<div class='scroll'><table class='legend-t'><thead><tr><th></th>")
	for i, h := range c.Legend {
		cls := ""
		if i == len(c.Legend)-1 {
			cls = " class='num'"
		}
		fmt.Fprintf(sb, "<th%s>%s</th>", cls, html.EscapeString(h))
	}
	sb.WriteString("</tr></thead><tbody>")
	for i := range c.Series {
		s := c.Series[i]
		if c.Kind == Stacked {
			s = c.Series[len(c.Series)-1-i]
		}
		fmt.Fprintf(sb, "<tr><td><i style='background:%s'></i></td><td>%s</td>", c.color(s.Name), html.EscapeString(s.Name))
		for _, cell := range s.Cells {
			fmt.Fprintf(sb, "<td>%s</td>", html.EscapeString(cell))
		}
		fmt.Fprintf(sb, "<td class='num'>%s</td></tr>", html.EscapeString(c.latest(s)))
	}
	sb.WriteString("</tbody></table></div>")
}

// color gives each series a distinct color: its fixed one when it has one,
// otherwise the next unused palette color in series order.
func (c *Chart) color(name string) string {
	if c.colors == nil {
		c.colors = map[string]string{}
		used := map[string]bool{}
		for _, s := range c.Series {
			if col, ok := fixedColors[s.Name]; ok {
				c.colors[s.Name] = col
				used[col] = true
			}
		}
		next := 0
		for _, s := range c.Series {
			if _, ok := c.colors[s.Name]; ok {
				continue
			}
			col := palette[next%len(palette)]
			for tries := 0; used[col] && tries < len(palette); tries++ {
				next++
				col = palette[next%len(palette)]
			}
			c.colors[s.Name] = col
			used[col] = true
			next++
		}
	}
	if col, ok := c.colors[name]; ok {
		return col
	}
	return Color(name)
}
