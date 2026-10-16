//go:build cgo && unix

package gioterm

import (
	"fmt"
	"image"
	"image/color"
	"path/filepath"

	"github.com/0magnet/progterm/glterm"
)

// tab is one program in the window and the terminal it draws in. Each frame
// draws into the other of two buffers, so the one Gio still reads is left alone.
type tab struct {
	term  *glterm.Terminal
	title string
	bufs  [2]*image.RGBA
	frame int
}

// tabs are the window's terminals. cur is the one shown and given input.
type tabs struct {
	list []*tab
	cur  int
}

func (ts *tabs) active() *tab {
	if len(ts.list) == 0 {
		return nil
	}
	return ts.list[ts.cur]
}

// add appends t, in front when front is set and behind the current tab
// otherwise.
func (ts *tabs) add(t *tab, front bool) {
	ts.list = append(ts.list, t)
	if front {
		ts.cur = len(ts.list) - 1
	}
}

// remove drops tab i. The current tab stays current; if it is the one
// removed, the tab after it takes its place, or the one before at the end.
func (ts *tabs) remove(i int) {
	ts.list = append(ts.list[:i], ts.list[i+1:]...)
	if i < ts.cur || ts.cur >= len(ts.list) {
		ts.cur = max(0, ts.cur-1)
	}
}

// step moves the current tab by d, wrapping around.
func (ts *tabs) step(d int) {
	if n := len(ts.list); n > 0 {
		ts.cur = ((ts.cur+d)%n + n) % n
	}
}

// pick makes tab i current, if there is one.
func (ts *tabs) pick(i int) {
	if i >= 0 && i < len(ts.list) {
		ts.cur = i
	}
}

func (ts *tabs) labels() []string {
	l := make([]string, len(ts.list))
	for i, t := range ts.list {
		l[i] = fmt.Sprintf(" %d %s ", i+1, t.title)
	}
	return l
}

// strip is where the tab strip puts each tab's label and the new tab button.
type strip struct {
	tabs []image.Rectangle
	plus image.Rectangle
}

// maxLabel is the widest a label gets, in cells.
const maxLabel = 24

// layoutStrip places labels left to right in a strip width by height pixels,
// cellW to a cell, with the new tab button after them. Labels that do not fit
// share the width.
func layoutStrip(labels []string, cellW, width, height int) strip {
	var s strip
	plusW := 3 * cellW
	cells := make([]int, len(labels))
	total := 0
	for i, l := range labels {
		cells[i] = min(len([]rune(l)), maxLabel)
		total += cells[i]
	}
	room := max(0, (width-plusW)/cellW)
	x := 0
	for i := range labels {
		w := cells[i]
		if total > room {
			w = max(1, room/len(labels))
		}
		s.tabs = append(s.tabs, image.Rect(x, 0, x+w*cellW, height))
		x += w * cellW
	}
	s.plus = image.Rect(x, 0, x+plusW, height)
	return s
}

// hit is the tab at x, or -1, and whether x is on the new tab button.
func (s strip) hit(x int) (int, bool) {
	for i, r := range s.tabs {
		if x >= r.Min.X && x < r.Max.X {
			return i, false
		}
	}
	return -1, x >= s.plus.Min.X && x < s.plus.Max.X
}

// The strip's colors beside the terminal's own.
var (
	stripBG    = color.RGBA{0x1c, 0x1c, 0x1c, 0xff}
	inactiveBG = color.RGBA{0x2e, 0x2e, 0x2e, 0xff}
	inactiveFG = color.RGBA{0x90, 0x90, 0x90, 0xff}
)

// drawStrip paints the tab strip into dst, the current tab in the terminal's
// own colors so it reads as joined to the terminal below.
func drawStrip(dst *image.RGBA, g *glterm.Grid, ts *tabs, s strip) {
	fg, bg := g.Colors()
	g.Label(dst, dst.Bounds(), "", fg, stripBG)
	pad := (dst.Bounds().Dy() - g.CellH) / 2
	for i, l := range ts.labels() {
		r := s.tabs[i]
		lf, lb := inactiveFG, inactiveBG
		if i == ts.cur {
			lf, lb = fg, bg
		}
		g.Label(dst, r, "", lf, lb)
		g.Label(dst, image.Rect(r.Min.X, r.Min.Y+pad, r.Max.X-1, r.Max.Y), l, lf, lb)
	}
	g.Label(dst, image.Rect(s.plus.Min.X, s.plus.Min.Y+pad, s.plus.Max.X, s.plus.Max.Y), " + ", inactiveFG, stripBG)
}

// titleOf is a tab's title: the one asked for, or the program's name.
func titleOf(t glterm.Tab, shell string) string {
	if t.Title != "" {
		return t.Title
	}
	if shell == "" {
		return "shell"
	}
	return filepath.Base(shell)
}
