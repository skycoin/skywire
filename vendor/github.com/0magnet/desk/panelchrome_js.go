//go:build js && wasm

package desk

import (
	"fmt"
	"syscall/js"
)

// The panel, drawn.
//
// Same problem as the window frames and the same answer. The panel is DOM — a
// start button, one task button per window, a clock — so a canvas that is going
// to be sampled as a texture does not have it, and a desktop with no panel is
// missing the thing that makes a collection of windows read as a desktop at
// all.
//
// It is read out of the DOM rather than reimplemented: the labels, which task
// is active and the time all come from the elements the panel already
// maintains, and only the PAINTING is done again. So the clock stays correct
// and a new window's task button appears without this file knowing what a
// window is.

// The panel's colors, from panelCSS. Kept beside the drawing that uses them.
const (
	panelBar      = "#171b24"
	panelBorder   = "#2b313c"
	panelStart    = "#e6e9ee"
	panelTaskBg   = "#222835"
	panelTaskText = "#aeb4be"
	panelActiveBg = "#2f3949"
	panelActiveFg = "#e6e9ee"
	panelActiveUL = "#8fc6f0"
	panelClockFg  = "#8b929c"
)

// panelSnapshot is everything the drawing needs, and everything the cache key
// is made of. Read once per frame; rasterized only when it changes.
type panelSnapshot struct {
	present bool
	r       rect
	start   string
	tasks   []string
	active  int // index into tasks, or -1
	clock   string
	w, h    int // device pixels
}

func (p panelSnapshot) key() string {
	s := fmt.Sprintf("%d|%d|%s|%s|%d|", p.w, p.h, p.start, p.clock, p.active)
	for _, t := range p.tasks {
		s += t + "\x00"
	}
	return s
}

// readPanel finds the panel and reads what is on it.
func (c *compositor) readPanel(view rect) panelSnapshot {
	root := rootElement()
	if !root.Truthy() || root.Get("querySelector").Type() != js.TypeFunction {
		return panelSnapshot{}
	}
	el := root.Call("querySelector", ".dk-panel")
	if !el.Truthy() {
		return panelSnapshot{}
	}
	b := el.Call("getBoundingClientRect")
	// Desk-local, the same translation planFrame does for windows: the GL
	// canvas covers the desk, not the page.
	r := rect{
		X: b.Get("left").Float() - view.X,
		Y: b.Get("top").Float() - view.Y,
		W: b.Get("width").Float(),
		H: b.Get("height").Float(),
	}
	if r.empty() {
		return panelSnapshot{}
	}
	snap := panelSnapshot{
		present: true,
		r:       r,
		active:  -1,
		w:       int(r.W*c.dpr + 0.5),
		h:       int(r.H*c.dpr + 0.5),
	}
	if s := el.Call("querySelector", ".dk-start"); s.Truthy() {
		snap.start = s.Get("textContent").String()
	}
	tasks := el.Call("querySelectorAll", ".dk-task")
	for i := 0; i < tasks.Get("length").Int(); i++ {
		t := tasks.Index(i)
		if t.Get("classList").Call("contains", "active").Bool() {
			snap.active = i
		}
		snap.tasks = append(snap.tasks, t.Get("textContent").String())
	}
	if cl := el.Call("querySelector", ".dk-clock"); cl.Truthy() {
		snap.clock = cl.Get("textContent").String()
	}
	return snap
}

// panelChrome returns a canvas holding the panel, rasterizing only on change.
//
// The clock puts a floor on how often that is: once a minute, which is nothing,
// and is why the key includes it rather than the panel being redrawn every
// frame to be safe.
func (c *compositor) panelChrome(snap panelSnapshot) js.Value {
	if !snap.present || snap.w <= 0 || snap.h <= 0 {
		return js.Value{}
	}
	if got, ok := c.chrome[panelChromeID]; ok && got.key.title == snap.key() && got.canvas.Truthy() {
		return got.canvas
	}
	doc := js.Global().Get("document")
	cv := doc.Call("createElement", "canvas")
	cv.Set("width", snap.w)
	cv.Set("height", snap.h)
	ctx := cv.Call("getContext", "2d")
	if !ctx.Truthy() {
		return js.Value{}
	}
	drawPanelBar(ctx, float64(snap.w), float64(snap.h), c.dpr, snap)
	// Reuses the chrome cache, keyed on the whole snapshot in the title field
	// rather than a second map with the same lifetime.
	c.chrome[panelChromeID] = chromeCache{canvas: cv, key: chromeKey{title: snap.key()}}
	return cv
}

