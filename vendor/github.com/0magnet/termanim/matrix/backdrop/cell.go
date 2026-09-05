package backdrop

import (
	"github.com/gdamore/tcell/v3"

	"github.com/0magnet/termanim/canvas"
	"github.com/0magnet/termanim/matrix"
)

// Cell is one cell of whatever is behind the text: a glyph, and the colors to
// draw it in.
//
// It is what lets a backdrop come from anywhere rather than only from the
// rain. The compositor used to work in matrix.Cell — a glyph, an intensity
// into a green ramp, and a flag for the highlighted leading glyph — which is
// the rain's own vocabulary and asks questions the other animations in this
// repository cannot answer. A plasma has no leading glyph and no position
// along a ramp of green. Resolving the color at the source and handing on a
// cell that says only what to draw is what gets both kinds to the same place.
//
// A Bg of ColorDefault leaves the terminal's own background showing, which is
// not the same as black and is what an unlit half of a half-block cell wants.
type Cell struct {
	Rune rune
	Fg   tcell.Color
	Bg   tcell.Color
	Bold bool
}

// Frame is a grid of cells to draw behind text — one frame of a backdrop.
//
// Unlit is not black. A cell nothing was drawn into is left alone, so a
// backdrop that does not cover the screen — the rain is mostly gaps — sits on
// whatever background the terminal has rather than on a black rectangle.
//
// It is meant to be filled and refilled rather than reallocated. An animated
// backdrop redraws many times a second and the grid is the largest thing in
// that loop.
type Frame struct {
	cols, rows int
	cells      []Cell
	lit        []bool
}

// NewFrame returns a cleared frame of the given size.
func NewFrame(cols, rows int) *Frame {
	f := &Frame{}
	f.Resize(cols, rows)
	return f
}

// Size reports the grid in cells.
func (f *Frame) Size() (cols, rows int) { return f.cols, f.rows }

// Resize sets the grid size and clears it, keeping the buffers when they are
// already big enough.
func (f *Frame) Resize(cols, rows int) {
	if cols < 0 {
		cols = 0
	}
	if rows < 0 {
		rows = 0
	}
	f.cols, f.rows = cols, rows
	n := cols * rows
	if n > cap(f.cells) {
		// A fresh buffer is already zeroed, so there is nothing to clear.
		f.cells = make([]Cell, n)
		f.lit = make([]bool, n)
		return
	}
	f.cells = f.cells[:n]
	f.lit = f.lit[:n]
	f.Clear()
}

// Clear unlights every cell.
func (f *Frame) Clear() {
	clear(f.lit)
}

// Set lights one cell. Coordinates outside the grid are dropped, so a source
// can be written without clamping at every call site.
func (f *Frame) Set(x, y int, c Cell) {
	if x < 0 || y < 0 || x >= f.cols || y >= f.rows {
		return
	}
	i := y*f.cols + x
	f.cells[i] = c
	f.lit[i] = true
}

// At returns the cell at x, y and whether anything was drawn there.
func (f *Frame) At(x, y int) (Cell, bool) {
	if x < 0 || y < 0 || x >= f.cols || y >= f.rows {
		return Cell{}, false
	}
	i := y*f.cols + x
	return f.cells[i], f.lit[i]
}

// FromMatrix fills f from the rain's current frame.
//
// dim scales the intensity before the palette is read rather than scaling the
// color that comes out of it. The two are different pictures: the ramp runs
// from a near-black green through to a white head and is not linear in any
// channel, so walking down it and multiplying its output disagree. Walking
// down it is the one that stays green.
//
// The palette is the matrix's own, so a caller that tuned it — Painter.Matrix
// exists for exactly that — sees the change behind its text as well as on a
// screen of its own.
func (f *Frame) FromMatrix(m *matrix.Matrix, dim int) {
	f.Clear()
	pal := m.Palette
	m.Cells(func(x, y int, c matrix.Cell) {
		n := c.Intensity
		if dim != 256 {
			n = n * dim / 256
		}
		if n < 0 {
			n = 0
		} else if n > 255 {
			n = 255
		}
		f.Set(x, y, Cell{Rune: c.Rune, Fg: pal[n], Bold: c.Hot})
	})
}

// FromSurface fills f from a pixel surface, two pixel rows to a cell row.
//
// This is what puts the pixel animations in this repository behind text. A
// terminal cell is about twice as tall as it is wide, so canvas draws each one
// as a half block carrying two independently colored pixels; that works as
// well under a help screen as it does on a screen of its own.
//
// Which block is used follows canvas's own flush, and for the same reason: a
// cell with one pixel lit is drawn as whichever block puts the color on the
// lit half and leaves the other to the terminal. Drawing it as an upper block
// with a default foreground instead paints that half in whatever color the
// terminal writes text in, which is a solid bar rather than emptiness — the
// fringe that used to run along the top of the fire.
//
// dim scales the colors themselves here. There is no ramp to walk down: a
// surface is already the picture.
func (f *Frame) FromSurface(s *canvas.Surface, dim int) {
	f.Clear()
	w, h := s.Size()
	cols, rows := f.cols, f.rows
	if w < cols {
		cols = w
	}
	if h/2 < rows {
		rows = h / 2
	}
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			t := dimColor(s.At(x, 2*y), dim)
			b := dimColor(s.At(x, 2*y+1), dim)
			switch {
			case t == tcell.ColorDefault && b == tcell.ColorDefault:
				// Nothing here. Left unlit, so the text's own row shows
				// through and the rain's gaps stay gaps.
			case t == tcell.ColorDefault:
				f.Set(x, y, Cell{Rune: canvas.LowerHalf, Fg: b})
			case b == tcell.ColorDefault:
				f.Set(x, y, Cell{Rune: canvas.UpperHalf, Fg: t})
			default:
				f.Set(x, y, Cell{Rune: canvas.UpperHalf, Fg: t, Bg: b})
			}
		}
	}
}

// dimColor scales a color towards black, out of 256. ColorDefault is left
// alone: it is the absence of a color and there is nothing to scale.
func dimColor(c tcell.Color, dim int) tcell.Color {
	if dim == 256 || c == tcell.ColorDefault {
		return c
	}
	if dim < 0 {
		dim = 0
	}
	r, g, b := c.RGB()
	return tcell.NewRGBColor(r*int32(dim)/256, g*int32(dim)/256, b*int32(dim)/256)
}
