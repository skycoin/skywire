package progkit

import (
	_ "embed"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/0magnet/websh/childtty"
	"github.com/0magnet/websh/progressive"
	"github.com/gdamore/tcell/v3"
)

// widgetName is the name the shipped document is placed by.
const widgetName = "progkit"

//go:embed widget.html
var widgetHTML []byte

// App is a program's screen, the events its host sends, and the elements
// it has laid over its cells.
type App struct {
	Screen tcell.Screen
	// Theme colors the elements laid over the cells.
	Theme Theme
	// CellsOnly turns placements off, as if the host offered none.
	CellsOnly bool

	tty      io.Writer // the terminal, for sequences in order with tcell's
	caps     *progressive.Caps
	shipped  bool
	shown    map[string]shownPlacement
	frame    map[string]shownPlacement
	handlers map[string]func(Msg)
	mu       sync.Mutex
	closed   atomic.Bool
}

type shownPlacement struct {
	r  Rect
	el string // the element as posted, JSON
}

// Theme is the CSS colors of the elements laid over the cells.
type Theme struct {
	FG     string `json:"fg"`
	BG     string `json:"bg"`
	Accent string `json:"accent"`
	Dim    string `json:"dim"`
}

// DefaultTheme matches a dark terminal.
var DefaultTheme = Theme{FG: "#d0d0d0", BG: "#000000", Accent: "#3a7bd5", Dim: "#808080"}

// Open opens the program's terminal: asked first what its host offers, with
// the host's events taken out of its input. Where there is no terminal it
// falls back to tcell's own screen, and draws cells only.
func Open() (*App, error) {
	a := &App{Theme: DefaultTheme, shown: map[string]shownPlacement{}, handlers: map[string]func(Msg){}}
	var err error
	if t, ok := childtty.Open(); ok {
		a.tty = t
		a.caps = progressive.Current()
		a.Screen, err = tcell.NewTerminfoScreenFromTty(t)
	} else {
		a.Screen, err = tcell.NewScreen()
	}
	if err != nil {
		return nil, err
	}
	if err := a.Screen.Init(); err != nil {
		return nil, err
	}
	a.Screen.EnableMouse()
	a.Screen.EnablePaste()
	if evs := childtty.Events(); evs != nil {
		go func() {
			for e := range evs {
				a.Screen.EventQ() <- e
			}
		}()
	}
	return a, nil
}

// NewApp is an App on a screen the caller made and initialized, writing
// placements to tty when it is not nil and caps offers them. Tests and
// programs with a terminal of their own use it.
func NewApp(sc tcell.Screen, tty io.Writer, caps *progressive.Caps) *App {
	return &App{Screen: sc, Theme: DefaultTheme, tty: tty, caps: caps, shown: map[string]shownPlacement{}, handlers: map[string]func(Msg){}}
}

// Placing reports whether elements are laid over the cells.
func (a *App) Placing() bool {
	return !a.CellsOnly && a.tty != nil && a.caps.Has("ship") && a.caps.Has("place.input") && a.caps.Has("event")
}

// Caps is what the host offers, or nil.
func (a *App) Caps() *progressive.Caps { return a.caps }

// Close takes the placements away and gives the terminal back.
func (a *App) Close() {
	a.closed.Store(true)
	if len(a.shown) > 0 {
		a.write(progressive.Clear())
	}
	a.Screen.Fini()
}

// Redraw asks Run for a new frame, from any goroutine.
func (a *App) Redraw() {
	if a.closed.Load() {
		return
	}
	// Close may still win the race and close the queue under this send.
	defer func() { _ = recover() }() //nolint:errcheck
	select {
	case a.Screen.EventQ() <- tcell.NewEventInterrupt(nil):
	default:
	}
}

// Run draws a frame, shows it, and waits for the next event, until handle
// returns false. Messages from placed elements go to the widget that placed
// them first, and then to handle as a *MsgEvent.
func (a *App) Run(draw func(f *Frame), handle func(ev tcell.Event) bool) {
	for {
		a.Draw(draw)
		ev := <-a.Screen.EventQ()
		switch ev := ev.(type) {
		case *tcell.EventResize:
			a.Screen.Sync()
		case *progressive.Event:
			m, ok := a.Deliver(ev)
			if !ok {
				continue
			}
			var out tcell.Event = m
			if k := m.Msg.Key(); k != nil {
				out = k
			}
			if !handle(out) {
				return
			}
			continue
		}
		if !handle(ev) {
			return
		}
	}
}

// Draw draws one frame with draw and shows it, with its placements.
func (a *App) Draw(draw func(f *Frame)) {
	a.Screen.Clear()
	// Only a widget that has the cursor shows it; it does so as it draws.
	a.Screen.HideCursor()
	a.frame = map[string]shownPlacement{}
	f := &Frame{App: a, Screen: a.Screen}
	f.W, f.H = a.Screen.Size()
	draw(f)
	a.Screen.Show()
	a.flush()
}

