package autoconfigui

import (
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
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
	buf     []rune
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
		s.editKey(ev)
		return
	}
	if ev.Key() != tcell.KeyRune || ev.Rune() != 'q' {
		s.quitArm = false
	}
	switch ev.Key() {
	case tcell.KeyUp:
		s.move(-1)
	case tcell.KeyDown:
		s.move(1)
	case tcell.KeyPgUp:
		for i := 0; i < 10; i++ {
			s.move(-1)
		}
	case tcell.KeyPgDn:
		for i := 0; i < 10; i++ {
			s.move(1)
		}
	case tcell.KeyEnter:
		s.activate()
	case tcell.KeyEscape, tcell.KeyCtrlC:
		s.done = true
	case tcell.KeyRune:
		s.runeKey(ev.Rune())
	}
}

func (s *tuiState) runeKey(r rune) {
	switch r {
	case 'k':
		s.move(-1)
	case 'j':
		s.move(1)
	case ' ':
		s.activate()
	case 'r':
		f := s.field()
		f.Value = f.Current
	case 'n':
		s.m.NoRestart = !s.m.NoRestart
	case 'p':
		s.finish(ActionPrint)
	case 's':
		s.finish(ActionApply)
	case 'q':
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
	s.buf = []rune(f.Value)
}

func (s *tuiState) editKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyEnter:
		f := s.field()
		f.Value = string(s.buf)
		if f.Type == "int" {
			if _, err := strconv.Atoi(strings.TrimSpace(f.Value)); err != nil {
				s.status = "not an integer: " + f.Value
			}
		}
		s.editing = false
	case tcell.KeyEscape:
		s.editing = false
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if len(s.buf) > 0 {
			s.buf = s.buf[:len(s.buf)-1]
		}
	case tcell.KeyCtrlU:
		s.buf = nil
	case tcell.KeyRune:
		s.buf = append(s.buf, ev.Rune())
	}
}

func draw(scr tcell.Screen, y, w int, text string, st tcell.Style) {
	i := 0
	for _, r := range text {
		if i >= w {
			break
		}
		scr.SetContent(i, y, r, nil, st)
		i++
	}
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

func (s *tuiState) render(scr tcell.Screen) {
	scr.Clear()
	w, h := scr.Size()
	base := tcell.StyleDefault
	bold := base.Bold(true)
	dim := base.Foreground(tcell.ColorGray)
	chg := base.Foreground(tcell.ColorYellow).Bold(true)
	draw(scr, 0, w, "skywire autoconfig  "+s.m.Path, bold)

	helpH := 5
	bodyH := h - 2 - helpH
	if bodyH < 1 {
		bodyH = 1
	}
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
			draw(scr, y, w, "== "+r.header+" ==", bold)
			continue
		}
		f := r.field
		val := f.Value
		if f.Secret && val != "" {
			val = strings.Repeat("*", len(val))
		}
		if s.editing && s.top+i == s.cur {
			val = string(s.buf) + "_"
		}
		mark := "  "
		st := base
		if f.Changed() {
			mark, st = "* ", chg
		}
		if s.top+i == s.cur {
			st = st.Reverse(true)
		}
		draw(scr, y, w, mark+padRight(f.Name, 28)+" "+val, st)
	}

	hy := h - 1 - helpH
	if s.cur < len(s.rows) && s.rows[s.cur].field != nil {
		f := s.field()
		text := f.Help
		if f.Note != "" {
			text += " Note: " + f.Note
		}
		if f.Default != "" {
			text += " (default " + f.Default + ")"
		}
		for i, l := range wrap(text, w) {
			if i >= helpH {
				break
			}
			draw(scr, hy+i, w, l, dim)
		}
	}
	nr := "restart"
	if s.m.NoRestart {
		nr = "no restart"
	}
	keys := "enter edit/toggle  r revert  n " + nr + "  p print command  s save  q quit"
	if s.status != "" {
		keys = s.status
	}
	draw(scr, h-1, w, keys, bold)
	scr.Show()
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
	scr, err := tcell.NewScreen()
	if err != nil {
		return ActionQuit, err
	}
	if err := scr.Init(); err != nil {
		return ActionQuit, err
	}
	defer scr.Fini()
	s := newTUIState(m)
	for !s.done {
		s.render(scr)
		switch ev := scr.PollEvent().(type) {
		case *tcell.EventResize:
			scr.Sync()
		case *tcell.EventKey:
			s.status = ""
			s.handleKey(ev)
		}
	}
	return s.action, nil
}
