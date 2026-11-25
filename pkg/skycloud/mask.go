// Package skycloud pkg/skycloud/mask.go c0-com-util
package skycloud

import (
	"image"
	"slices"

	"github.com/0magnet/termanim/matrix/backdrop"
)

// Floor is the help screen's fill inside the cloud, 0-255. It sits clearly above
// a trail cell so the one cell slashes read as the logo and not as gaps.
const Floor = 95

// Margin is the columns Fit keeps clear at each side of the default box.
const Margin = 2

// rowRoom is the rows left over after the cloud, so it never fills the box.
const rowRoom = 2

// Mask cuts the cloud into a backdrop. Inside it the rain is lit to Floor with
// the glyphs it holds and tinted Color. It is a backdrop.Fitter and Tinter.
type Mask struct {
	// Box is where Fit may place the cloud on a cols by rows screen. Nil is the
	// whole screen less Margin at each side.
	Box func(cols, rows int) image.Rectangle
	// Floor is the least intensity inside the cloud, 0-255.
	Floor int
	// Color is the tint at full brightness.
	Color [3]int32
	// Bright takes a cell's level from its brightest channel rather than its
	// luma. Luma leaves the blue too dark to find in a small picture.
	Bright bool
	// Outside scales the rain outside the cloud, out of 256. Zero leaves it.
	Outside int

	st    backdrop.Stencil
	on    bool
	fixed bool
}

// New is a mask with the help screen's floor and Skycoin blue.
func New() *Mask { return &Mask{Floor: Floor, Color: Blue} }

// Place pins the cloud to rows at x, y, and Fit leaves it there. It is for a
// stencil of a size the generated shapes do not have, from Sample.
func (m *Mask) Place(rows []string, x, y int) {
	m.st = backdrop.Stencil{Rows: rows, X: x, Y: y, Floor: m.Floor}
	m.on, m.fixed = true, true
}

// Fit places the largest generated shape that fits the box, centered in it.
// Nothing is placed when none fits, and every cell is then left as it was.
func (m *Mask) Fit(cols, rows int) {
	if m.fixed {
		return
	}
	m.on = false
	box := image.Rect(Margin, 0, cols-Margin, rows)
	if m.Box != nil {
		box = m.Box(cols, rows)
	}
	for _, shape := range slices.Backward(shapes) {
		st := backdrop.Stencil{Rows: shape, Floor: m.Floor}
		w, h := st.Size()
		if w > box.Dx() || h+rowRoom > box.Dy() {
			continue
		}
		st.X = box.Min.X + (box.Dx()-w)/2
		st.Y = box.Min.Y + (box.Dy()-h)/2
		m.st, m.on = st, true
		return
	}
}

// Placed reports the cloud's stencil and whether one is placed.
func (m *Mask) Placed() (backdrop.Stencil, bool) { return m.st, m.on }

// Covers reports whether x, y is inside the placed cloud.
func (m *Mask) Covers(x, y int) bool { return m.on && m.st.Covers(x, y) }

// IntensityAt implements backdrop.Mask.
func (m *Mask) IntensityAt(x, y, n int) int {
	if !m.on {
		return n
	}
	return m.st.IntensityAt(x, y, n)
}

// TintAt implements backdrop.Tinter. The cell keeps its brightness and takes
// Color, so trails still fade and hot heads still flare inside the cloud.
func (m *Mask) TintAt(x, y int, r, g, b int32) (int32, int32, int32) {
	if !m.Covers(x, y) {
		if m.Outside > 0 && m.Outside != 256 {
			o := int32(min(m.Outside, 256))
			return r * o / 256, g * o / 256, b * o / 256
		}
		return r, g, b
	}
	lvl := (r*30 + g*59 + b*11) / 100
	if m.Bright {
		lvl = max(r, g, b)
	}
	return m.Color[0] * lvl / 255, m.Color[1] * lvl / 255, m.Color[2] * lvl / 255
}

// Shapes is the generated silhouettes, smallest first, as copies.
func Shapes() [][]string {
	out := make([][]string, len(shapes))
	for i, s := range shapes {
		out[i] = slices.Clone(s)
	}
	return out
}
