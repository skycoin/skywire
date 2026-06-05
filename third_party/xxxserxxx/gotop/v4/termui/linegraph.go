package termui

import (
	"fmt"
	"image"
	"sort"
	"strconv"
	"unicode"

	ui "github.com/gizak/termui/v3"

	drawille "github.com/skycoin/skywire/third_party/xxxserxxx/gotop/v4/termui/drawille-go"
)

// LineGraph draws a graph like this ⣀⡠⠤⠔⣁ of data points.
type LineGraph struct {
	*ui.Block

	// Data is a size-managed data set for the graph. Each entry is a line;
	// each sub-array are points in the line. The maximum size of the
	// sub-arrays is controlled by the size of the canvas. This
	// array is **not** thread-safe. Do not modify this array, or it's
	// sub-arrays in threads different than the thread that calls `Draw()`
	Data map[string][]float64
	// The labels drawn on the graph for each of the lines; the key is shared
	// by Data; the value is the text that will be rendered.
	Labels map[string]string

	HorizontalScale int

	LineColors       map[string]ui.Color
	LabelStyles      map[string]ui.Modifier
	DefaultLineColor ui.Color

	seriesList numbered
}

func NewLineGraph() *LineGraph {
	return &LineGraph{
		Block: ui.NewBlock(),

		Data:   make(map[string][]float64),
		Labels: make(map[string]string),

		HorizontalScale: 5,

		LineColors:  make(map[string]ui.Color),
		LabelStyles: make(map[string]ui.Modifier),
	}
}

// Tint returns a lighter variant of a 256-color "color cube" color (palette
// indices 16-231) by blending each channel toward white by fraction t (0..1).
// t<=0 returns the base color; colors outside the cube are returned unchanged.
// Used to derive shades of a single base hue (e.g. light vs. dark green for
// memory used/buffers/cache) the way multiload-ng tints one color per subtype.
func Tint(base ui.Color, t float64) ui.Color {
	c := int(base)
	if c < 16 || c > 231 || t <= 0 {
		return base
	}
	if t > 1 {
		t = 1
	}
	c -= 16
	blend := func(v int) int {
		nv := int(float64(v) + (5.0-float64(v))*t + 0.5)
		if nv < 0 {
			nv = 0
		}
		if nv > 5 {
			nv = 5
		}
		return nv
	}
	r := blend(c / 36)
	g := blend((c % 36) / 6)
	b := blend(c % 6)
	return ui.Color(16 + 36*r + 6*g + b)
}

func (lg *LineGraph) Draw(buf *ui.Buffer) {
	lg.Block.Draw(buf)
	// we render each data point on to the canvas then copy over the braille to the buffer at the end
	// fyi braille characters have 2x4 dots for each character
	c := drawille.NewCanvas()
	// used to keep track of the braille colors until the end when we render the braille to the buffer
	colors := make([][]ui.Color, lg.Inner.Dx()+2)
	for i := range colors {
		colors[i] = make([]ui.Color, lg.Inner.Dy()+2)
	}

	if len(lg.seriesList) != len(lg.Data) {
		// sort the series so that overlapping data will overlap the same way each time
		lg.seriesList = make(numbered, len(lg.Data))
		i := 0
		for seriesName := range lg.Data {
			lg.seriesList[i] = seriesName
			i++
		}
		sort.Sort(lg.seriesList)
	}

	// draw lines in reverse order so that the first color defined in the colorscheme is on top
	for i := len(lg.seriesList) - 1; i >= 0; i-- {
		seriesName := lg.seriesList[i]
		seriesData := lg.Data[seriesName]
		seriesLineColor, ok := lg.LineColors[seriesName]
		if !ok {
			seriesLineColor = lg.DefaultLineColor
			lg.LineColors[seriesName] = seriesLineColor
		}

		// coordinates of last point
		lastY, lastX := -1, -1
		// assign colors to `colors` and lines/points to the canvas
		dx := lg.Inner.Dx()
		for i := len(seriesData) - 1; i >= 0; i-- {
			x := ((dx + 1) * 2) - 1 - (((len(seriesData) - 1) - i) * lg.HorizontalScale)
			y := ((lg.Inner.Dy() + 1) * 4) - 1 - int((float64((lg.Inner.Dy())*4)-1)*(seriesData[i]/100))
			if x < 0 {
				// render the line to the last point up to the wall
				if x > -lg.HorizontalScale {
					for _, p := range drawille.Line(lastX, lastY, x, y) {
						if p.X > 0 {
							c.Set(p.X, p.Y)
							colors[p.X/2][p.Y/4] = seriesLineColor
						}
					}
				}
				if len(seriesData) > 4*dx {
					lg.Data[seriesName] = seriesData[dx-1:]
				}
				break
			}
			if lastY == -1 { // if this is the first point
				c.Set(x, y)
				colors[x/2][y/4] = seriesLineColor
			} else {
				c.DrawLine(lastX, lastY, x, y)
				for _, p := range drawille.Line(lastX, lastY, x, y) {
					colors[p.X/2][p.Y/4] = seriesLineColor
				}
			}
			lastX, lastY = x, y
		}

		// copy braille and colors to buffer
		for y, line := range c.Rows(c.MinX(), c.MinY(), c.MaxX(), c.MaxY()) {
			for x, char := range line {
				x /= 3 // idk why but it works
				if x == 0 {
					continue
				}
				if char != 10240 { // empty braille character
					buf.SetCell(
						ui.NewCell(char, ui.NewStyle(colors[x][y])),
						image.Pt(lg.Inner.Min.X+x-1, lg.Inner.Min.Y+y-1),
					)
				}
			}
		}
	}

	lg.drawLabels(buf)
}

