package canvas

import "github.com/gdamore/tcell/v3"

// The Braille surface trades color for resolution, and it is a trade rather
// than an improvement on the half-block Surface.
//
// A Braille glyph, U+2800 through U+28FF, is a 2x4 grid of dots whose 256
// combinations are all encoded — so one cell carries eight subpixels instead of
// two, four times the vertical resolution and twice the horizontal, on the same
// terminal. What it does not carry is eight colors: a cell has exactly one
// foreground, so every lit dot in it is the same color, and an unlit dot is not
// a color at all but the absence of ink.
//
// That is the whole cost, and it decides which effects belong here. A maze, an
// ant's trail, a life board, a wireframe cube — anything whose subject is a
// shape rather than a field — is sharper drawn as dots and loses nothing,
// because it was one or two colors to begin with. Fire and plasma are the
// opposite: they are a color field sampled on a grid, and quartering the color
// resolution to quadruple the spatial resolution destroys the very thing being
// drawn. Neither surface replaces the other; Run takes an Animation and
// RunBraille takes a BrailleAnimation, and an effect picks the one that suits
// it.
//
// Braille also renders less reliably than the half blocks. The half-block
// glyphs are block elements, which any terminal font has; Braille is a separate
// block that a font may render at a different width or not at all. That is the
// second half of the trade and the reason the half-block Surface stays the
// default.

// The 256 Braille glyphs as strings, built once, indexed by dot pattern.
//
// This is the same allocation argument flush makes for the two half blocks —
// Screen.Put takes a string, and building one from a rune per cell per frame is
// what dominated the cost of drawing — except that there are 256 possible
// glyphs here rather than two, so the constants become a table. PutRune's map
// would also avoid the allocation, but a 256-entry array indexed by the byte
// already in hand is a bounds check where the map is a hash.
var brailleStr = func() [256]string {
	var t [256]string
	for i := range t {
		t[i] = string(BrailleBase + rune(i))
	}
	return t
}()

// BrailleBase is U+2800, the empty Braille pattern. Adding a dot mask to it
// gives the glyph for that mask; see BrailleRune.
const BrailleBase rune = 0x2800

// dotBit maps a subpixel within a cell to its bit in the dot mask, indexed by
// row*2+col with col in 0..1 and row in 0..3.
//
// This table exists because Braille dot numbering is not raster order and
// cannot be computed with a shift. Unicode inherited the numbering from
// six-dot Braille, where the cell was 2x3 and the dots ran down the left column
// then down the right: dot 1 at (0,0), dot 2 at (0,1), dot 3 at (0,2), dot 4 at
// (1,0), dot 5 at (1,1), dot 6 at (1,2). The fourth row was added afterwards
// and had to take the two bits left over, so dot 7 at (0,3) is 0x40 and dot 8
// at (1,3) is 0x80 — the bottom row is the two high bits, out of sequence with
// the three above it.
//
// Getting this wrong does not produce garbage. It produces a picture that is
// scrambled only within each cell, which at this size still looks like a
// picture, so it has to be tested exhaustively rather than looked at.
var dotBit = [8]byte{
	0x01, 0x08, // row 0: dot 1 left, dot 4 right
	0x02, 0x10, // row 1: dot 2, dot 5
	0x04, 0x20, // row 2: dot 3, dot 6
	0x40, 0x80, // row 3: dot 7, dot 8 — the leftovers
}

// BrailleRune returns the glyph for a dot mask, for a consumer that has to
// composite a Braille cell somewhere other than a screen. The bit layout is the
// one described on dotBit.
func BrailleRune(mask byte) rune { return BrailleBase + rune(mask) }

