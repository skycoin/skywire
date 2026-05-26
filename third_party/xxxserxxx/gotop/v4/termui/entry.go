package termui

import (
	"image"
	"strings"
	"unicode/utf8"

	ui "github.com/gizak/termui/v3"
	rw "github.com/mattn/go-runewidth"
	"github.com/skycoin/skywire/third_party/xxxserxxx/gotop/v4/utils"
)

const (
	ELLIPSIS = "…"
	CURSOR   = " "
)

type Entry struct {
	ui.Block

	Style ui.Style

	Label          string
	Value          string
	ShowWhenEmpty  bool
	UpdateCallback func(string)

	editing bool
}

func (e *Entry) SetEditing(editing bool) {
	e.editing = editing
}

func (e *Entry) update() {
	if e.UpdateCallback != nil {
		e.UpdateCallback(e.Value)
	}
}

// HandleEvent handles input events if the entry is being edited.
// Returns true if the event was handled.
func (e *Entry) HandleEvent(ev ui.Event) bool {
	if !e.editing {
		return false
	}
	if utf8.RuneCountInString(ev.ID) == 1 {
		e.Value += ev.ID
		e.update()
		return true
	}
	switch ev.ID {
	case "<C-c>", "<Escape>":
		e.Value = ""
		e.editing = false
		e.update()
	case "<Enter>":
		e.editing = false
	case "<Backspace>":
		if e.Value != "" {
			r := []rune(e.Value)
			e.Value = string(r[:len(r)-1])
			e.update()
		}
	case "<Space>":
		e.Value += " "
		e.update()
	default:
		return false
	}
	return true
}

func (e *Entry) Draw(buf *ui.Buffer) {
	if e.Value == "" && !e.editing && !e.ShowWhenEmpty {
		return
	}

	style := e.Style
	label := e.Label
	if e.editing {
		label += "["
		style = ui.NewStyle(style.Fg, style.Bg, ui.ModifierBold)
	}
	cursorStyle := ui.NewStyle(style.Bg, style.Fg, ui.ModifierClear)

	p := image.Pt(e.Min.X, e.Min.Y)
	buf.SetString(label, style, p)
	p.X += rw.StringWidth(label)

	tail := " "
	if e.editing {
		tail = "] "
	}

	maxLen := e.Max.X - p.X - rw.StringWidth(tail)
	if e.editing {
		maxLen -= 1 // for cursor
	}
	value := utils.TruncateFront(e.Value, maxLen, ELLIPSIS)
	buf.SetString(value, e.Style, p)
	p.X += rw.StringWidth(value)

	if e.editing {
		buf.SetString(CURSOR, cursorStyle, p)
		p.X += rw.StringWidth(CURSOR)
		if remaining := maxLen - rw.StringWidth(value); remaining > 0 {
			buf.SetString(strings.Repeat(" ", remaining), e.TitleStyle, p)
			p.X += remaining
		}
	}
	buf.SetString(tail, style, p)
}
