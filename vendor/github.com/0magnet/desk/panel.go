//go:build js && wasm

package desk

import (
	"syscall/js"
	"time"

	"github.com/0magnet/desk/dom"
)

const panelCSS = `
.dk-panel { position:absolute; left:0; right:0; bottom:0; height:34px; z-index:100000;
            display:flex; align-items:stretch; gap:6px; padding:0 6px;
            background:#171b24; border-top:1px solid #2b313c;
            font:12px/1 ui-sans-serif,system-ui,sans-serif; color:#c8ccd4;
            user-select:none; }
.dk-start { display:flex; align-items:center; gap:7px; padding:0 12px; cursor:pointer;
            color:#e6e9ee; font-weight:600; }
.dk-start:hover, .dk-start.open { background:#232936; }
.dk-start-dot { width:9px; height:9px; border-radius:50%;
                background:linear-gradient(135deg,#8fc6f0,#ad7fa8); }
.dk-tasks { flex:1 1 auto; display:flex; align-items:center; gap:5px; overflow:hidden; }
.dk-task { max-width:190px; padding:5px 11px; border-radius:4px; cursor:pointer;
           background:#222835; color:#aeb4be; overflow:hidden;
           text-overflow:ellipsis; white-space:nowrap; }
.dk-task:hover { background:#2b3341; }
.dk-task.active { background:#2f3949; color:#e6e9ee; box-shadow:inset 0 -2px 0 #8fc6f0; }
.dk-clock { display:flex; align-items:center; padding:0 10px; color:#8b929c;
            font-variant-numeric:tabular-nums; }

.dk-menu { position:absolute; left:6px; bottom:38px; z-index:100001; width:250px;
           background:#1b2028; border:1px solid #2f3744; border-radius:6px;
           box-shadow:0 12px 34px rgba(0,0,0,.55); overflow:hidden;
           font:13px/1 ui-sans-serif,system-ui,sans-serif; }
.dk-menu[hidden] { display:none; }
.dk-menu-search { width:100%; box-sizing:border-box; padding:9px 11px; border:0;
                  border-bottom:1px solid #2f3744; background:#151a21; color:#d3d7cf;
                  font:13px/1 ui-sans-serif,system-ui,sans-serif; outline:none; }
.dk-menu-list { max-height:320px; overflow:auto; padding:4px; }
.dk-item { display:flex; flex-direction:column; gap:3px; padding:7px 9px;
           border-radius:4px; cursor:pointer; }
.dk-item:hover, .dk-item.sel { background:#2a3242; }
.dk-item-name { color:#e6e9ee; }
.dk-item-help { color:#7d8694; font-size:11px; }
.dk-menu-empty { padding:12px; color:#7d8694; }
`

// Panel is the bar along the bottom: a menu on the left, a button per window in
// the middle, a clock on the right.
//
// It is xfce4's panel in outline and not much more. What makes a desktop read
// as one is having somewhere to start things from and somewhere to get back to
// a window you buried — workspaces, applets and the rest are refinements on top
// of those two, and are left out.
type Panel struct {
	root, tasks, menu, search, list, clock js.Value
	fns                                    dom.Funcs

	items    []*task
	selected int
	filtered []App
	open     bool
	ticker   *time.Ticker
	stop     chan struct{}
}

type task struct {
	win *Window
	el  js.Value
}

// Window is a launched window the panel tracks.
type Window struct {
	Title string
	Focus func()
	Alive func() bool
}

// PanelHeight is how much of the desk the panel occupies.
const PanelHeight = 34

var panel *Panel

// NewPanel builds the panel inside the desk root and starts tracking windows.
func NewPanel() *Panel {
	dom.Stylesheet("desk-panel-css", panelCSS)

	p := &Panel{stop: make(chan struct{})}
	panel = p

	p.tasks = dom.El("div", dom.Class("dk-tasks"))
	p.clock = dom.El("div", dom.Class("dk-clock"))

	start := dom.El("div", dom.Class("dk-start"),
		dom.Child(dom.El("span", dom.Class("dk-start-dot"))),
		dom.Child(dom.El("span", dom.Text("Applications"))),
		p.fns.On("click", func(ev js.Value) {
			ev.Call("stopPropagation")
			p.toggle()
		}))

	p.search = dom.El("input", dom.Class("dk-menu-search"),
		dom.Attr("placeholder", "type to filter"), dom.Attr("spellcheck", "false"))
	p.list = dom.El("div", dom.Class("dk-menu-list"))
	p.menu = dom.El("div", dom.Class("dk-menu"), dom.Attr("hidden", ""),
		dom.Child(p.search), dom.Child(p.list),
		p.fns.On("click", func(ev js.Value) { ev.Call("stopPropagation") }))

	p.search.Call("addEventListener", "input", js.FuncOf(func(js.Value, []js.Value) any {
		p.refilter()
		return nil
	}))
	p.search.Call("addEventListener", "keydown", js.FuncOf(func(_ js.Value, args []js.Value) any {
		p.key(args[0])
		return nil
	}))

	p.root = dom.El("div", dom.Class("dk-panel"),
		dom.Child(start), dom.Child(p.tasks), dom.Child(p.clock))

	host := rootElement()
	host.Call("appendChild", p.menu)
	host.Call("appendChild", p.root)

	// A click anywhere else dismisses the menu, which is what every menu
	// everywhere does and is immediately missed when it is absent.
	js.Global().Get("document").Call("addEventListener", "click",
		js.FuncOf(func(js.Value, []js.Value) any {
			if p.open {
				p.setOpen(false)
			}
			return nil
		}))

	p.tick()
	go p.runClock()
	return p
}

