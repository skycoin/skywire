//go:build unix

package glterm

import (
	"fmt"
	"image"
	"image/color"
	"path/filepath"
)

// Shown is one program in a window and the terminal it draws in.
type Shown struct {
	Term  *Terminal
	Title string
}

// Tabs are a window's terminals. Cur is the one shown and given input.
type Tabs struct {
	List []*Shown
	Cur  int
}

// Active is the current tab, or nil when there are none.
func (ts *Tabs) Active() *Shown {
	if len(ts.List) == 0 {
		return nil
	}
	return ts.List[ts.Cur]
}

// Add appends t, in front when front is set and behind the current tab
// otherwise.
func (ts *Tabs) Add(t *Shown, front bool) {
	ts.List = append(ts.List, t)
	if front {
		ts.Cur = len(ts.List) - 1
	}
}

// Remove drops tab i. The current tab stays current; if it is the one
// removed, the tab after it takes its place, or the one before at the end.
func (ts *Tabs) Remove(i int) {
	ts.List = append(ts.List[:i], ts.List[i+1:]...)
	if i < ts.Cur || ts.Cur >= len(ts.List) {
		ts.Cur = max(0, ts.Cur-1)
	}
}

// Prune drops the tabs whose program has exited, and reports whether any did.
func (ts *Tabs) Prune() bool {
	gone := false
	for i := len(ts.List) - 1; i >= 0; i-- {
		select {
		case <-ts.List[i].Term.Sess.Done():
			ts.Remove(i)
			gone = true
		default:
		}
	}
	return gone
}

// Step moves the current tab by d, wrapping around.
func (ts *Tabs) Step(d int) {
	if n := len(ts.List); n > 0 {
		ts.Cur = ((ts.Cur+d)%n + n) % n
	}
}

// Pick makes tab i current, if there is one.
func (ts *Tabs) Pick(i int) {
	if i >= 0 && i < len(ts.List) {
		ts.Cur = i
	}
}

// Labels are the tabs' labels in the strip.
func (ts *Tabs) Labels() []string {
	l := make([]string, len(ts.List))
	for i, t := range ts.List {
		l[i] = fmt.Sprintf(" %d %s ", i+1, t.Title)
	}
	return l
}

// Resize lays every tab out in w by h pixels.
func (ts *Tabs) Resize(g *Grid, w, h int) {
	cols, rows := g.Size(w, h)
	for _, t := range ts.List {
		t.Term.Grid = g
		t.Term.Sess.Resize(cols, rows, w, h)
		t.Term.Resize(w, h)
		t.Term.Menu.Close()
	}
}

// CloseAll ends every tab's program.
func (ts *Tabs) CloseAll() {
	for _, t := range ts.List {
		t.Term.Sess.Close() //nolint:errcheck,gosec // the window is going
	}
}

// Shortcut runs a tab shortcut and reports whether e was one: Ctrl+Shift+T a
// new tab, Ctrl+Shift+W closes this one, Ctrl+(Shift+)Tab and Ctrl+PageDown or
// PageUp the next or previous, Alt+1 to Alt+9 a tab by number.
func (ts *Tabs) Shortcut(e KeyEvent, newTab func()) bool {
	ctrl, shift, alt := e.Mods&Ctrl != 0, e.Mods&Shift != 0, e.Mods&Alt != 0
	letter := e.Rune | 0x20
	switch {
	case ctrl && shift && e.Key == KeyNone && letter == 't':
		newTab()
	case ctrl && shift && e.Key == KeyNone && letter == 'w':
		if t := ts.Active(); t != nil {
			t.Term.Sess.Close() //nolint:errcheck,gosec // its tab goes when it exits
		}
	case ctrl && e.Key == KeyTab:
		ts.Step(map[bool]int{false: 1, true: -1}[shift])
	case ctrl && !shift && e.Key == KeyPageDown:
		ts.Step(1)
	case ctrl && !shift && e.Key == KeyPageUp:
		ts.Step(-1)
	case alt && !ctrl && e.Key == KeyNone && e.Rune >= '1' && e.Rune <= '9':
		ts.Pick(int(e.Rune - '1'))
	default:
		return false
	}
	return true
}

// StripPress acts on a press at x on the strip: a tab is shown, or closed
// with the middle button, and the new tab button opens one.
func (ts *Tabs) StripPress(s Strip, x int, middle bool, newTab func()) {
	i, plus := s.Hit(x)
	switch {
	case plus:
		newTab()
	case i >= 0 && middle:
		ts.List[i].Term.Sess.Close() //nolint:errcheck,gosec // its tab goes when it exits
	case i >= 0:
		ts.Pick(i)
	}
}

