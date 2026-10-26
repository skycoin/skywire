// Package glterm is a terminal drawn without a browser: xterm-go's terminal
// core (vt) fed by a shell on a pty, its cells drawn with the Go fonts into an
// image a window puts on the screen (gioterm, on Gio). It is the host that
// runs where no page can, a phone's app among them, and it draws cells only: a
// progressive program finds no host to ask (PROTOCOL.md in websh) and draws
// as it does in any terminal.
package glterm

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"strconv"
	"unicode/utf8"

	xterm "github.com/0magnet/xterm-go"
	"github.com/0magnet/xterm-go/vt"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Grid draws a terminal's cells. It caches each glyph it draws, so a frame
// after the first is mostly copies.
type Grid struct {
	// CellW and CellH are a cell's size in pixels; Ascent is the baseline's
	// distance from a cell's top.
	CellW, CellH, Ascent int

	faces [4]font.Face // regular, bold, italic, bold italic
	// fallbackFace draws what faces lack, made the first time it is needed.
	px           float64
	fallbackFace font.Face
	fallbackDone bool
	glyphs       map[glyphKey]*image.Alpha
	palette      [256]color.RGBA
	fg, bg       color.RGBA
}

// The selection's colors.
var (
	selBG = color.RGBA{0x26, 0x4f, 0x78, 0xff}
	selFG = color.RGBA{0xff, 0xff, 0xff, 0xff}
)

type glyphKey struct {
	s    string
	face int
}

// NewGrid is a grid drawing Go Mono at px pixels per em.
func NewGrid(px float64) (*Grid, error) { return NewGridFont(GoMono(0), px) }

// NewGridFont is a grid drawing f at px pixels per em; f.Pt is not used.
func NewGridFont(f Font, px float64) (*Grid, error) {
	g := &Grid{glyphs: map[glyphKey]*image.Alpha{}, px: px}
	for i, sf := range f.styles {
		if sf == nil {
			return nil, errors.New("glterm: a font with no faces")
		}
		face, err := opentype.NewFace(sf, &opentype.FaceOptions{Size: px, DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			return nil, err
		}
		g.faces[i] = face
	}
	m := g.faces[0].Metrics()
	adv, _ := g.faces[0].GlyphAdvance('M')
	g.CellW = adv.Ceil()
	g.Ascent = m.Ascent.Ceil()
	g.CellH = g.Ascent + m.Descent.Ceil()
	if g.CellW < 1 || g.CellH < 1 {
		g.CellW, g.CellH = 1, 1
	}
	for i, hex := range xterm.BuildPalette(vt.Theme{}) {
		g.palette[i] = parseHex(hex)
	}
	g.fg = color.RGBA{0xd0, 0xd0, 0xd0, 0xff}
	g.bg = color.RGBA{0x00, 0x00, 0x00, 0xff}
	return g, nil
}

// Size is how many cells fit in w by h pixels, at least one of each.
func (g *Grid) Size(w, h int) (cols, rows int) {
	return max(1, w/g.CellW), max(1, h/g.CellH)
}

// Draw paints t's viewport into dst, with the cursor where t has it and the
// cells sel holds highlighted. sel may be nil.
func (g *Grid) Draw(t *vt.Terminal, dst *image.RGBA, sel *Selection) {
	draw.Draw(dst, dst.Bounds(), &image.Uniform{g.bg}, image.Point{}, draw.Src)
	buf := t.Buffer()
	cell := vt.NewCellData()
	cursorOn := !t.CoreService().IsCursorHidden && buf.YDisp == buf.YBase
	for row := 0; row < t.Rows(); row++ {
		i := buf.YDisp + row
		if i >= buf.Lines.Length() {
			break
		}
		line := buf.Lines.Get(i)
		if line == nil {
			continue
		}
		for col := 0; col < t.Cols() && col < line.Length; col++ {
			line.LoadCell(col, cell)
			w := cell.GetWidth()
			if w == 0 {
				continue // the right half of a wide character
			}
			cursor := cursorOn && row == buf.Y && col == buf.X
			g.drawCell(dst, col, row, w, cell, cursor, sel != nil && sel.Contains(t, i, col))
		}
	}
}

func (g *Grid) drawCell(dst *image.RGBA, col, row, width int, c *vt.CellData, cursor, selected bool) {
	fg, bg := g.colorOf(c.GetFgColorMode(), c.GetFgColor(), g.fg), g.colorOf(c.GetBgColorMode(), c.GetBgColor(), g.bg)
	if c.IsBold() && c.GetFgColorMode() == vt.AttrCMP16 && c.GetFgColor() < 8 {
		fg = g.palette[c.GetFgColor()+8] // bold brightens the first eight, as xterm does
	}
	if c.IsInverse() != cursor {
		fg, bg = bg, fg
	}
	if selected {
		fg, bg = selFG, selBG
	}
	if c.IsDim() {
		fg = color.RGBA{fg.R / 2, fg.G / 2, fg.B / 2, 0xff}
	}
	r := image.Rect(col*g.CellW, row*g.CellH, (col+width)*g.CellW, (row+1)*g.CellH)
	if bg != g.bg {
		draw.Draw(dst, r, &image.Uniform{bg}, image.Point{}, draw.Src)
	}
	if s := c.GetChars(); s != "" && s != " " && !c.IsInvisible() {
		face := 0
		if c.IsBold() {
			face |= 1
		}
		if c.IsItalic() {
			face |= 2
		}
		mask := g.glyph(s, face, width)
		draw.DrawMask(dst, r, &image.Uniform{fg}, image.Point{}, mask, image.Point{}, draw.Over)
	}
	if c.IsUnderline() {
		y := r.Min.Y + g.Ascent + 1
		draw.Draw(dst, image.Rect(r.Min.X, y, r.Max.X, y+1), &image.Uniform{fg}, image.Point{}, draw.Src)
	}
	if c.IsStrikethrough() {
		y := r.Min.Y + g.CellH/2
		draw.Draw(dst, image.Rect(r.Min.X, y, r.Max.X, y+1), &image.Uniform{fg}, image.Point{}, draw.Src)
	}
}

// glyph is s drawn once, as a mask the size of width cells.
func (g *Grid) glyph(s string, face, width int) *image.Alpha {
	key := glyphKey{s, face}
	if m, ok := g.glyphs[key]; ok {
		return m
	}
	m := image.NewAlpha(image.Rect(0, 0, width*g.CellW, g.CellH))
	if r := []rune(s); len(r) == 1 && r[0] >= 0x2800 && r[0] <= 0x28ff {
		g.braille(m, int(r[0]-0x2800))
		g.glyphs[key] = m
		return m
	}
	f := g.faceFor(s, face)
	if r, _ := utf8.DecodeRuneInString(s); f == g.faces[face] {
		if _, ok := f.GlyphAdvance(r); !ok {
			if k := kanaBitmap(r); k != nil {
				g.drawKana(m, k)
				g.glyphs[key] = m
				return m
			}
		}
	}
	d := font.Drawer{Dst: m, Src: image.Opaque, Face: f, Dot: fixed.P(0, g.Ascent)}
	d.DrawString(s)
	g.glyphs[key] = m
	return m
}

// colorOf is the color a cell names in mode, or def for the default.
func (g *Grid) colorOf(mode uint32, v int, def color.RGBA) color.RGBA {
	switch mode {
	case vt.AttrCMP16, vt.AttrCMP256:
		if v >= 0 && v < 256 {
			return g.palette[v]
		}
	case vt.AttrCMRGB:
		return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 0xff} //nolint:gosec // bytes of a 24-bit color
	}
	return def
}

