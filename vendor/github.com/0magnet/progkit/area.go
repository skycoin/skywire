package progkit

import (
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/rivo/uniseg"
)

const tabWidth = 4

// Area is a multi-line text editor. Where the host can, a real textarea is
// laid over it, and what is typed there arrives here.
type Area struct {
	ID    string
	Style tcell.Style
	// OnChange is told after each edit.
	OnChange func()

	lines     [][]rune
	cy, cx    int // cursor line, and rune index in it
	top, left int // first line and column shown
	h         int
}

// SetText replaces the text, keeping the cursor where it still fits.
func (a *Area) SetText(s string) {
	a.lines = a.lines[:0]
	for _, l := range strings.Split(s, "\n") {
		a.lines = append(a.lines, []rune(l))
	}
	a.cy = min(a.cy, len(a.lines)-1)
	a.cx = min(a.cx, len(a.lines[a.cy]))
}

// Text is the whole text, lines joined by "\n".
func (a *Area) Text() string {
	a.ensure()
	var b strings.Builder
	for i, l := range a.lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(string(l))
	}
	return b.String()
}

// Cursor is the cursor's line and rune index in it, from zero.
func (a *Area) Cursor() (line, col int) { return a.cy, a.cx }

func (a *Area) ensure() {
	if len(a.lines) == 0 {
		a.lines = [][]rune{{}}
	}
}

// offset is the cursor as a rune index into Text.
func (a *Area) offset() int {
	n := a.cx
	for _, l := range a.lines[:a.cy] {
		n += len(l) + 1
	}
	return n
}

func (a *Area) setOffset(n int) {
	a.ensure()
	for a.cy = 0; a.cy < len(a.lines)-1 && n > len(a.lines[a.cy]); a.cy++ {
		n -= len(a.lines[a.cy]) + 1
	}
	a.cx = min(max(n, 0), len(a.lines[a.cy]))
}

// columns is the display width of l, with tabs to the next stop.
func columns(l []rune) int {
	w := 0
	for _, r := range l {
		if r == '\t' {
			w += tabWidth - w%tabWidth
		} else {
			w += uniseg.StringWidth(string(r))
		}
	}
	return w
}

// Draw draws the text in r, with the cursor when focused.
func (a *Area) Draw(f *Frame, r Rect, focused bool) {
	if r.Empty() {
		return
	}
	a.ensure()
	a.h = r.H
	Fill(f.Screen, r, a.Style)
	a.top = min(a.top, a.cy)
	a.top = max(a.top, a.cy-r.H+1)
	cw := columns(a.lines[a.cy][:a.cx])
	a.left = min(a.left, cw)
	a.left = max(a.left, cw-r.W+1)
	for i := 0; i < r.H && a.top+i < len(a.lines); i++ {
		a.drawLine(f.Screen, r, r.Y+i, a.lines[a.top+i])
	}
	if focused {
		f.Screen.ShowCursor(r.X+cw-a.left, r.Y+a.cy-a.top)
	}
	f.Place(a.ID, r, Element{Kind: "area", Value: a.Text(), Selected: a.offset(), Top: a.top, Focus: focused}, a.message)
}

func (a *Area) drawLine(sc tcell.Screen, r Rect, y int, l []rune) {
	col := 0
	for _, ch := range l {
		s, w := string(ch), uniseg.StringWidth(string(ch))
		if ch == '\t' {
			w = tabWidth - col%tabWidth
			s = strings.Repeat(" ", w)
		}
		if x := col - a.left; x >= 0 && x+w <= r.W {
			sc.PutStrStyled(r.X+x, y, s, a.Style)
		}
		col += w
		if col-a.left >= r.W {
			return
		}
	}
}

func (a *Area) message(m Msg) {
	switch m.Type {
	case "change":
		a.SetText(m.Value)
		a.setOffset(m.Index)
		a.changed()
	case "cursor":
		a.setOffset(m.Index)
	}
}

func (a *Area) changed() {
	if a.OnChange != nil {
		a.OnChange()
	}
}

// Key edits the text or moves the cursor, and reports whether it used the key.
func (a *Area) Key(ev *tcell.EventKey) bool {
	a.ensure()
	if t := Typed(ev); t != "" {
		a.insert(t)
		return true
	}
	page := max(a.h-1, 1)
	switch ev.Key() {
	case tcell.KeyEnter:
		a.insert("\n")
	case tcell.KeyTab:
		a.insert("\t")
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if a.cx == 0 && a.cy == 0 {
			return true
		}
		a.Key(tcell.NewEventKey(tcell.KeyLeft, "", 0))
		a.deleteAt()
	case tcell.KeyDelete:
		a.deleteAt()
	case tcell.KeyLeft:
		if a.cx > 0 {
			a.cx--
		} else if a.cy > 0 {
			a.cy--
			a.cx = len(a.lines[a.cy])
		}
	case tcell.KeyRight:
		if a.cx < len(a.lines[a.cy]) {
			a.cx++
		} else if a.cy < len(a.lines)-1 {
			a.cy, a.cx = a.cy+1, 0
		}
	case tcell.KeyUp:
		a.moveLines(-1)
	case tcell.KeyDown:
		a.moveLines(1)
	case tcell.KeyPgUp:
		a.moveLines(-page)
	case tcell.KeyPgDn:
		a.moveLines(page)
	case tcell.KeyHome:
		a.cx = 0
	case tcell.KeyEnd:
		a.cx = len(a.lines[a.cy])
	default:
		return false
	}
	return true
}

func (a *Area) moveLines(n int) {
	a.cy = min(max(a.cy+n, 0), len(a.lines)-1)
	a.cx = min(a.cx, len(a.lines[a.cy]))
}

// deleteAt removes the rune under the cursor, joining lines at a line end.
func (a *Area) deleteAt() {
	l := a.lines[a.cy]
	switch {
	case a.cx < len(l):
		a.lines[a.cy] = append(l[:a.cx:a.cx], l[a.cx+1:]...)
	case a.cy < len(a.lines)-1:
		a.lines[a.cy] = append(l[:a.cx:a.cx], a.lines[a.cy+1]...)
		a.lines = append(a.lines[:a.cy+1], a.lines[a.cy+2:]...)
	default:
		return
	}
	a.changed()
}

func (a *Area) insert(s string) {
	for _, r := range s {
		l := a.lines[a.cy]
		switch r {
		case '\r':
			continue
		case '\n':
			rest := append([]rune(nil), l[a.cx:]...)
			a.lines[a.cy] = l[:a.cx:a.cx]
			a.lines = append(a.lines[:a.cy+1], append([][]rune{rest}, a.lines[a.cy+1:]...)...)
			a.cy, a.cx = a.cy+1, 0
		default:
			a.lines[a.cy] = append(l[:a.cx:a.cx], append([]rune{r}, l[a.cx:]...)...)
			a.cx++
		}
	}
	a.changed()
}

// Paste inserts pasted text at the cursor.
func (a *Area) Paste(s string) {
	a.ensure()
	a.insert(s)
}