// OpenTab starts spec's program in a terminal w by h pixels of g, the
// Options' own program where spec names none. onChange is called whenever
// its screen may have changed, and when it exits.
func OpenTab(opt Options, spec Tab, g *Grid, w, h int, clip Clipboard, onChange func()) (*Shown, error) {
	shell, args := spec.Shell, spec.Args
	if shell == "" {
		shell = opt.Shell
		if args == nil {
			args = opt.Args
		}
	}
	cols, rows := g.Size(w, h)
	var s *Session
	var err error
	if spec.Run != nil {
		s, err = StartFunc(spec.Run, cols, rows)
	} else {
		s, err = Start(shell, args, cols, rows)
	}
	if err != nil {
		return nil, err
	}
	t := &Shown{Term: &Terminal{Sess: s, Grid: g, Clip: clip}, Title: TitleOf(spec, shell)}
	t.Term.Resize(w, h)
	s.OnChange = onChange
	go func() {
		<-s.Done()
		onChange()
	}()
	return t, nil
}

// Strip is where the tab strip puts each tab's label and the new tab button.
type Strip struct {
	Tabs []image.Rectangle
	Plus image.Rectangle
}

// maxLabel is the widest a label gets, in cells.
const maxLabel = 24

// StripHeight is the tab strip's height over cells of g.
func StripHeight(g *Grid) int { return g.CellH + g.CellH/3 }

// LayoutStrip places labels left to right in a strip width by height pixels,
// cellW to a cell, with the new tab button after them. Labels that do not fit
// share the width.
func LayoutStrip(labels []string, cellW, width, height int) Strip {
	var s Strip
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
		s.Tabs = append(s.Tabs, image.Rect(x, 0, x+w*cellW, height))
		x += w * cellW
	}
	s.Plus = image.Rect(x, 0, x+plusW, height)
	return s
}

// Hit is the tab at x, or -1, and whether x is on the new tab button.
func (s Strip) Hit(x int) (int, bool) {
	for i, r := range s.Tabs {
		if x >= r.Min.X && x < r.Max.X {
			return i, false
		}
	}
	return -1, x >= s.Plus.Min.X && x < s.Plus.Max.X
}

// The strip's colors beside the terminal's own.
var (
	stripBG    = color.RGBA{0x1c, 0x1c, 0x1c, 0xff}
	inactiveBG = color.RGBA{0x2e, 0x2e, 0x2e, 0xff}
	inactiveFG = color.RGBA{0x90, 0x90, 0x90, 0xff}
)

// DrawStrip paints the tab strip into dst, the current tab in the terminal's
// own colors so it reads as joined to the terminal below.
func DrawStrip(dst *image.RGBA, g *Grid, ts *Tabs, s Strip) {
	fg, bg := g.Colors()
	g.Label(dst, dst.Bounds(), "", fg, stripBG)
	pad := (dst.Bounds().Dy() - g.CellH) / 2
	for i, l := range ts.Labels() {
		r := s.Tabs[i]
		lf, lb := inactiveFG, inactiveBG
		if i == ts.Cur {
			lf, lb = fg, bg
		}
		g.Label(dst, r, "", lf, lb)
		g.Label(dst, image.Rect(r.Min.X, r.Min.Y+pad, r.Max.X-1, r.Max.Y), l, lf, lb)
	}
	g.Label(dst, image.Rect(s.Plus.Min.X, s.Plus.Min.Y+pad, s.Plus.Max.X, s.Plus.Max.Y), " + ", inactiveFG, stripBG)
}

// TitleOf is a tab's title: the one asked for, or the program's name.
func TitleOf(t Tab, shell string) string {
	if t.Title != "" {
		return t.Title
	}
	if shell == "" {
		return "shell"
	}
	return filepath.Base(shell)
}

// WindowSize is the size in pixels of a window cols by rows cells of g, and
// its tab strip. It is made smaller where screen, when known, has less room,
// and kept wider than it is tall.
func WindowSize(g *Grid, cols, rows int, screen image.Point) image.Point {
	stripH := StripHeight(g)
	if screen.X > 0 && screen.Y > 0 {
		cols = min(cols, max(20, screen.X*9/10/g.CellW))
		rows = min(rows, max(5, (screen.Y*85/100-stripH)/g.CellH))
	}
	for rows*g.CellH+stripH >= cols*g.CellW {
		if rows > 5 {
			rows--
		} else {
			cols++
		}
	}
	return image.Pt(cols*g.CellW, rows*g.CellH+stripH)
}
