package glterm

import (
	"image"
	"image/color"
	"image/draw"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// MenuItem is one entry in the context menu.
type MenuItem struct {
	Label, Shortcut string
	Enabled         bool
	Do              func()
}

// Menu is the context menu: the terminal's own, drawn over its cells, so a
// phone gets the same one a desktop does.
type Menu struct {
	Items []MenuItem
	Open  bool
	hover int
	rect  image.Rectangle // where it is drawn, in pixels
	rowH  int
}

var (
	menuBG     = color.RGBA{0x24, 0x24, 0x2a, 0xff}
	menuBorder = color.RGBA{0x55, 0x55, 0x60, 0xff}
	menuHover  = color.RGBA{0x3a, 0x7b, 0xd5, 0xff}
	menuFG     = color.RGBA{0xe0, 0xe0, 0xe0, 0xff}
	menuDim    = color.RGBA{0x80, 0x80, 0x80, 0xff}
)

// Show opens the menu at (x, y), kept inside bounds.
func (m *Menu) Show(g *Grid, x, y int, bounds image.Rectangle, items []MenuItem) {
	m.Items, m.Open, m.hover = items, true, -1
	m.rowH = g.CellH * 3 / 2
	w := 0
	for _, it := range items {
		w = max(w, len([]rune(it.Label))+len([]rune(it.Shortcut))+4)
	}
	r := image.Rect(0, 0, w*g.CellW, len(items)*m.rowH+g.CellH/2)
	r = r.Add(image.Pt(x, y))
	if r.Max.X > bounds.Max.X {
		r = r.Sub(image.Pt(r.Max.X-bounds.Max.X, 0))
	}
	if r.Max.Y > bounds.Max.Y {
		r = r.Sub(image.Pt(0, r.Max.Y-bounds.Max.Y))
	}
	m.rect = r.Intersect(bounds)
}

// Close closes the menu.
func (m *Menu) Close() { m.Open = false }

// itemAt is the enabled item under (x, y), or -1.
func (m *Menu) itemAt(x, y int) int {
	if !m.Open || !image.Pt(x, y).In(m.rect) {
		return -1
	}
	i := (y - m.rect.Min.Y - m.rowH/4) / m.rowH
	if i < 0 || i >= len(m.Items) || !m.Items[i].Enabled {
		return -1
	}
	return i
}

// Hover marks the item under (x, y), and reports whether that changed.
func (m *Menu) Hover(x, y int) bool {
	i := m.itemAt(x, y)
	if i == m.hover {
		return false
	}
	m.hover = i
	return true
}

// Click runs the item under (x, y) and closes the menu. It reports whether
// (x, y) was on the menu at all: a click elsewhere only closes it.
func (m *Menu) Click(x, y int) bool {
	on := image.Pt(x, y).In(m.rect)
	i := m.itemAt(x, y)
	m.Close()
	if i >= 0 && m.Items[i].Do != nil {
		m.Items[i].Do()
	}
	return on
}

// Draw paints the menu over dst when it is open.
func (m *Menu) Draw(g *Grid, dst *image.RGBA) {
	if !m.Open {
		return
	}
	draw.Draw(dst, m.rect, &image.Uniform{menuBorder}, image.Point{}, draw.Src)
	draw.Draw(dst, m.rect.Inset(1), &image.Uniform{menuBG}, image.Point{}, draw.Src)
	for i, it := range m.Items {
		row := image.Rect(m.rect.Min.X+1, m.rect.Min.Y+m.rowH/4+i*m.rowH, m.rect.Max.X-1, m.rect.Min.Y+m.rowH/4+(i+1)*m.rowH)
		fg := menuFG
		if !it.Enabled {
			fg = menuDim
		}
		if i == m.hover {
			draw.Draw(dst, row, &image.Uniform{menuHover}, image.Point{}, draw.Src)
		}
		base := row.Min.Y + (m.rowH-g.CellH)/2 + g.Ascent
		d := font.Drawer{Dst: dst, Src: &image.Uniform{fg}, Face: g.faces[0], Dot: fixed.P(row.Min.X+g.CellW, base)}
		d.DrawString(it.Label)
		if it.Shortcut != "" {
			d.Src = &image.Uniform{menuDim}
			if i == m.hover {
				d.Src = &image.Uniform{menuFG}
			}
			d.Dot = fixed.P(row.Max.X-(len([]rune(it.Shortcut))+1)*g.CellW, base)
			d.DrawString(it.Shortcut)
		}
	}
}
