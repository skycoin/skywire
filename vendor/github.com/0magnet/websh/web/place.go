//go:build js && wasm

package web

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"syscall/js"

	"github.com/0magnet/websh/progressive"
	offer "github.com/0magnet/websh/widget"
)

// Placements: a program in the terminal lays an image, or a widget the page
// offers, over a rectangle of the terminal's cells, with no frame or
// chrome — html where it is needed, cells everywhere else. Like the viewer
// it is asked for with OSC 7337, so it is output like any other:
//
//	OSC 7337 ; place ; <id> ; <data> ST   lay <data> over its cells, or move it
//	OSC 7337 ; remove ; <id> ST           take it away
//	OSC 7337 ; clear ST                   take every placement away
//
// <data> is base64 of a JSON object: {"row": 2, "col": 40, "w": 30, "h": 12,
// and "url": "https://…" (an image) or "widget": "<name>"}, row and col from
// 0 at the top left of the screen. The program draws its own cells under it
// as ever, which a terminal without placements shows instead, and which show
// through where an image does not cover them. A placement takes no input —
// the mouse and the keys still go to the program — unless it asks with
// "input": true: then the mouse over it is its own (a widget turned or zoomed
// by hand), and the program keeps the keys. What a command placed is taken
// away when it ends. A widget is the page's (RegisterWidget) or one a
// program run from the filesystem offered (package widget).

// A Widget makes a widget's elements in el, the placement it fills, and
// returns what to do when the placement is taken away (nil for nothing).
type Widget func(el js.Value) (unmount func())

var (
	widgetsMu sync.Mutex
	widgets   = map[string]Widget{}
)

// RegisterWidget offers a widget by name to the programs in this page's
// terminals: OSC 7337 place with "widget": name lays it over their cells.
// Only what the page registers can be placed this way; a program can name a
// widget, never supply one.
func RegisterWidget(name string, w Widget) {
	widgetsMu.Lock()
	defer widgetsMu.Unlock()
	widgets[name] = w
}

func widget(name string) Widget {
	widgetsMu.Lock()
	defer widgetsMu.Unlock()
	return widgets[name]
}

// placeData is a placement's request.
type placeData struct {
	Row    int    `json:"row"`
	Col    int    `json:"col"`
	W      int    `json:"w"`
	H      int    `json:"h"`
	URL    string `json:"url"`
	Widget string `json:"widget"`
	// Fit is how an image fills its cells: "contain" (the default: all of
	// it, the cells showing at the edges where its shape differs), "cover"
	// (all the cells, its edges cut) or "fill" (both, stretched).
	Fit string `json:"fit"`
	// Input gives the placement the mouse over it, instead of the program.
	Input bool `json:"input"`
	// Page, with Input, lets the mouse over it go on to the page around the
	// terminal as well (takeInput).
	Page bool `json:"page"`
	// Events reports what happens to the placement to the program, on its
	// input: clicks on it (with Input), and messages from its widget.
	Events bool `json:"events"`
}

// A placement is one element over the cells.
type placement struct {
	id      string
	d       placeData
	el      js.Value
	unmount func()
	stops   []js.Func // what keeps its mouse from the terminal
	// port is the host's end of an offered widget's line to the program
	// (widget.Conn), and onMsg its listener.
	port  js.Value
	onMsg js.Func
}

// placements is a session's layer of placements: one element over the
// terminal's screen, its children positioned in fractions of the screen,
// so they keep to their cells however the terminal is zoomed or its box
// resized.
type placements struct {
	s     *Session
	root  js.Value // the session's element, the terminal inside it
	layer js.Value
	by    map[string]*placement
	cols  int
	rows  int
	// shipped is what the running command has shipped (ship.go).
	shipped shipping
}

func newPlacements(s *Session, root js.Value) *placements {
	return &placements{s: s, root: root, by: map[string]*placement{}}
}

// layerEl is the layer, made on first use inside the terminal's screen.
func (p *placements) layerEl() js.Value {
	if p.layer.Truthy() {
		return p.layer
	}
	screen := p.root.Call("querySelector", ".xterm-screen")
	if !screen.Truthy() {
		return js.Value{}
	}
	p.layer = js.Global().Get("document").Call("createElement", "div")
	p.layer.Get("style").Set("cssText", "position:absolute;inset:0;pointer-events:none;z-index:5;overflow:hidden")
	screen.Call("append", p.layer)
	return p.layer
}

