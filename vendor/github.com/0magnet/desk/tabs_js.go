//go:build js && wasm

// Tabs. A desk window holds a stack of panes rather than exactly one, so a
// second terminal joins the terminal window instead of adding another frame to
// the desk. The strip is hidden while a window has a single pane, so a window
// nobody ever tabbed looks exactly as it did before.
//
// Every window gets its tab structure at creation, even when it will only ever
// hold one pane. Retrofitting a strip later would mean moving live DOM out from
// under a pane that is already mounted, and neither a terminal nor a browser
// survives being reparented — an iframe reloads, and an xterm loses its
// measurements.
package desk

import (
	"fmt"
	"sync"
	"syscall/js"

	winbox "github.com/0magnet/winbox-go"
)

// tabset is the pane stack behind one window.
type tabset struct {
	win    *winbox.WinBox
	strip  js.Value
	views  js.Value
	tabs   []*paneTab
	active int
}

type paneTab struct {
	pane  Pane
	view  js.Value
	btn   js.Value
	label js.Value
	title string
}

var (
	tabMu   sync.Mutex
	tabsets = map[*winbox.WinBox]*tabset{}
)

// tabsetFor returns the pane stack of a window, or nil if it has none (a
// window this package did not open).
func tabsetFor(w *winbox.WinBox) *tabset {
	if w == nil {
		return nil
	}
	tabMu.Lock()
	defer tabMu.Unlock()
	return tabsets[w]
}

// newTabset lays the window's body out as [strip][views] and registers it.
// The body drives the layout with flex so the views box takes whatever height
// the strip does not, which is what a pane measuring itself in pixels reads.
func newTabset(win *winbox.WinBox) *tabset {
	doc := js.Global().Get("document")
	body := win.Body
	// Set only what the layout needs. A wholesale cssText write would destroy
	// the geometry winbox gave the body, floating it up under the title bar so
	// the strip lands on the drag handle and no tab can be clicked.
	bs := body.Get("style")
	if pos := js.Global().Call("getComputedStyle", body).Get("position").String(); pos == "" || pos == "static" {
		bs.Set("position", "relative")
	}
	bs.Set("display", "flex")
	bs.Set("flexDirection", "column")
	bs.Set("overflow", "hidden")

	strip := doc.Call("createElement", "div")
	strip.Get("style").Set("cssText", "display:none;gap:2px;align-items:flex-end;background:#151922;"+
		"border-bottom:1px solid #2a3040;padding:3px 3px 0;min-height:24px;flex:0 0 auto")

	views := doc.Call("createElement", "div")
	views.Get("style").Set("cssText", "position:relative;flex:1 1 auto;min-height:0")

	body.Call("appendChild", strip)
	body.Call("appendChild", views)

	ts := &tabset{win: win, strip: strip, views: views, active: -1}
	tabMu.Lock()
	tabsets[win] = ts
	tabMu.Unlock()
	return ts
}

// dropTabset forgets a window and closes every pane still in it. Called when
// the window itself goes away, so a pane in a background tab is closed too
// rather than leaking its timers and sockets.
func dropTabset(win *winbox.WinBox) {
	tabMu.Lock()
	ts := tabsets[win]
	delete(tabsets, win)
	tabMu.Unlock()
	if ts == nil {
		return
	}
	for _, t := range ts.tabs {
		t.pane.Close()
	}
}

// add mounts a pane as a new tab and brings it to the front. The view is
// positioned before the pane mounts: a pane that measures itself needs a box
// with a size already, and one that styles its host (the browser does) needs
// to find a position it can keep.
func (ts *tabset) add(pane Pane, title string) error {
	doc := js.Global().Get("document")
	view := doc.Call("createElement", "div")
	view.Get("style").Set("cssText", "position:absolute;inset:0;display:none")
	ts.views.Call("appendChild", view)

	t := &paneTab{pane: pane, view: view, title: title}
	// Visible before Mount so the pane measures a real box, not a hidden one.
	view.Get("style").Set("display", "block")
	if err := pane.Mount(view); err != nil {
		ts.views.Call("removeChild", view)
		return err
	}

	t.btn = doc.Call("createElement", "div")
	t.btn.Get("style").Set("cssText", "display:flex;align-items:center;gap:6px;background:#1b2130;"+
		"color:#cdd2da;border:1px solid #2a3040;border-bottom:none;border-radius:5px 5px 0 0;"+
		"padding:3px 8px;cursor:pointer;font:12px monospace;max-width:180px")
	t.label = doc.Call("createElement", "span")
	t.label.Set("textContent", title)
	t.label.Get("style").Set("cssText", "overflow:hidden;text-overflow:ellipsis;white-space:nowrap")
	closer := doc.Call("createElement", "span")
	closer.Set("textContent", "×")
	closer.Get("style").Set("cssText", "opacity:.6;padding:0 2px")
	t.btn.Call("appendChild", t.label)
	t.btn.Call("appendChild", closer)

	idx := len(ts.tabs)
	t.btn.Call("addEventListener", "click", js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) > 0 {
			a[0].Call("stopPropagation")
		}
		ts.activate(ts.indexOf(t))
		return nil
	}))
	closer.Call("addEventListener", "click", js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) > 0 {
			a[0].Call("stopPropagation")
		}
		ts.closeTab(ts.indexOf(t))
		return nil
	}))
	// Before the "+"-less strip's end; the strip holds only tab buttons.
	ts.strip.Call("appendChild", t.btn)

	ts.tabs = append(ts.tabs, t)
	ts.activate(idx)
	ts.sync()
	return nil
}