func rootElement() js.Value {
	mu.Lock()
	r := root
	mu.Unlock()
	if r.Truthy() {
		return r
	}
	return js.Global().Get("document").Get("body")
}

func (p *Panel) toggle() { p.setOpen(!p.open) }

func (p *Panel) setOpen(open bool) {
	p.open = open
	if open {
		p.menu.Call("removeAttribute", "hidden")
		p.search.Set("value", "")
		p.selected = 0
		p.refilter()
		p.search.Call("focus")
	} else {
		p.menu.Call("setAttribute", "hidden", "")
	}
}

func (p *Panel) refilter() {
	q := p.search.Get("value").String()
	p.filtered = p.filtered[:0]
	for _, a := range Apps() {
		if q == "" || containsFold(a.Name, q) || containsFold(a.Help, q) {
			p.filtered = append(p.filtered, a)
		}
	}
	if p.selected >= len(p.filtered) {
		p.selected = 0
	}
	p.drawMenu()
}

func (p *Panel) drawMenu() {
	dom.Clear(p.list)
	if len(p.filtered) == 0 {
		p.list.Call("appendChild",
			dom.El("div", dom.Class("dk-menu-empty"), dom.Text("nothing matches")))
		return
	}
	for i, a := range p.filtered {
		name := a.Name
		cls := "dk-item"
		if i == p.selected {
			cls += " sel"
		}
		help := a.Help
		if help == "" {
			help = a.Title
		}
		p.list.Call("appendChild", dom.El("div", dom.Class(cls),
			dom.Child(dom.El("div", dom.Class("dk-item-name"), dom.Text(a.Title))),
			dom.Child(dom.El("div", dom.Class("dk-item-help"), dom.Text(help))),
			p.fns.On("click", func(js.Value) {
				p.setOpen(false)
				launchFromMenu(name)
			})))
	}
}

func (p *Panel) key(ev js.Value) {
	switch ev.Get("key").String() {
	case "Escape":
		p.setOpen(false)
	case "ArrowDown":
		ev.Call("preventDefault")
		if p.selected < len(p.filtered)-1 {
			p.selected++
			p.drawMenu()
		}
	case "ArrowUp":
		ev.Call("preventDefault")
		if p.selected > 0 {
			p.selected--
			p.drawMenu()
		}
	case "Enter":
		if p.selected < len(p.filtered) {
			name := p.filtered[p.selected].Name
			p.setOpen(false)
			launchFromMenu(name)
		}
	}
}

func launchFromMenu(name string) {
	if _, err := Launch(name); err != nil {
		js.Global().Get("console").Call("warn", "desk: "+err.Error())
	}
}

// SetActive marks which window the task buttons should show as focused. Only
// one is active at a time, so this clears the others rather than trusting every
// window to report its own blur.
func (p *Panel) SetActive(w *Window) {
	for _, t := range p.items {
		cls := "dk-task"
		if t.win == w {
			cls += " active"
		}
		t.el.Set("className", cls)
	}
}

// Track adds a window to the task list.
func (p *Panel) Track(w *Window) {
	el := dom.El("div", dom.Class("dk-task"), dom.Text(w.Title),
		p.fns.On("click", func(js.Value) {
			if w.Focus != nil {
				w.Focus()
			}
		}))
	p.items = append(p.items, &task{win: w, el: el})
	p.tasks.Call("appendChild", el)
}

// tick drops buttons whose windows have closed and refreshes the clock.
func (p *Panel) tick() {
	live := p.items[:0]
	for _, t := range p.items {
		if t.win.Alive != nil && !t.win.Alive() {
			p.tasks.Call("removeChild", t.el)
			continue
		}
		t.el.Set("textContent", t.win.Title)
		live = append(live, t)
	}
	p.items = live
	p.clock.Set("textContent", time.Now().Format("15:04"))
}

func (p *Panel) runClock() {
	p.ticker = time.NewTicker(time.Second)
	defer p.ticker.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-p.ticker.C:
			p.tick()
		}
	}
}

// Close releases the panel.
func (p *Panel) Close() {
	close(p.stop)
	p.fns.Release()
}

func containsFold(s, sub string) bool {
	s, sub = lower(s), lower(sub)
	return len(sub) == 0 || indexOf(s, sub) >= 0
}

func lower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