// place lays d over its cells as id, replacing what id was.
func (p *placements) place(id string, d placeData) {
	layer := p.layerEl()
	if !layer.Truthy() || d.W <= 0 || d.H <= 0 {
		return
	}
	if old := p.by[id]; old != nil {
		if old.d.URL == d.URL && old.d.Widget == d.Widget && old.d.Fit == d.Fit && old.d.Input == d.Input && old.d.Page == d.Page && old.d.Events == d.Events {
			old.d = d // same content: only moved
			p.position(old)
			return
		}
		p.remove(id)
	}
	doc := js.Global().Get("document")
	pl := &placement{id: id, d: d}
	switch {
	case d.Widget != "":
		// The page's own widgets first, then those the program shipped,
		// then those a program running here offered from its own process.
		w := widget(d.Widget)
		sw, shipped := p.shipped.done[d.Widget]
		var offered js.Value
		if w == nil && !shipped {
			m, ok := offer.Find(d.Widget)
			if !ok || p.s.remote() {
				return // a remote program is not in the tab to offer one
			}
			offered = m
		}
		pl.el = doc.Call("createElement", "div")
		pl.el.Get("style").Set("cssText", "position:absolute;overflow:hidden")
		layer.Call("append", pl.el)
		// Placed before it is filled, so the widget can size itself from
		// the element (a canvas, say).
		p.position(pl)
		switch {
		case w != nil:
			pl.unmount = w(pl.el)
		case shipped:
			p.mountShipped(pl, sw)
		default:
			p.mountOffered(pl, offered)
		}
	case strings.HasPrefix(d.URL, "https://") || strings.HasPrefix(d.URL, "http://"):
		pl.el = doc.Call("createElement", "img")
		fit := "contain"
		if d.Fit == "cover" || d.Fit == "fill" {
			fit = d.Fit
		}
		pl.el.Get("style").Set("cssText", "position:absolute;object-fit:"+fit)
		pl.el.Set("src", d.URL)
		pl.el.Set("alt", "")
		layer.Call("append", pl.el)
	default:
		return
	}
	if d.Input {
		p.takeInput(pl)
		if d.Events {
			p.reportClicks(pl)
		}
	}
	p.by[id] = pl
	p.position(pl)
}

// position sets pl's box from its cells and the screen's size now.
func (p *placements) position(pl *placement) {
	cols, rows := p.s.Term.Core.Cols(), p.s.Term.Core.Rows()
	if cols <= 0 || rows <= 0 {
		return
	}
	p.cols, p.rows = cols, rows
	pct := func(n, of int) string { return strconv.FormatFloat(100*float64(n)/float64(of), 'f', 4, 64) + "%" }
	// Its cells, for a widget drawn in them (data-cols, data-rows).
	pl.el.Get("dataset").Set("cols", pl.d.W)
	pl.el.Get("dataset").Set("rows", pl.d.H)
	st := pl.el.Get("style")
	st.Set("left", pct(pl.d.Col, cols))
	st.Set("top", pct(pl.d.Row, rows))
	st.Set("width", pct(pl.d.W, cols))
	st.Set("height", pct(pl.d.H, rows))
}

// reposition puts every placement back on its cells if the screen's
// columns or rows changed since they were placed.
func (p *placements) reposition() {
	if p.s.Term.Core.Cols() == p.cols && p.s.Term.Core.Rows() == p.rows {
		return
	}
	for _, pl := range p.by {
		p.position(pl)
	}
}

func (p *placements) remove(id string) {
	pl := p.by[id]
	if pl == nil {
		return
	}
	delete(p.by, id)
	if pl.unmount != nil {
		pl.unmount()
	}
	for _, f := range pl.stops {
		f.Release()
	}
	if pl.port.Truthy() {
		pl.port.Set("onmessage", js.Null())
		pl.port.Call("close")
	}
	pl.onMsg.Release()
	// A placement taken away while it had the keys gives them back to the
	// terminal: otherwise they go nowhere until the person clicks it.
	focused := pl.el.Call("contains", js.Global().Get("document").Get("activeElement")).Truthy()
	pl.el.Call("remove")
	if focused {
		p.s.Term.Focus()
	}
}

