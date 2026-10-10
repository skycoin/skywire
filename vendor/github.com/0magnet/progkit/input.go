package progkit

import (
	"github.com/gdamore/tcell/v3"
	"github.com/rivo/uniseg"
)

// Input is a one-line text field. Where the host can, a real text input is
// laid over it, and what is typed there arrives here.
type Input struct {
	ID          string
	Placeholder string
	Style       tcell.Style
	// OnSubmit is told the value when Enter is pressed.
	OnSubmit func(value string)

	value  []rune
	cursor int
	scroll int
}

// Value is what is in the field.
func (in *Input) Value() string { return string(in.value) }

// SetValue replaces what is in the field and puts the cursor at its end.
func (in *Input) SetValue(s string) {
	in.value = []rune(s)
	in.cursor = len(in.value)
}

// Draw draws the field in r's first row, with the cursor when focused.
func (in *Input) Draw(f *Frame, r Rect, focused bool) {
	if r.Empty() {
		return
	}
	r.H = 1
	Fill(f.Screen, r, in.Style)
	if len(in.value) == 0 && in.Placeholder != "" {
		DrawText(f.Screen, r.X, r.Y, r.W, in.Placeholder, in.Style.Dim(true))
	}
	if in.cursor < in.scroll {
		in.scroll = in.cursor
	}
	for uniseg.StringWidth(string(in.value[in.scroll:in.cursor])) >= r.W && in.scroll < in.cursor {
		in.scroll++
	}
	DrawText(f.Screen, r.X, r.Y, r.W, string(in.value[in.scroll:]), in.Style)
	if focused {
		f.Screen.ShowCursor(r.X+uniseg.StringWidth(string(in.value[in.scroll:in.cursor])), r.Y)
	}
	f.Place(in.ID, r, Element{Kind: "input", Value: string(in.value), Placeholder: in.Placeholder, Focus: focused}, in.message)
}

func (in *Input) message(m Msg) {
	switch m.Type {
	case "change":
		in.SetValue(m.Value)
	case "submit":
		in.SetValue(m.Value)
		if in.OnSubmit != nil {
			in.OnSubmit(m.Value)
		}
	}
}

// Key edits the field, and reports whether it used the key.
func (in *Input) Key(ev *tcell.EventKey) bool {
	switch {
	case IsCtrl(ev, 'a'):
		in.cursor = 0
		return true
	case IsCtrl(ev, 'e'):
		in.cursor = len(in.value)
		return true
	case IsCtrl(ev, 'u'):
		in.value, in.cursor = in.value[in.cursor:], 0
		return true
	case Typed(ev) != "":
		r := []rune(Typed(ev))
		in.value = append(in.value[:in.cursor], append(r, in.value[in.cursor:]...)...)
		in.cursor += len(r)
		return true
	}
	switch ev.Key() {
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if in.cursor > 0 {
			in.value = append(in.value[:in.cursor-1], in.value[in.cursor:]...)
			in.cursor--
		}
	case tcell.KeyDelete:
		if in.cursor < len(in.value) {
			in.value = append(in.value[:in.cursor], in.value[in.cursor+1:]...)
		}
	case tcell.KeyLeft:
		in.cursor = max(in.cursor-1, 0)
	case tcell.KeyRight:
		in.cursor = min(in.cursor+1, len(in.value))
	case tcell.KeyHome:
		in.cursor = 0
	case tcell.KeyEnd:
		in.cursor = len(in.value)
	case tcell.KeyEnter:
		if in.OnSubmit != nil {
			in.OnSubmit(string(in.value))
		}
	default:
		return false
	}
	return true
}

// Paste inserts pasted text, its line ends as spaces.
func (in *Input) Paste(s string) {
	for _, r := range s {
		if r == '\n' || r == '\r' {
			r = ' '
		}
		in.value = append(in.value[:in.cursor], append([]rune{r}, in.value[in.cursor:]...)...)
		in.cursor++
	}
}