// panelChromeID is the cache slot the panel uses. A window id is "wN", so this
// cannot collide with one.
const panelChromeID = ":panel"

func drawPanelBar(ctx js.Value, w, h, dpr float64, snap panelSnapshot) {
	ctx.Set("fillStyle", panelBar)
	ctx.Call("fillRect", 0, 0, w, h)
	ctx.Set("fillStyle", panelBorder)
	ctx.Call("fillRect", 0, 0, w, dpr) // the top border

	ctx.Set("textBaseline", "middle")
	cy := h / 2

	// The start button, with the dot the CSS gives a gradient and this gives
	// its mean color — a two-stop gradient on a nine-pixel circle is not worth
	// the arithmetic.
	x := 12 * dpr
	ctx.Set("fillStyle", "#9ea6d4")
	ctx.Call("beginPath")
	ctx.Call("arc", x+4.5*dpr, cy, 4.5*dpr, 0, 6.2832)
	ctx.Call("fill")
	x += 16 * dpr
	ctx.Set("fillStyle", panelStart)
	ctx.Set("font", fmt.Sprintf("600 %.0fpx ui-sans-serif, system-ui, sans-serif", 12*dpr))
	ctx.Call("fillText", snap.start, x, cy)
	x += ctx.Call("measureText", snap.start).Get("width").Float() + 14*dpr

	// The task buttons.
	ctx.Set("font", fmt.Sprintf("%.0fpx ui-sans-serif, system-ui, sans-serif", 12*dpr))
	clockW := 46 * dpr
	for i, t := range snap.tasks {
		tw := ctx.Call("measureText", t).Get("width").Float() + 22*dpr
		if max := 190 * dpr; tw > max {
			tw = max
		}
		if x+tw > w-clockW {
			break // out of room; the DOM panel would have scrolled these away
		}
		bg, fg := panelTaskBg, panelTaskText
		if i == snap.active {
			bg, fg = panelActiveBg, panelActiveFg
		}
		ctx.Set("fillStyle", bg)
		roundRect(ctx, x, cy-11*dpr, tw, 22*dpr, 4*dpr)
		ctx.Call("fill")
		if i == snap.active {
			ctx.Set("fillStyle", panelActiveUL)
			ctx.Call("fillRect", x, cy+9*dpr, tw, 2*dpr)
		}
		ctx.Call("save")
		ctx.Call("beginPath")
		ctx.Call("rect", x, 0, tw-8*dpr, h)
		ctx.Call("clip")
		ctx.Set("fillStyle", fg)
		ctx.Call("fillText", t, x+11*dpr, cy)
		ctx.Call("restore")
		x += tw + 5*dpr
	}

	// The clock, right-aligned.
	ctx.Set("fillStyle", panelClockFg)
	ctx.Set("textAlign", "right")
	ctx.Call("fillText", snap.clock, w-10*dpr, cy)
	ctx.Set("textAlign", "left")
}

// roundRect paths a rounded rectangle. Canvas2D grew one of these, but not
// everywhere this runs, and the arithmetic is four arcs.
func roundRect(ctx js.Value, x, y, w, h, r float64) {
	if r > w/2 {
		r = w / 2
	}
	if r > h/2 {
		r = h / 2
	}
	ctx.Call("beginPath")
	ctx.Call("moveTo", x+r, y)
	ctx.Call("arcTo", x+w, y, x+w, y+h, r)
	ctx.Call("arcTo", x+w, y+h, x, y+h, r)
	ctx.Call("arcTo", x, y+h, x, y, r)
	ctx.Call("arcTo", x, y, x+w, y, r)
	ctx.Call("closePath")
}

// hidePanelDOM hides or restores the real panel.
//
// It is not a window, so the compositor's per-window hiding does not reach it,
// and leaving it visible while a copy is drawn into the canvas would show both
// — one flat on the page and one in whatever the canvas is being used for.
func hidePanelDOM(hide bool) {
	// Checked before rootElement, which reaches for document.body when no root
	// has been set — and there is no document under Node, where the tests for
	// the parts that need none run.
	if !js.Global().Get("document").Truthy() {
		return
	}
	root := rootElement()
	if !root.Truthy() || root.Get("querySelector").Type() != js.TypeFunction {
		return
	}
	for _, sel := range []string{".dk-panel", ".dk-menu"} {
		if el := root.Call("querySelector", sel); el.Truthy() {
			if hide {
				el.Get("style").Set("opacity", "0")
			} else {
				el.Get("style").Set("opacity", "")
			}
		}
	}
}
