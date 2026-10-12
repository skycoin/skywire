package progkit

import (
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/rivo/uniseg"
)

// Span is text in one style.
type Span struct {
	Text  string
	Style tcell.Style
}

// Line is a row of spans.
type Line []Span

// Width is how many cells l takes.
func (l Line) Width() int {
	n := 0
	for _, s := range l {
		n += uniseg.StringWidth(s.Text)
	}
	return n
}

// Plain is the text of l without its styles.
func (l Line) Plain() string {
	var b strings.Builder
	for _, s := range l {
		b.WriteString(s.Text)
	}
	return b.String()
}

// DrawText writes s at x, y in style, clipped to w cells, and returns the
// cells it used.
func DrawText(sc tcell.Screen, x, y, w int, s string, style tcell.Style) int {
	return DrawLine(sc, x, y, w, Line{{s, style}})
}

// DrawLine writes l at x, y, clipped to w cells, and returns the cells it
// used. Wide characters that would cross the edge are left out.
func DrawLine(sc tcell.Screen, x, y, w int, l Line) int {
	used := 0
	for _, sp := range l {
		g := uniseg.NewGraphemes(sp.Text)
		for g.Next() {
			cw := g.Width()
			if cw == 0 {
				continue
			}
			if used+cw > w {
				return used
			}
			sc.Put(x+used, y, g.Str(), sp.Style)
			used += cw
		}
	}
	return used
}

// Fill paints r with spaces in style.
func Fill(sc tcell.Screen, r Rect, style tcell.Style) {
	sc.FillArea(r.X, r.Y, r.W, r.H, ' ', style)
}

// Box draws a single-line border around r with title on its top edge, and
// returns the inside.
func Box(sc tcell.Screen, r Rect, title string, style tcell.Style) Rect {
	if r.W < 2 || r.H < 2 {
		return Rect{}
	}
	right, bottom := r.X+r.W-1, r.Y+r.H-1
	for x := r.X + 1; x < right; x++ {
		sc.Put(x, r.Y, "─", style)
		sc.Put(x, bottom, "─", style)
	}
	for y := r.Y + 1; y < bottom; y++ {
		sc.Put(r.X, y, "│", style)
		sc.Put(right, y, "│", style)
	}
	sc.Put(r.X, r.Y, "┌", style)
	sc.Put(right, r.Y, "┐", style)
	sc.Put(r.X, bottom, "└", style)
	sc.Put(right, bottom, "┘", style)
	if title != "" && r.W > 4 {
		DrawText(sc, r.X+2, r.Y, r.W-4, " "+title+" ", style)
	}
	return r.Inset(1)
}
