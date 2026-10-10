package progkit

import (
	"fmt"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
)

// Text is a scrolling view of styled lines. Where the host can, the same
// text is laid over it as selectable HTML, so it can be copied.
type Text struct {
	ID    string
	Style tcell.Style
	// Follow keeps the view at the end as lines are added, until the person
	// scrolls up.
	Follow bool
	// Selectable lays the HTML text over the cells.
	Selectable bool

	lines []Line
	top   int
	h     int
}

// SetANSI replaces the text with s, its SGR colors kept.
func (t *Text) SetANSI(s string) { t.SetLines(ParseANSI(s, t.Style)) }

// SetLines replaces the text.
func (t *Text) SetLines(l []Line) {
	atEnd := t.top >= t.maxTop()
	t.lines = l
	if t.Follow && atEnd {
		t.top = t.maxTop()
	}
	t.top = min(t.top, t.maxTop())
}

// Append adds lines at the end.
func (t *Text) Append(l ...Line) { t.SetLines(append(t.lines, l...)) }

// Lines is the text.
func (t *Text) Lines() []Line { return t.lines }

// Top is the first line in view.
func (t *Text) Top() int { return t.top }

func (t *Text) maxTop() int { return max(len(t.lines)-max(t.h, 1), 0) }

// ScrollTo puts line i at the top of the view.
func (t *Text) ScrollTo(i int) { t.top = min(max(i, 0), t.maxTop()) }

// Draw draws the lines in view.
func (t *Text) Draw(f *Frame, r Rect) {
	if r.Empty() {
		return
	}
	t.h = r.H
	t.top = min(t.top, t.maxTop())
	Fill(f.Screen, r, t.Style)
	for i := 0; i < r.H && t.top+i < len(t.lines); i++ {
		DrawLine(f.Screen, r.X, r.Y+i, r.W, t.lines[t.top+i])
	}
	if !t.Selectable {
		return
	}
	f.Place(t.ID, r, Element{Kind: "text", Lines: htmlLines(t.lines), Top: t.top}, func(m Msg) {
		if m.Type == "scroll" {
			t.ScrollTo(m.Index)
		}
	})
}

// Key scrolls, and reports whether it used the key.
func (t *Text) Key(ev *tcell.EventKey) bool {
	page := max(t.h-1, 1)
	switch ev.Key() {
	case tcell.KeyUp:
		t.ScrollTo(t.top - 1)
	case tcell.KeyDown:
		t.ScrollTo(t.top + 1)
	case tcell.KeyPgUp:
		t.ScrollTo(t.top - page)
	case tcell.KeyPgDn:
		t.ScrollTo(t.top + page)
	case tcell.KeyHome:
		t.ScrollTo(0)
	case tcell.KeyEnd:
		t.ScrollTo(t.maxTop())
	default:
		return false
	}
	return true
}

// Mouse scrolls on the wheel, and reports whether it used the event.
func (t *Text) Mouse(ev *tcell.EventMouse) bool {
	switch {
	case ev.Buttons()&tcell.WheelUp != 0:
		t.ScrollTo(t.top - 3)
	case ev.Buttons()&tcell.WheelDown != 0:
		t.ScrollTo(t.top + 3)
	default:
		return false
	}
	return true
}

func htmlLines(lines []Line) [][]HTMLSpan {
	out := make([][]HTMLSpan, len(lines))
	for i, l := range lines {
		for _, s := range l {
			out[i] = append(out[i], HTMLSpan{
				Text: s.Text,
				FG:   cssColor(s.Style.GetForeground()),
				BG:   cssColor(s.Style.GetBackground()),
				Bold: s.Style.GetAttributes()&tcell.AttrBold != 0,
			})
		}
	}
	return out
}

func cssColor(c color.Color) string {
	if c == color.Default || !c.Valid() {
		return ""
	}
	r, g, b := c.RGB()
	if r < 0 {
		return ""
	}
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}
