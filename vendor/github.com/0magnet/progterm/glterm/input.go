//go:build unix

package glterm

import (
	"image"
	"strings"
	"time"

	"github.com/0magnet/xterm-go/vt"
)

// Clipboard is the system clipboard as the window has it: Set puts text on
// the clipboard (or the primary selection), Get fetches it and hands it to
// paste, maybe later and from another goroutine.
type Clipboard interface {
	Set(primary bool, text string)
	Get(primary bool, paste func(text string))
}

// Terminal is what the window draws and what its input acts on: the session,
// the selection the mouse makes, and the context menu.
type Terminal struct {
	Sess *Session
	Grid *Grid
	Clip Clipboard
	Sel  Selection
	Menu Menu

	size     image.Rectangle // the window, in pixels
	dragging bool
	lastDown time.Time
	lastCell Pos
	clicks   int
}

// clickGap is how close together presses must be to count as a double or
// triple click.
const clickGap = 400 * time.Millisecond

// Resize records the window's size in pixels.
func (t *Terminal) Resize(w, h int) { t.size = image.Rect(0, 0, w, h) }

// Draw paints the terminal and whatever is over it into dst.
func (t *Terminal) Draw(dst *image.RGBA) {
	t.Sess.Lock()
	t.Grid.Draw(t.Sess.Term, dst, &t.Sel)
	t.Sess.Unlock()
	t.Menu.Draw(t.Grid, dst)
}

// cell is the buffer position under pixel (x, y), and the viewport cell.
func (t *Terminal) cell(x, y float32) (Pos, int, int) {
	col := max(0, min(int(x)/t.Grid.CellW, t.Sess.Term.Cols()-1))
	row := max(0, min(int(y)/t.Grid.CellH, t.Sess.Term.Rows()-1))
	return Pos{t.Sess.Term.Buffer().YDisp + row, col}, col, row
}

// Mouse handles a mouse event, and reports whether the window needs redrawing.
func (t *Terminal) Mouse(e MouseEvent) bool {
	x, y := int(e.X), int(e.Y)
	if t.Menu.Open {
		switch {
		case e.Action == Move:
			return t.Menu.Hover(x, y)
		case e.Action == Press && e.Button == ButtonRight:
			t.openMenu(x, y)
			return true
		case e.Action == Press:
			t.Menu.Click(x, y)
			return true
		}
		return false
	}

	t.Sess.Lock()
	defer t.Sess.Unlock()
	term := t.Sess.Term
	// A program that asked for the mouse gets it; Shift takes it back, as in
	// xterm, so text can still be selected over a TUI.
	if term.MouseService().AreMouseEventsActive() && e.Mods&Shift == 0 {
		t.report(e)
		return false
	}

	pos, _, _ := t.cell(e.X, e.Y)
	switch {
	case e.Button == WheelUp || e.Button == WheelDown:
		t.wheel(e.Button == WheelUp)
		return true

	case e.Button == ButtonLeft && e.Action == Press:
		now := time.Now()
		if now.Sub(t.lastDown) < clickGap && pos == t.lastCell {
			t.clicks = t.clicks%3 + 1
		} else {
			t.clicks = 1
		}
		t.lastDown, t.lastCell = now, pos
		t.Sel.Start(pos, Unit(t.clicks-1))
		t.dragging = true
		return true

	case e.Action == Move && t.dragging:
		t.Sel.Extend(pos)
		return true

	case e.Button == ButtonLeft && e.Action == Release:
		t.dragging = false
		if text := t.Sel.Text(term); text != "" && t.Clip != nil {
			t.Clip.Set(true, text) // what is selected is the primary selection
		}
		return true

	case e.Button == ButtonMiddle && e.Action == Press:
		t.paste(true)

	case e.Button == ButtonRight && e.Action == Press:
		t.Sess.Unlock()
		t.openMenu(x, y)
		t.Sess.Lock()
		return true
	}
	return false
}