// indexOf finds a tab's current position, which shifts as tabs close.
func (ts *tabset) indexOf(t *paneTab) int {
	for i, x := range ts.tabs {
		if x == t {
			return i
		}
	}
	return -1
}

// activate shows one tab and hides the rest, and titles the window after it —
// a window's title is whatever is in front of it.
func (ts *tabset) activate(i int) {
	if i < 0 || i >= len(ts.tabs) {
		return
	}
	ts.active = i
	for j, t := range ts.tabs {
		on := j == i
		if on {
			t.view.Get("style").Set("display", "block")
			t.btn.Get("style").Set("background", "#2a3040")
		} else {
			t.view.Get("style").Set("display", "none")
			t.btn.Get("style").Set("background", "transparent")
		}
	}
	if ts.win != nil {
		ts.win.SetTitle(ts.tabs[i].title)
	}
}

// closeTab removes one pane. Closing the last one closes the window, which is
// what a person means by closing the only thing in it.
func (ts *tabset) closeTab(i int) {
	if i < 0 || i >= len(ts.tabs) {
		return
	}
	if len(ts.tabs) == 1 {
		if ts.win != nil {
			ts.win.Close(false)
		}
		return
	}
	t := ts.tabs[i]
	t.pane.Close()
	ts.views.Call("removeChild", t.view)
	ts.strip.Call("removeChild", t.btn)
	ts.tabs = append(ts.tabs[:i], ts.tabs[i+1:]...)
	if ts.active >= len(ts.tabs) {
		ts.active = len(ts.tabs) - 1
	}
	ts.activate(ts.active)
	ts.sync()
}

// sync hides the strip while there is nothing to choose between.
func (ts *tabset) sync() {
	if len(ts.tabs) > 1 {
		ts.strip.Get("style").Set("display", "flex")
	} else {
		ts.strip.Get("style").Set("display", "none")
	}
}

// resize forwards the views box's size to the front pane. The pane's box is
// the window minus the strip, so a pane that lays itself out in pixels must be
// told the inner size rather than the window's.
func (ts *tabset) resize() {
	if ts.active < 0 || ts.active >= len(ts.tabs) {
		return
	}
	if r, ok := ts.tabs[ts.active].pane.(Resizer); ok {
		r.Resize(ts.views.Get("clientWidth").Float(), ts.views.Get("clientHeight").Float())
	}
}

// ShowTab brings the first tab with this title to the front, reporting whether
// it found one. Titles are how a host names its own tabs, so they are the
// handle it already holds.
func ShowTab(win *winbox.WinBox, title string) bool {
	ts := tabsetFor(win)
	if ts == nil {
		return false
	}
	for i, t := range ts.tabs {
		if t.title == title {
			ts.activate(i)
			return true
		}
	}
	return false
}

// AddTab mounts a new pane of the named app as a tab of an existing window and
// brings it to the front. The window must be one this package opened. It is
// the tabbed counterpart of Launch: same app registry, same arguments.
func AddTab(win *winbox.WinBox, name string, args ...string) error {
	return AddTabOpts(win, name, Options{}, args...)
}

// AddTabOpts is AddTab with the tab's title spelled out; only Options.Title is
// consulted, since a tab has no geometry of its own.
func AddTabOpts(win *winbox.WinBox, name string, opt Options, args ...string) error {
	ts := tabsetFor(win)
	if ts == nil {
		return fmt.Errorf("desk: window has no tab set — it was not opened by this package")
	}
	app, ok := Lookup(name)
	if !ok {
		return fmt.Errorf("desk: no app named %q", name)
	}
	if app.Open == nil {
		return fmt.Errorf("desk: app %q has nothing to mount", name)
	}
	pane, err := app.Open(args)
	if err != nil {
		return err
	}
	title := app.Title
	if opt.Title != "" {
		title = opt.Title
	}
	return ts.add(pane, title)
}