// parseHex reads "#rrggbb".
func parseHex(s string) color.RGBA {
	if len(s) != 7 || s[0] != '#' {
		return color.RGBA{A: 0xff}
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return color.RGBA{A: 0xff}
	}
	return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 0xff} //nolint:gosec // bytes of a 24-bit color
}

// braillePos is where each of a braille cell's eight dots sits, as column and
// row of a 2 by 4 grid, in the order of their bits.
var braillePos = [8][2]int{{0, 0}, {0, 1}, {0, 2}, {1, 0}, {1, 1}, {1, 2}, {0, 3}, {1, 3}}

// braille draws the braille pattern with dots bits into m. The fonts have no
// braille, and the TUIs that use it (spinners, graphs) are the ones a person
// watches, so it is drawn rather than left as boxes.
func (g *Grid) braille(m *image.Alpha, bits int) {
	dot := max(1, g.CellW/4)
	for i, p := range braillePos {
		if bits&(1<<i) == 0 {
			continue
		}
		x := g.CellW*(1+2*p[0])/4 - dot/2
		y := g.CellH*(1+2*p[1])/8 - dot/2
		draw.Draw(m, image.Rect(x, y, x+dot, y+dot), image.Opaque, image.Point{}, draw.Src)
	}
}

// Colors are the grid's default foreground and background.
func (g *Grid) Colors() (fg, bg color.RGBA) { return g.fg, g.bg }

// Label paints s in one row of cells from r's top left, over bg and clipped
// to r. A tab's title is drawn with it.
func (g *Grid) Label(dst *image.RGBA, r image.Rectangle, s string, fg, bg color.RGBA) {
	draw.Draw(dst, r, &image.Uniform{bg}, image.Point{}, draw.Src)
	x := r.Min.X
	for _, c := range s {
		if x+g.CellW > r.Max.X {
			return
		}
		if c != ' ' {
			cell := image.Rect(x, r.Min.Y, x+g.CellW, r.Min.Y+g.CellH).Intersect(r)
			draw.DrawMask(dst, cell, &image.Uniform{fg}, image.Point{}, g.glyph(string(c), 0, 1), image.Point{}, draw.Over)
		}
		x += g.CellW
	}
}

// faceFor is face, or the fallback face where face has no glyph for s and the
// fallback does, so a character the font lacks is drawn rather than a box.
func (g *Grid) faceFor(s string, face int) font.Face {
	f := g.faces[face]
	r, _ := utf8.DecodeRuneInString(s)
	if _, ok := f.GlyphAdvance(r); ok || r < 0x80 {
		return f
	}
	if !g.fallbackDone {
		g.fallbackDone = true
		if fb := fallback(); fb != nil {
			g.fallbackFace, _ = opentype.NewFace(fb, &opentype.FaceOptions{Size: g.px, DPI: 72, Hinting: font.HintingFull}) //nolint:errcheck // nil keeps the box
		}
	}
	if g.fallbackFace != nil {
		if _, ok := g.fallbackFace.GlyphAdvance(r); ok {
			return g.fallbackFace
		}
	}
	return f
}
