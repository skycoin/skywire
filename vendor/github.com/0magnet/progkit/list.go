package progkit

import "github.com/gdamore/tcell/v3"

// List is a scrolling list with one selected row. Where the host can, a
// real list is laid over it.
type List struct {
	ID       string
	Items    []string
	Selected int
	Style    tcell.Style
	// OnActivate is told the row Enter or a double click chose.
	OnActivate func(index int)

	top int
}

// Draw draws the rows that fit in r, keeping the selected one in view.
func (l *List) Draw(f *Frame, r Rect, focused bool) {
	if r.Empty() {
		return
	}
	l.clamp()
	if l.Selected < l.top {
		l.top = l.Selected
	}
	if l.Selected >= l.top+r.H {
		l.top = l.Selected - r.H + 1
	}
	for i := 0; i < r.H && l.top+i < len(l.Items); i++ {
		st := l.Style
		if l.top+i == l.Selected {
			st = st.Reverse(focused).Bold(true)
		}
		row := Rect{r.X, r.Y + i, r.W, 1}
		Fill(f.Screen, row, st)
		DrawText(f.Screen, row.X, row.Y, row.W, l.Items[l.top+i], st)
	}
	f.Place(l.ID, r, Element{Kind: "list", Items: l.Items, Selected: l.Selected, Focus: focused}, func(m Msg) {
		switch m.Type {
		case "select":
			l.Selected = m.Index
		case "activate":
			l.Selected = m.Index
			if l.OnActivate != nil {
				l.OnActivate(m.Index)
			}
		}
	})
}

func (l *List) clamp() {
	l.Selected = min(max(l.Selected, 0), max(len(l.Items)-1, 0))
}

// Key moves the selection, and reports whether it used the key.
func (l *List) Key(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyUp:
		l.Selected--
	case tcell.KeyDown:
		l.Selected++
	case tcell.KeyPgUp:
		l.Selected -= 10
	case tcell.KeyPgDn:
		l.Selected += 10
	case tcell.KeyHome:
		l.Selected = 0
	case tcell.KeyEnd:
		l.Selected = len(l.Items) - 1
	case tcell.KeyEnter:
		if l.OnActivate != nil && len(l.Items) > 0 {
			l.OnActivate(l.Selected)
		}
	default:
		return false
	}
	l.clamp()
	return true
}