// Deliver hands a host event to the widget whose placement it is about, and
// returns it as a MsgEvent.
func (a *App) Deliver(ev *progressive.Event) (*MsgEvent, bool) {
	if ev.Type != "message" {
		return nil, false
	}
	var m Msg
	if err := json.Unmarshal(ev.Data, &m); err != nil {
		return nil, false
	}
	a.mu.Lock()
	h := a.handlers[ev.ID]
	a.mu.Unlock()
	if h != nil {
		h(m)
	}
	return &MsgEvent{ID: ev.ID, Msg: m, Event: ev}, true
}

// flush lays this frame's elements over the cells and takes away the ones
// it no longer has.
func (a *App) flush() {
	if !a.Placing() {
		return
	}
	if !a.shipped && len(a.frame) > 0 {
		for _, s := range progressive.Ship(widgetName, widgetHTML) {
			a.write(s)
		}
		a.shipped = true
	}
	for id, p := range a.frame {
		old, ok := a.shown[id]
		if !ok || old.r != p.r {
			a.write(progressive.Place(id, progressive.Placement{
				Row: p.r.Y, Col: p.r.X, W: p.r.W, H: p.r.H,
				Widget: widgetName, Input: true, Events: true,
			}))
		}
		if !ok || old.el != p.el {
			a.write(progressive.Post(id, json.RawMessage(p.el)))
		}
	}
	for id := range a.shown {
		if _, ok := a.frame[id]; !ok {
			a.write(progressive.Remove(id))
			a.mu.Lock()
			delete(a.handlers, id)
			a.mu.Unlock()
		}
	}
	a.shown = a.frame
}

func (a *App) write(s string) {
	if a.tty != nil {
		io.WriteString(a.tty, s) //nolint:errcheck,gosec // a terminal that fails a write fails the cells too
	}
}

// Frame is one frame being drawn.
type Frame struct {
	*App
	Screen tcell.Screen
	W, H   int
}

// Size is the whole screen.
func (f *Frame) Size() Rect { return Rect{0, 0, f.W, f.H} }

// Place lays el over r for this frame, when the host can show it, and sends
// what the person does with it to on.
func (f *Frame) Place(id string, r Rect, el Element, on func(Msg)) {
	if !f.Placing() || r.Empty() {
		return
	}
	el.Theme = f.Theme
	b, err := json.Marshal(el)
	if err != nil {
		return
	}
	f.frame[id] = shownPlacement{r: r, el: string(b)}
	f.mu.Lock()
	f.handlers[id] = on
	f.mu.Unlock()
}

// Element is what a placement shows: the HTML element of a widget.
type Element struct {
	Kind        string       `json:"kind"` // input, button, list or text
	Value       string       `json:"value,omitempty"`
	Placeholder string       `json:"placeholder,omitempty"`
	Label       string       `json:"label,omitempty"`
	Items       []string     `json:"items,omitempty"`
	Lines       [][]HTMLSpan `json:"lines,omitempty"`
	Selected    int          `json:"selected"`
	Top         int          `json:"top"`
	Focus       bool         `json:"focus,omitempty"`
	Theme       Theme        `json:"theme"`
}

// HTMLSpan is a span of a text element, its colors as CSS.
type HTMLSpan struct {
	Text string `json:"t"`
	FG   string `json:"f,omitempty"`
	BG   string `json:"b,omitempty"`
	Bold bool   `json:"w,omitempty"`
}

// Msg is what a placed element reports: change, submit, press, select,
// activate or scroll, with the value or index it is about.
type Msg struct {
	Type  string `json:"type"`
	Value string `json:"value,omitempty"`
	Index int    `json:"index,omitempty"`
}

// MsgEvent is a Msg as Run hands it to the program, after the widget that
// placed the element has had it.
type MsgEvent struct {
	ID  string
	Msg Msg
	*progressive.Event
}

// Key is the key a placed element passed on to the program, named as the
// browser names it (KeyboardEvent.key), with "Ctrl+" before a held Ctrl, or nil.
func (m Msg) Key() *tcell.EventKey {
	if m.Type != "key" {
		return nil
	}
	if k, ok := browserKeys[m.Value]; ok {
		return tcell.NewEventKey(k, "", 0)
	}
	if c, ok := strings.CutPrefix(m.Value, "Ctrl+"); ok && len([]rune(c)) == 1 {
		return tcell.NewEventKey(tcell.KeyRune, c, tcell.ModCtrl)
	}
	if r := []rune(m.Value); len(r) == 1 {
		return tcell.NewEventKey(tcell.KeyRune, m.Value, 0)
	}
	return nil
}

var browserKeys = map[string]tcell.Key{
	"Escape": tcell.KeyEscape, "Tab": tcell.KeyTab, "Backtab": tcell.KeyBacktab,
	"Enter": tcell.KeyEnter, "Backspace": tcell.KeyBackspace, "Delete": tcell.KeyDelete,
	"ArrowUp": tcell.KeyUp, "ArrowDown": tcell.KeyDown, "ArrowLeft": tcell.KeyLeft, "ArrowRight": tcell.KeyRight,
	"PageUp": tcell.KeyPgUp, "PageDown": tcell.KeyPgDn, "Home": tcell.KeyHome, "End": tcell.KeyEnd,
}
