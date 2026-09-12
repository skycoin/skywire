// Package backdrop matrix/backdrop/mask.go
//
// A mask varies the backdrop cell by cell, where Dim varies it all at once.
//
// The difference is between turning the rain down and making a shape out of
// it. Dim answers "how bright is the backdrop"; a mask answers "what is the
// backdrop doing *here*", which is what it takes to get a picture out of the
// rain rather than painted over it. Inside a shape the rain can fall brighter,
// or denser, or both; the glyphs are the rain's own either way, scrambling as
// they always do, so what the reader sees is weather arranging itself rather
// than a sticker.
package backdrop

import "github.com/gdamore/tcell/v3"

// Mask maps the backdrop's own intensity at one cell to the intensity to draw
// there. Both are 0-255, the scale the rain's palette is indexed by.
//
// n is 0 for a cell the backdrop left dark, and returning more than 0 for one
// of those lights it — with the glyph the rain already holds at that position,
// not one invented for the purpose. That is what makes a shape solid enough to
// read. Scaling alone cannot do it: the rain is mostly gaps, and a shape that
// only brightens the cells that happen to be lit is a shape the eye never
// finds, because the gaps outnumber them and the streams are vertical while
// the shape is not.
//
// Returning n unchanged is a no-op, and is what every cell outside a shape
// should do.
type Mask interface {
	// IntensityAt maps the intensity at x, y. n is the backdrop's own, 0 if
	// the cell is dark.
	IntensityAt(x, y, n int) int
}

// Fitter is the optional half of Mask: a mask that places itself against the
// grid it is used on. Frame calls Fit before each fill, with the size of the
// grid about to be filled.
//
// A mask addressed in absolute cells cannot answer "put this in the empty
// space to the right of the text" on its own: where that is depends on how
// wide the screen turned out to be and how many rows the text came to, and
// both are settled inside the compositor after the caller has handed its
// options over. Fit is where a mask that wants to center itself, right-align,
// or decline to appear at all on a small screen gets told what it is working
// with. A stencil at fixed coordinates has nothing to ask and need not
// implement it.
type Fitter interface {
	Mask

	// Fit is called with the grid size before each fill.
	Fit(cols, rows int)
}

// Stencil is a Mask cut from a block of text: a cell is inside the shape when
// the corresponding character is anything but a space.
//
// Rows are indexed from Y downward and X rightward, so the block can be placed
// anywhere on the grid. A row shorter than the widest is padded with outside,
// and a coordinate past the block is outside, so a caller need not make the
// block rectangular or clamp its own lookups.
//
// The zero value changes nothing, which is what a zero-valued mask should do.
type Stencil struct {
	// Rows is the shape. Space is outside, anything else is inside.
	Rows []string

	// X and Y are where the block's top-left corner sits on the grid.
	X, Y int

	// Inside and Outside scale the intensity, out of 256. Zero means 256 for
	// both — no change.
	Inside, Outside int

	// Floor is the least the intensity may be inside the shape, 0-255. It is
	// what fills the shape in: a cell the rain left dark is lit this far, so
	// the silhouette is solid instead of being only as solid as the streams
	// that happen to cross it.
	//
	// Keep it low. The shape wants to be legible as a shape while still
	// reading as rain, and the rain's own trails run to 200 — a floor near
	// that flattens them into a block of even color and loses the streams
	// entirely. Somewhere under a third of that leaves the trails clearly on
	// top of a dim field.
	//
	// Zero fills nothing, which is the scaling-only mask.
	Floor int
}

// IntensityAt implements Mask.
func (s *Stencil) IntensityAt(x, y, n int) int {
	if !s.Covers(x, y) {
		return clamp255(scale(n, s.Outside))
	}
	n = scale(n, s.Inside)
	if n < s.Floor {
		n = s.Floor
	}
	return clamp255(n)
}

// Covers reports whether the grid cell x, y falls on a non-space character of
// the block — whether the shape is there.
//
// Exported because a mask that wraps a Stencil needs the same answer for its
// own purposes: a Tinter has to know which cells are its shape before it can
// recolor them, and recomputing that from IntensityAt would mean inferring a
// boolean from an arithmetic result.
//
// Named Covers rather than Inside because Inside is already the field holding
// the scale applied within the shape.
func (s *Stencil) Covers(x, y int) bool {
	row := y - s.Y
	if row < 0 || row >= len(s.Rows) {
		return false
	}
	col := x - s.X
	line := s.Rows[row]
	if col < 0 || col >= len(line) {
		return false
	}
	return line[col] != ' '
}