// BrailleSurface is a grid of 1-bit subpixels, w wide and h tall, where w is
// twice the terminal's column count and h is four times its row count. Color is
// per cell rather than per subpixel; see Color and SetColor.
//
// The dots are stored as the dot masks themselves, one byte per terminal cell,
// rather than as a bool per subpixel. That is eight times less memory, and it
// means flush has nothing to pack: the byte it needs to index the glyph table
// with is the byte already in the slice.
type BrailleSurface struct {
	w, h       int    // subpixels
	cols, rows int    // terminal cells
	dots       []byte // one dot mask per cell
	fg         []tcell.Color

	// Color is the foreground for cells the caller never colored. Its zero
	// value is ColorDefault, which draws in whatever the terminal writes text
	// in — the right answer for a monochrome effect, and unlike the half-block
	// surface it is safe here, because an unlit dot is absent rather than
	// painted. That was the bug the half blocks had at their edges: there, a
	// default foreground filled the empty half with solid text color.
	Color tcell.Color
}

// NewBrailleSurface returns a surface of w by h subpixels, cleared.
//
// Dimensions that are not a whole number of cells are rounded up, so the last
// cell of a row or column is a partial one rather than a subpixel with nowhere
// to go. Set would otherwise have to drop coordinates that are inside the
// surface it was asked for.
func NewBrailleSurface(w, h int) *BrailleSurface {
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	cols, rows := (w+1)/2, (h+3)/4
	s := &BrailleSurface{
		w: w, h: h,
		cols: cols, rows: rows,
		dots: make([]byte, cols*rows),
		fg:   make([]tcell.Color, cols*rows),
	}
	s.Clear()
	return s
}

// Size reports the surface in subpixels. The terminal has half as many columns
// and a quarter as many rows.
func (s *BrailleSurface) Size() (w, h int) { return s.w, s.h }

// Cells reports the surface in terminal cells, for an animation that has to
// reason about where the cell boundaries fall — anything that colors per cell
// has to know how wide a cell is.
func (s *BrailleSurface) Cells() (cols, rows int) { return s.cols, s.rows }

// Clear turns every dot off and returns every cell to Color. An animation that
// does not cover the screen then sits on the terminal's own background rather
// than on a black rectangle, which is what the half-block Clear buys too.
func (s *BrailleSurface) Clear() {
	for i := range s.dots {
		s.dots[i] = 0
	}
	for i := range s.fg {
		s.fg[i] = tcell.ColorDefault
	}
}

// Fill turns every dot on or off without touching the colors.
func (s *BrailleSurface) Fill(on bool) {
	var v byte
	if on {
		v = 0xff
	}
	for i := range s.dots {
		s.dots[i] = v
	}
}

// Set turns one subpixel on or off. Coordinates outside the surface are
// dropped, so animations can be written without clamping at every call site.
func (s *BrailleSurface) Set(x, y int, on bool) {
	if x < 0 || y < 0 || x >= s.w || y >= s.h {
		return
	}
	i := (y/4)*s.cols + x/2
	b := dotBit[(y%4)*2+x%2]
	if on {
		s.dots[i] |= b
	} else {
		s.dots[i] &^= b
	}
}

// At reports whether one subpixel is lit, or false if out of bounds.
func (s *BrailleSurface) At(x, y int) bool {
	if x < 0 || y < 0 || x >= s.w || y >= s.h {
		return false
	}
	return s.dots[(y/4)*s.cols+x/2]&dotBit[(y%4)*2+x%2] != 0
}

// SetColor colors the cell containing subpixel (x, y).
//
// Color is per cell because a Braille cell has exactly one foreground and there
// is no honest way around that. Per-cell rather than one fixed color for the
// whole surface because the fixed version is strictly less useful and no
// simpler to use: a caller that wants one color sets Color once and never comes
// here, while a caller that wants a gradient down a maze or a hot head on a
// trail can have it. The cost is that a cell holds the *last* color written to
// it, whichever of its eight dots that was — the alternative would be to blend
// the eight into an average, and an average of the caller's colors is a
// decision that belongs to the caller, not to the surface. Callers that care
// should write the color they want the cell to end up with last, which for the
// usual case of a bright thing drawn over a dim one is the natural drawing
// order anyway.
//
// Coordinates are subpixels, matching Set, so a caller never has to divide.
func (s *BrailleSurface) SetColor(x, y int, c tcell.Color) {
	if x < 0 || y < 0 || x >= s.w || y >= s.h {
		return
	}
	s.fg[(y/4)*s.cols+x/2] = c
}