// report sends e to the program, in the mouse protocol it asked for. The
// session is locked.
func (t *Terminal) report(e MouseEvent) {
	_, col, row := t.cell(e.X, e.Y)
	me := &vt.MouseEvent{
		Col: col, Row: row, X: int(e.X), Y: int(e.Y),
		Ctrl: e.Mods&Ctrl != 0, Alt: e.Mods&Alt != 0, Shift: e.Mods&Shift != 0,
	}
	switch e.Button {
	case ButtonLeft:
		me.Button = vt.MouseButtonLeft
	case ButtonMiddle:
		me.Button = vt.MouseButtonMiddle
	case ButtonRight:
		me.Button = vt.MouseButtonRight
	case WheelUp, WheelDown:
		me.Button = vt.MouseButtonWheel
	default:
		me.Button = vt.MouseButtonNone
	}
	switch {
	case e.Button == WheelUp:
		me.Action = vt.MouseActionUp
	case e.Button == WheelDown:
		me.Action = vt.MouseActionDown
	case e.Action == Press:
		me.Action = vt.MouseActionDown
	case e.Action == Release:
		me.Action = vt.MouseActionUp
	default:
		me.Action = vt.MouseActionMove
		if t.dragging {
			me.Button = vt.MouseButtonLeft
		}
	}
	switch e.Action {
	case Press:
		t.dragging = e.Button == ButtonLeft
	case Release:
		t.dragging = false
	}
	t.Sess.Term.MouseService().TriggerMouseEvent(me)
}

// wheel scrolls the scrollback, or on the alternate screen (a full-screen
// program, which has none) sends the cursor keys, as xterm does. The session
// is locked.
func (t *Terminal) wheel(up bool) {
	term := t.Sess.Term
	if term.Buffers().Active() == term.Buffers().Alt() {
		k := KeyDown
		if up {
			k = KeyUp
		}
		seq := Encode(KeyEvent{Key: k}, term.CoreService().DecPrivateModes.ApplicationCursorKeys)
		term.Input(strings.Repeat(seq, 3), true)
		return
	}
	if up {
		term.ScrollLines(-3)
	} else {
		term.ScrollLines(3)
	}
}

// Key handles a key press: the terminal's own shortcuts first, then what goes
// to the program. It reports whether the window needs redrawing.
func (t *Terminal) Key(e KeyEvent) bool {
	if t.Menu.Open && e.Key == KeyEscape {
		t.Menu.Close()
		return true
	}
	ctrl, shift := e.Mods&Ctrl != 0, e.Mods&Shift != 0
	letter := e.Rune | 0x20 // the letter, either case
	switch {
	case ctrl && shift && letter == 'c':
		t.copy()
		return false
	case ctrl && shift && letter == 'v':
		t.paste(false)
		return false
	case ctrl && shift && letter == 'a':
		t.Sess.Lock()
		t.Sel.All(t.Sess.Term)
		t.Sess.Unlock()
		return true
	case shift && e.Key == KeyInsert:
		t.paste(true)
		return false
	case shift && (e.Key == KeyPageUp || e.Key == KeyPageDown):
		t.Sess.Lock()
		n := t.Sess.Term.Rows() - 1
		if e.Key == KeyPageUp {
			n = -n
		}
		t.Sess.Term.ScrollLines(n)
		t.Sess.Unlock()
		return true
	}
	t.Sess.Lock()
	appCursor := t.Sess.Term.CoreService().DecPrivateModes.ApplicationCursorKeys
	t.Sess.Unlock()
	s := Encode(e, appCursor)
	if s == "" {
		return false
	}
	t.Sel.Clear()
	t.Sess.Input(s)
	return true
}

// Text handles text that arrives composed rather than as key presses: from an
// on-screen keyboard or an input method. It goes to the program as typed.
func (t *Terminal) Text(s string) bool {
	if s == "" {
		return false
	}
	t.Sel.Clear()
	t.Sess.Input(s)
	return true
}

// openMenu shows the context menu at (x, y).
func (t *Terminal) openMenu(x, y int) {
	t.Sess.Lock()
	has := !t.Sel.Empty()
	t.Sess.Unlock()
	t.Menu.Show(t.Grid, x, y, t.size, []MenuItem{
		{"Copy", "Ctrl+Shift+C", has, t.copy},
		{"Paste", "Ctrl+Shift+V", t.Clip != nil, func() { t.paste(false) }},
		{"Select All", "Ctrl+Shift+A", true, func() {
			t.Sess.Lock()
			t.Sel.All(t.Sess.Term)
			t.Sess.Unlock()
		}},
	})
}

// copy puts the selection on the clipboard.
func (t *Terminal) copy() {
	t.Sess.Lock()
	text := t.Sel.Text(t.Sess.Term)
	t.Sess.Unlock()
	if text != "" && t.Clip != nil {
		t.Clip.Set(false, text)
	}
}

// paste sends the clipboard, or the primary selection, to the program.
func (t *Terminal) paste(primary bool) {
	if t.Clip == nil {
		return
	}
	t.Clip.Get(primary, t.Sess.Paste)
}