// Size reports the block's extent in cells.
func (s *Stencil) Size() (cols, rows int) {
	for _, r := range s.Rows {
		if len(r) > cols {
			cols = len(r)
		}
	}
	return cols, len(s.Rows)
}

// scale applies a scale out of 256, treating zero as no change.
func scale(n, by int) int {
	if by == 0 || by == 256 {
		return n
	}
	if by < 0 {
		return 0
	}
	return n * by / 256
}

// clamp255 holds an intensity inside the palette.
func clamp255(n int) int {
	if n < 0 {
		return 0
	}
	if n > 255 {
		return 255
	}
	return n
}

// maskIntensity applies a possibly-nil mask.
func maskIntensity(m Mask, x, y, n int) int {
	if m == nil {
		return n
	}
	return clamp255(m.IntensityAt(x, y, n))
}

// fitMask tells a mask the grid size, when it is the kind that wants to know.
func fitMask(m Mask, cols, rows int) {
	if f, ok := m.(Fitter); ok {
		f.Fit(cols, rows)
	}
}

// Tinter is a Mask that also recolors the cells it covers.
//
// Intensity alone cannot separate a shape from the rain it is made of. The
// rain's palette is one ramp of green, so a masked shape can only ever be
// brighter or dimmer green among green — legible up close, and easy to lose at
// a glance, which is the whole problem with a silhouette drawn in the same ink
// as its background. A mask that can say "and this cell is blue" gets a shape
// the eye finds without looking for it.
//
// TintAt is handed the RGB the backdrop resolved for the cell — for the rain,
// its palette entry for the post-mask intensity — and returns what to draw. It
// is called only for cells that end up lit, so a mask need not care about the
// dark ones, and returning the channels unchanged is a no-op.
//
// Plain channels rather than a tcell.Color so that implementing this does not
// oblige a caller to depend on the terminal library. A mask is a statement
// about a shape; which library ends up painting it is the compositor's
// business, and skywire — the first consumer — would otherwise have promoted
// tcell from an indirect dependency to a direct one to write four lines of
// arithmetic.
//
// Optional, like Fitter: a mask that only varies brightness need not implement
// it.
type Tinter interface {
	Mask

	// TintAt returns the color to draw at x, y, given the one already resolved.
	TintAt(x, y int, r, g, b int32) (int32, int32, int32)
}

// tintAt applies a possibly-absent tint, converting at the boundary so the
// Tinter never sees a tcell type.
func tintAt(m Mask, x, y int, c tcell.Color) tcell.Color {
	t, ok := m.(Tinter)
	if !ok {
		return c
	}
	r, g, b := c.RGB()
	r, g, b = t.TintAt(x, y, r, g, b)
	return tcell.NewRGBColor(clampChan(r), clampChan(g), clampChan(b))
}

// clampChan holds a tint's channel inside a byte; a Tinter is caller-supplied
// and nothing stops it returning a number no color has.
func clampChan(v int32) int32 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

// Washer is a Mask that paints a background behind the cells it covers,
// whether or not the backdrop lit them.
//
// It is what a shape needs when the thing behind the text is too sparse to
// carry it. A mask that can only brighten or recolor is at the mercy of where
// the rain happens to be falling: the streams are vertical and mostly gaps, so
// a silhouette drawn only in lit cells comes out as vertical smears, and one
// filled in with invented glyphs stops being rain. A wash sidesteps both — the
// region is solid because the background is solid, and the rain stays exactly
// as sparse as it was, reading as a knockout through the color rather than as
// something added to it.
//
// WashAt returns the background for a cell and whether there is one at all;
// false leaves the terminal's own background showing, which is what every cell
// outside a shape wants. A cell with a wash is drawn even when the backdrop
// left it dark — as a space, so the wash is all there is to see — which is the
// property that makes the shape solid.
//
// Optional, like Fitter and Tinter.
type Washer interface {
	Mask

	// WashAt returns the background at x, y, and whether to paint one.
	WashAt(x, y int) (r, g, b int32, ok bool)
}

// washAt applies a possibly-absent wash, clamped like a tint.
func washAt(m Mask, x, y int) (tcell.Color, bool) {
	w, ok := m.(Washer)
	if !ok {
		return tcell.ColorDefault, false
	}
	r, g, b, on := w.WashAt(x, y)
	if !on {
		return tcell.ColorDefault, false
	}
	return tcell.NewRGBColor(clampChan(r), clampChan(g), clampChan(b)), true
}