// ColorAt returns the color of the cell containing subpixel (x, y), or
// ColorDefault if out of bounds or if the cell was never colored. It reports
// what was written and does not resolve ColorDefault to Color; flush does that.
func (s *BrailleSurface) ColorAt(x, y int) tcell.Color {
	if x < 0 || y < 0 || x >= s.w || y >= s.h {
		return tcell.ColorDefault
	}
	return s.fg[(y/4)*s.cols+x/2]
}

// Glyph returns the Braille character cell (cx, cy) would be drawn with, or the
// empty pattern U+2800 if out of bounds.
//
// This is the same accessor UpperHalf is on the half-block side: a consumer
// that has to composite a surface somewhere other than a screen needs the
// glyph, and BrailleRune alone is no use to it without a way to reach the dot
// mask. Note that flush draws a space rather than U+2800 for an empty cell, for
// the reason given there.
func (s *BrailleSurface) Glyph(cx, cy int) rune {
	if cx < 0 || cy < 0 || cx >= s.cols || cy >= s.rows {
		return BrailleRune(0)
	}
	return BrailleRune(s.dots[cy*s.cols+cx])
}

// Plot lights a subpixel and colors its cell, which is what almost every call
// site wants and would otherwise write as two calls with the same bounds check.
func (s *BrailleSurface) Plot(x, y int, c tcell.Color) {
	s.Set(x, y, true)
	s.SetColor(x, y, c)
}

// flush paints the surface onto the screen, one glyph per cell.
//
// It allocates nothing: the dot mask indexes a table of strings built at init,
// and Put takes the string.
func (s *BrailleSurface) flush(screen tcell.Screen) {
	for cy := 0; cy < s.rows; cy++ {
		row := cy * s.cols
		for cx := 0; cx < s.cols; cx++ {
			mask := s.dots[row+cx]
			if mask == 0 {
				// A space, not the empty Braille pattern. U+2800 is a printed
				// character with no dots in it, so a terminal may give it a
				// different width or a background of its own; a space is what
				// the half-block flush draws for an empty cell and is cheaper
				// for tcell to diff.
				screen.Put(cx, cy, blankStr, tcell.StyleDefault) //nolint:errcheck // no error is possible for one cell
				continue
			}
			c := s.fg[row+cx]
			if c == tcell.ColorDefault {
				c = s.Color
			}
			screen.Put(cx, cy, brailleStr[mask], //nolint:errcheck // as above
				tcell.StyleDefault.Foreground(c))
		}
	}
}

// BrailleAnimation is one effect drawn as dots. It mirrors Animation, and the
// split between Resize and Frame is there for the same reason: buffers are
// allocated on a size change so that a frame allocates nothing.
//
// Resize is given subpixels, not cells — twice the columns by four times the
// rows — so an animation is written in the units it draws in.
type BrailleAnimation interface {
	Resize(w, h int)
	// Frame draws one frame. dt is the seconds elapsed since the previous
	// frame, and every motion should be scaled by it; see Animation.Frame for
	// why elapsed time is passed rather than assumed.
	//
	// The surface is not cleared between frames. An effect that repaints every
	// subpixel does not want the second pass, and one that accumulates — a
	// trail, a decaying field — actively needs the previous frame; calling
	// Clear is the animation's decision.
	Frame(s *BrailleSurface, dt float64)
}

// RunBraille drives a Braille animation on the given screen, on the same loop,
// the same keys and the same resize handling as Run. Like Run it does not call
// Init or Fini: the screen belongs to the caller.
func RunBraille(screen tcell.Screen, a BrailleAnimation, opt Options) error {
	var surf *BrailleSurface
	return run(screen, opt,
		func(cols, rows int) {
			surf = NewBrailleSurface(cols*2, rows*4)
			a.Resize(cols*2, rows*4)
		},
		func(_, _ int, dt float64) {
			a.Frame(surf, dt)
			surf.flush(screen)
		})
}