// mountOffered fills pl with a widget a program offered. Its mount and
// unmount belong to another Go runtime, so they run on microtasks of their
// own (package widget), never from this one's stack; a placement taken away
// before its widget is up has it taken down as soon as it is.
//
// The widget gets the other end of a MessageChannel: what it sends there
// reaches the program as an event, when the placement asked for events, and
// what the program posts reaches it. A port delivers later, by itself, which
// is what keeps the two runtimes apart.
func (p *placements) mountOffered(pl *placement, mount js.Value) {
	ch := js.Global().Get("MessageChannel").New()
	pl.port = ch.Get("port1")
	if pl.d.Events {
		pl.onMsg = js.FuncOf(func(_ js.Value, args []js.Value) any {
			if len(args) == 0 {
				return nil
			}
			d := args[0].Get("data")
			if d.Type() != js.TypeString || !json.Valid([]byte(d.String())) {
				return nil // messages are JSON text
			}
			p.s.event(pl.id, &progressive.Event{Type: "message", Data: json.RawMessage(d.String())})
			return nil
		})
		pl.port.Set("onmessage", pl.onMsg)
	}
	gone := false
	var un js.Value
	pl.unmount = func() {
		gone = true
		offer.Unmount(un)
	}
	offer.Mount(mount, pl.el, ch.Get("port2"), func(u js.Value) {
		if gone {
			offer.Unmount(u)
			return
		}
		un = u
	})
}

func (p *placements) clear() {
	for id := range p.by {
		p.remove(id)
	}
}

// takeInput gives pl the mouse over it. The terminal listens on elements
// around the layer, so what starts something — a press, a click, a wheel
// turn, a touch — stops at pl: it would otherwise reach the program too. A
// press keeps the focus where it was, so the keys still go to the program.
//
// Moves and releases go on, to the document and the window, where a widget's
// drag (a knob, a slider) follows the pointer: stopped here, a drag begun on
// pl would never move or end. A mouse's are marked handled (preventDefault),
// which the terminal reads as not its own; touches whose start it never saw
// it ignores already.
//
// A placement for the page (Page) stops nothing: everything goes on to the
// page around the terminal, as though the terminal were not there, and all
// of it is marked handled, so the terminal leaves it alone. It is marked on
// the element too (data-websh-page), for a page's own handlers to tell a
// press on it from a press on the terminal's cells.
func (p *placements) takeInput(pl *placement) {
	pl.el.Get("style").Set("pointerEvents", "auto")
	on := func(names []string, h func(e js.Value)) {
		for _, name := range names {
			f := js.FuncOf(func(_ js.Value, args []js.Value) any { h(args[0]); return nil })
			pl.el.Call("addEventListener", name, f)
			pl.stops = append(pl.stops, f)
		}
	}
	handled := func(e js.Value) { e.Call("preventDefault") }
	if pl.d.Page {
		pl.el.Call("setAttribute", "data-websh-page", "")
		on([]string{"mousedown", "mousemove", "mouseup", "click", "dblclick", "contextmenu", "wheel", "touchstart", "touchmove", "touchend"}, handled)
		return
	}
	on([]string{"mousedown", "click", "dblclick", "contextmenu", "wheel", "touchstart"}, func(e js.Value) {
		e.Call("stopPropagation")
		if e.Get("type").String() == "mousedown" {
			e.Call("preventDefault")
		}
	})
	on([]string{"mousemove", "mouseup"}, handled)
}

// post gives the widget in placement id a message from the program.
func (p *placements) post(id string, data []byte) {
	pl := p.by[id]
	if pl == nil || !pl.port.Truthy() || !json.Valid(data) {
		return
	}
	pl.port.Call("postMessage", string(data))
}

// reportClicks tells the program of clicks on a placement that has the mouse
// and asked for events: where on it, and the cell under that point.
func (p *placements) reportClicks(pl *placement) {
	for _, name := range []string{"click", "dblclick", "contextmenu"} {
		f := js.FuncOf(func(_ js.Value, args []js.Value) any {
			e := args[0]
			r := pl.el.Call("getBoundingClientRect")
			w, h := r.Get("width").Float(), r.Get("height").Float()
			if w <= 0 || h <= 0 {
				return nil
			}
			x := (e.Get("clientX").Float() - r.Get("left").Float()) / w
			y := (e.Get("clientY").Float() - r.Get("top").Float()) / h
			ev := &progressive.Event{
				Type: e.Get("type").String(), X: x, Y: y, Button: e.Get("button").Int(),
				Col: pl.d.Col + min(pl.d.W-1, max(0, int(x*float64(pl.d.W)))),
				Row: pl.d.Row + min(pl.d.H-1, max(0, int(y*float64(pl.d.H)))),
			}
			p.s.event(pl.id, ev)
			return nil
		})
		pl.el.Call("addEventListener", name, f)
		pl.stops = append(pl.stops, f)
	}
}
