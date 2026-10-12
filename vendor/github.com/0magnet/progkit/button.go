package progkit

import "github.com/gdamore/tcell/v3"

// Button is a labeled button. Where the host can, a real button is laid
// over it.
type Button struct {
	ID      string
	Label   string
	Style   tcell.Style
	OnPress func()
}

// Draw draws the button in r's first row, reversed when focused.
func (b *Button) Draw(f *Frame, r Rect, focused bool) {
	if r.Empty() {
		return
	}
	r.H = 1
	st := b.Style
	if focused {
		st = st.Reverse(true)
	}
	n := DrawText(f.Screen, r.X, r.Y, r.W, "[ "+b.Label+" ]", st)
	f.Place(b.ID, Rect{r.X, r.Y, n, 1}, Element{Kind: "button", Label: b.Label, Focus: focused}, func(m Msg) {
		if m.Type == "press" && b.OnPress != nil {
			b.OnPress()
		}
	})
}

// Key presses the button on Enter or space.
func (b *Button) Key(ev *tcell.EventKey) bool {
	if ev.Key() == tcell.KeyEnter || (ev.Key() == tcell.KeyRune && ev.Str() == " ") {
		if b.OnPress != nil {
			b.OnPress()
		}
		return true
	}
	return false
}