// drawLabels renders the per-series key + value text overlaid on the graph.
// Series names are left-justified to the widest name so the value column lines
// up instead of floating right after each variable-width name. Shared by the
// line and stacked render paths.
func (lg *LineGraph) drawLabels(buf *ui.Buffer) {
	nameWid := 0
	for _, seriesName := range lg.seriesList {
		if len(seriesName) > nameWid {
			nameWid = len(seriesName)
		}
	}
	maxWid := 0
	xoff := 0 // X offset for additional columns of text
	yoff := 0 // Y offset for resetting column to top of widget
	for i, seriesName := range lg.seriesList {
		if yoff+i+2 > lg.Inner.Dy() {
			xoff += maxWid + 2
			yoff = -i
			maxWid = 0
		}
		seriesLineColor, ok := lg.LineColors[seriesName]
		if !ok {
			seriesLineColor = lg.DefaultLineColor
		}
		seriesLabelStyle, ok := lg.LabelStyles[seriesName]
		if !ok {
			seriesLabelStyle = ui.ModifierClear
		}

		str := fmt.Sprintf("%-*s %s", nameWid, seriesName, lg.Labels[seriesName])
		// Mask the label's footprint (field width + a one-column right margin)
		// with blank cells first, so the braille graph doesn't bleed through the
		// gaps between/around the text. The graph still shows everywhere outside
		// this strip.
		fieldWid := len(str) + 1
		if fieldWid > maxWid {
			maxWid = fieldWid
		}
		for k := 0; k < fieldWid; k++ {
			buf.SetCell(
				ui.NewCell(' ', ui.NewStyle(ui.ColorClear)),
				image.Pt(xoff+lg.Inner.Min.X+2+k, yoff+lg.Inner.Min.Y+i+1),
			)
		}
		// then render the key + value text on the clean strip
		for k, char := range str {
			if char != ' ' {
				buf.SetCell(
					ui.NewCell(char, ui.NewStyle(seriesLineColor, ui.ColorClear, seriesLabelStyle)),
					image.Pt(xoff+lg.Inner.Min.X+2+k, yoff+lg.Inner.Min.Y+i+1),
				)
			}
		}

	}
}

// A string containing an integer
type numbered []string

func (n numbered) Len() int      { return len(n) }
func (n numbered) Swap(i, j int) { n[i], n[j] = n[j], n[i] }

func (n numbered) Less(i, j int) bool {
	ars := n[i]
	brs := n[j]
	// iterate over the runes in string A
	var ai int
	for ai = 0; ai < len(ars); ai++ {
		ar := ars[ai]
		// if brs has been equal to ars so far, but is shorter, than brs is less
		if ai >= len(brs) {
			return false
		}
		br := brs[ai]
		// handle numbers if we find them
		if unicode.IsDigit(rune(ar)) {
			// if ar is a digit and br isn't, then ars is less
			if !unicode.IsDigit(rune(brs[ai])) {
				return true
			}
			// now get the range for the full numbers, which is the sequence of consecutive digits from each
			ae := ai + 1
			be := ae
			for ; ae < len(ars) && unicode.IsDigit(rune(ars[ae])); ae++ {
			}
			for ; be < len(brs) && unicode.IsDigit(rune(brs[be])); be++ {
			}
			if be < ae {
				return false
			}
			adigs, err := strconv.Atoi(ars[ai:ae])
			if err != nil {
				return true
			}
			bdigs, err := strconv.Atoi(brs[ai:be])
			if err != nil {
				return true
			}
			// The numbers are equal; continue with the rest of the string
			if adigs == bdigs {
				ai = ae - 1
				continue
			}
			return adigs < bdigs
		} else if unicode.IsDigit(rune(br)) {
			return false
		}
		if ar == br {
			continue
		}
		// No digits, so compare letters
		if ar < br {
			return true
		}
		return false
	}
	return ai <= len(brs)
}
