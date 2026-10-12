package autoconfigui

import (
	"strconv"
	"strings"

	"github.com/0magnet/progkit"
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
)

// Action is what the operator chose when the terminal form closed.
type Action int

// Terminal form outcomes.
const (
	ActionQuit Action = iota
	ActionApply
	ActionPrint
)

type row struct {
	header string
	field  *Field
}

type tuiState struct {
	m       *Model
	rows    []row
	cur     int
	top     int
	editing bool
	input   progkit.Input
	status  string
	quitArm bool
	action  Action
	done    bool
}

func newTUIState(m *Model) *tuiState {
	s := &tuiState{m: m}
	for _, g := range m.Groups() {
		s.rows = append(s.rows, row{header: g})
		for _, f := range m.In(g) {
			s.rows = append(s.rows, row{field: f})
		}
	}
	s.cur = 1
	s.input = progkit.Input{ID: "value", OnSubmit: s.commit}
	return s
}

func (s *tuiState) field() *Field { return s.rows[s.cur].field }

func (s *tuiState) move(d int) {
	for i := s.cur + d; i >= 0 && i < len(s.rows); i += d {
		if s.rows[i].field != nil {
			s.cur = i
			return
		}
	}
}

// handleKey applies one key press and reports nothing, results live in s.
func (s *tuiState) handleKey(ev *tcell.EventKey) {
	if s.editing {
		if ev.Key() == tcell.KeyEscape {
			s.editing = false
			return
		}
		s.input.Key(ev)
		return
	}
	if progkit.Typed(ev) != "q" {
		s.quitArm = false
	}
	switch {
	case ev.Key() == tcell.KeyUp:
		s.move(-1)
	case ev.Key() == tcell.KeyDown:
		s.move(1)
	case ev.Key() == tcell.KeyPgUp:
		for i := 0; i < 10; i++ {
			s.move(-1)
		}
	case ev.Key() == tcell.KeyPgDn:
		for i := 0; i < 10; i++ {
			s.move(1)
		}
	case ev.Key() == tcell.KeyEnter:
		s.activate()
	case ev.Key() == tcell.KeyEscape, progkit.IsCtrl(ev, 'c'):
		s.done = true
	default:
		s.typed(progkit.Typed(ev))
	}
}

func (s *tuiState) typed(k string) {
	switch k {
	case "k":
		s.move(-1)
	case "j":
		s.move(1)
	case " ":
		s.activate()
	case "r":
		f := s.field()
		f.Value = f.Current
	case "n":
		s.m.NoRestart = !s.m.NoRestart
	case "p":
		s.finish(ActionPrint)
	case "s":
		s.finish(ActionApply)
	case "q":
		if s.changes() == 0 || s.quitArm {
			s.done = true
			return
		}
		s.quitArm = true
		s.status = "unsaved changes, press q again to quit"
	}
}

func (s *tuiState) changes() int {
	n := 0
	for _, f := range s.m.Fields {
		if f.Changed() {
			n++
		}
	}
	return n
}

func (s *tuiState) finish(a Action) {
	if _, err := s.m.Args(); err != nil {
		s.status = err.Error()
		return
	}
	s.action, s.done = a, true
}

func (s *tuiState) activate() {
	f := s.field()
	if f.Type == "bool" {
		b, _ := strconv.ParseBool(f.Value) //nolint:errcheck // junk reads as false
		f.Value = strconv.FormatBool(!b)
		return
	}
	s.editing = true
	s.input.SetValue(f.Value)
}

func (s *tuiState) commit(v string) {
	f := s.field()
	f.Value = v
	if f.Type == "int" {
		if _, err := strconv.Atoi(strings.TrimSpace(f.Value)); err != nil {
			s.status = "not an integer: " + f.Value
		}
	}
	s.editing = false
}

func wrap(text string, w int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		if line != "" && len(line)+1+len(word) > w {
			lines = append(lines, line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

func (s *tuiState) render(f *progkit.Frame) {
	scr, w, h := f.Screen, f.W, f.H
	base := tcell.StyleDefault
	bold := base.Bold(true)
	dim := base.Foreground(color.Gray)
	chg := base.Foreground(color.Yellow).Bold(true)
	progkit.DrawText(scr, 0, 0, w, "skywire autoconfig  "+s.m.Path, bold)

	helpH := 5
	bodyH := max(h-2-helpH, 1)
	if s.cur < s.top {
		s.top = s.cur
	}
	if s.cur >= s.top+bodyH {
		s.top = s.cur - bodyH + 1
	}
	for i := 0; i < bodyH && s.top+i < len(s.rows); i++ {
		r := s.rows[s.top+i]
		y := 1 + i
		if r.field == nil {
			progkit.DrawText(scr, 0, y, w, "== "+r.header+" ==", bold)
			continue
		}
		fl := r.field
		val := fl.Value
		if fl.Secret && val != "" {
			val = strings.Repeat("*", len(val))
		}
		mark := "  "
		st := base
		if fl.Changed() {
			mark, st = "* ", chg
		}
		label := mark + padRight(fl.Name, 28) + " "
		if s.editing && s.top+i == s.cur {
			x := progkit.DrawText(scr, 0, y, w, label, st.Reverse(true))
			s.input.Draw(f, progkit.Rect{X: x, Y: y, W: w - x, H: 1}, true)
			continue
		}
		if s.top+i == s.cur {
			st = st.Reverse(true)
		}
		progkit.DrawText(scr, 0, y, w, label+val, st)
	}

	hy := h - 1 - helpH
	if s.cur < len(s.rows) && s.rows[s.cur].field != nil {
		fl := s.field()
		text := fl.Help
		if fl.Note != "" {
			text += " Note: " + fl.Note
		}
		if fl.Default != "" {
			text += " (default " + fl.Default + ")"
		}
		for i, l := range wrap(text, w) {
			if i >= helpH {
				break
			}
			progkit.DrawText(scr, 0, hy+i, w, l, dim)
		}
	}
	nr := "restart"
	if s.m.NoRestart {
		nr = "no restart"
	}
	keys := "enter edit/toggle  r revert  n " + nr + "  p print command  s save  q quit"
	if s.editing {
		keys = "enter keep  esc cancel"
	}
	if s.status != "" {
		keys = s.status
	}
	progkit.DrawText(scr, 0, h-1, w, keys, bold)
}

func padRight(s string, n int) string {
	for len(s) < n {
		s += " "
	}
	return s
}

// RunTUI shows the form in the terminal and returns what the operator chose.
// The model holds the edits afterwards.
func RunTUI(m *Model) (Action, error) {
	app, err := progkit.Open()
	if err != nil {
		return ActionQuit, err
	}
	defer app.Close()
	s := newTUIState(m)
	app.Run(s.render, func(ev tcell.Event) bool {
		if ev, ok := ev.(*tcell.EventKey); ok {
			s.status = ""
			s.handleKey(ev)
		}
		return !s.done
	})
	return s.action, nil
}
