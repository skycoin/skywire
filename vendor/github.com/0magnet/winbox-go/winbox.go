//go:build js && wasm

// Package winbox is a Go/WebAssembly port of WinBox.js
// (https://github.com/nextapps-de/winbox), a modern HTML5 window manager
// for the web. It manipulates the DOM through syscall/js and compiles with
// both the standard Go toolchain (GOOS=js GOARCH=wasm) and TinyGo
// (-target wasm). The stylesheet ships embedded (icons inlined) and is
// injected automatically when the first window is created.
package winbox

import (
	"fmt"
	"math"
	"strings"
	"syscall/js"
)

type unitKind uint8

const (
	unitUnset unitKind = iota
	unitPx
	unitPct
	unitCenter
	unitEdge
)

// Unit is a position or dimension value: pixels, a percentage of the
// available space, or a keyword (Center, Right, Bottom). The zero value
// means "not set" and picks the same default the original library would.
type Unit struct {
	kind unitKind
	v    float64
}

// Px returns a Unit of v pixels.
func Px(v float64) Unit { return Unit{unitPx, v} }

// Pct returns a Unit of v percent of the available space.
func Pct(v float64) Unit { return Unit{unitPct, v} }

var (
	// Center centers the window on the given axis.
	Center = Unit{kind: unitCenter, v: 0}
	// Right aligns the window to the right edge (x axis).
	Right = Unit{kind: unitEdge, v: 0}
	// Bottom aligns the window to the bottom edge (y axis).
	Bottom = Unit{kind: unitEdge, v: 0}
)

// falsy mirrors the JS truthiness check on option values: unset or 0px.
func (u Unit) falsy() bool {
	return u.kind == unitUnset || (u.kind == unitPx && u.v == 0)
}

func (u Unit) parse(base, center float64) float64 {
	switch u.kind {
	case unitCenter:
		return math.Trunc((base-center)/2 + 0.5)
	case unitEdge:
		return base - center
	case unitPct:
		return math.Trunc(base/100*u.v + 0.5)
	default:
		return u.v
	}
}

// Control describes a custom titlebar button for AddControl.
type Control struct {
	Class string
	Image string
	Index int
	Click func(event js.Value, w *WinBox)
}

// Options configures a new window. All fields are optional.
type Options struct {
	ID       string
	Index    int // initial z-index (0 = auto)
	Root     js.Value
	Template js.Value
	Title    string
	Icon     string
	Mount    js.Value
	HTML     string
	URL      string

	Width     Unit
	Height    Unit
	MinWidth  Unit
	MinHeight Unit
	MaxWidth  Unit
	MaxHeight Unit
	Autosize  bool
	Overflow  bool

	X Unit
	Y Unit

	Top    Unit
	Left   Unit
	Bottom Unit
	Right  Unit

	Min    bool
	Max    bool
	Hidden bool
	Modal  bool

	// Dock pins the window to an edge of the viewport. The zero value,
	// EdgeNone, leaves it floating — docking is opt-in and changes nothing for
	// callers that do not ask for it. DockSize is the thickness across that
	// edge, defaulting to whatever the window's extent along that axis would
	// have been. See dock.go.
	Dock     Edge
	DockSize Unit
	DockMode DockMode

	Background string
	Border     float64
	Header     float64
	Class      []string

	OnCreate     func(w *WinBox)
	OnClose      func(w *WinBox, force bool) bool
	OnFocus      func(w *WinBox)
	OnBlur       func(w *WinBox)
	OnMove       func(w *WinBox, x, y float64)
	OnResize     func(w *WinBox, width, height float64)
	OnFullscreen func(w *WinBox)
	OnMaximize   func(w *WinBox)
	OnMinimize   func(w *WinBox)
	OnRestore    func(w *WinBox)
	OnHide       func(w *WinBox)
	OnShow       func(w *WinBox)
	OnLoad       func(w *WinBox)
	OnDock       func(w *WinBox, edge Edge)
	OnUndock     func(w *WinBox)
}

// WinBox is one window instance.
type WinBox struct {
	ID   string
	DOM  js.Value // the outer ".winbox" element
	Body js.Value // the ".wb-body" content element

	X, Y                float64
	Width, Height       float64
	MinWidth, MinHeight float64
	// MaxWidth and MaxHeight cap the window, or are zero for no cap — in which
	// case the limit is whatever the viewport allows at the time, recomputed as
	// the page changes size. They are not filled in with the viewport at
	// construction: a cap taken from how big the page was once outlives the
	// page it was measured from, and the window then refuses to be dragged any
	// larger after a zoom or a rotation even though there is room.
	MaxWidth, MaxHeight float64
	Top, Right          float64
	Bottom, Left        float64
	Header              float64
	Index               int
	Overflow            bool
	Title               string

	Min     bool
	Max     bool
	Full    bool
	Hidden  bool
	Focused bool

	// Docking state. dock is EdgeNone unless Dock was asked for; dockThick is
	// the extent across the docked edge, and preDock is the floating geometry
	// to put back on Undock. See dock.go.
	dock      Edge
	dockThick float64
	dockMode  DockMode
	preDock   dockGeom

	// Callbacks may be replaced at any time after creation.
	OnClose      func(w *WinBox, force bool) bool
	OnFocus      func(w *WinBox)
	OnBlur       func(w *WinBox)
	OnMove       func(w *WinBox, x, y float64)
	OnResize     func(w *WinBox, width, height float64)
	OnFullscreen func(w *WinBox)
	OnMaximize   func(w *WinBox)
	OnMinimize   func(w *WinBox)
	OnRestore    func(w *WinBox)
	OnHide       func(w *WinBox)
	OnShow       func(w *WinBox)
	OnLoad       func(w *WinBox)
	OnDock       func(w *WinBox, edge Edge)
	OnUndock     func(w *WinBox)

	funcs []js.Func
}

// New creates and shows a new window. A nil opts creates a default window.
func New(opts *Options) *WinBox {
	if opts == nil {
		opts = &Options{}
	}

	if !body.Truthy() {
		setup()
	}

	w := &WinBox{}

	x := opts.X
	y := opts.Y
	if x.falsy() && opts.Modal {
		x = Center
	}
	if y.falsy() && opts.Modal {
		y = Center
	}

	w.DOM = template(opts.Template)

	idCounter++
	w.ID = opts.ID
	if w.ID == "" {
		w.ID = fmt.Sprintf("winbox-%d", idCounter)
	}
	w.DOM.Set("id", w.ID)

	className := "winbox"
	if len(opts.Class) > 0 {
		className += " " + strings.Join(opts.Class, " ")
	}
	if opts.Modal {
		className += " modal"
	}
	w.DOM.Set("className", className)
	w.DOM.Set("winbox", w.ID)

	w.Body = getByClass(w.DOM, "wb-body")
	w.Header = 35
	if opts.Header != 0 {
		w.Header = opts.Header
	}

	stackWin = append(stackWin, w)

	if opts.Background != "" {
		w.SetBackground(opts.Background)
	}

	border := opts.Border
	if border != 0 {
		setStyle(w.Body, "margin", fmt.Sprintf("%vpx", border))
	}

	if opts.Header != 0 {
		node := getByClass(w.DOM, "wb-header")
		px := fmt.Sprintf("%vpx", opts.Header)
		setStyle(node, "height", px)
		setStyle(node, "line-height", px)
		setStyle(w.Body, "top", px)
	}

	if opts.Title != "" {
		w.SetTitle(opts.Title)
	}

	if opts.Icon != "" {
		w.SetIcon(opts.Icon)
	}

	if opts.Mount.Truthy() {
		w.Mount(opts.Mount)
	} else if opts.HTML != "" {
		w.Body.Set("innerHTML", opts.HTML)
	} else if opts.URL != "" {
		w.OnLoad = opts.OnLoad
		w.setURL(opts.URL)
	}

	var top, bottom, left, right float64
	if !opts.Top.falsy() {
		top = opts.Top.parse(rootH, 0)
	}
	if !opts.Bottom.falsy() {
		bottom = opts.Bottom.parse(rootH, 0)
	}
	if !opts.Left.falsy() {
		left = opts.Left.parse(rootW, 0)
	}
	if !opts.Right.falsy() {
		right = opts.Right.parse(rootW, 0)
	}

	viewportW := rootW - left - right
	viewportH := rootH - top - bottom

	// maxWidth and maxHeight size the window at birth; MaxWidth and MaxHeight
	// cap it forever after, and the two must not be confused. Only a caller
	// that asked for a maximum gets one: the viewport is not a maximum, it is
	// how big the page happened to be at this moment, and freezing it as a cap
	// is what stops a window being dragged any bigger after the page grows —
	// by a zoom, a rotation, or a window being made larger.
	maxWidth := viewportW
	if !opts.MaxWidth.falsy() {
		maxWidth = opts.MaxWidth.parse(viewportW, 0)
		w.MaxWidth = maxWidth
	}
	maxHeight := viewportH
	if !opts.MaxHeight.falsy() {
		maxHeight = opts.MaxHeight.parse(viewportH, 0)
		w.MaxHeight = maxHeight
	}
	minWidth := 150.0
	if !opts.MinWidth.falsy() {
		minWidth = opts.MinWidth.parse(maxWidth, 0)
	}
	minHeight := w.Header
	if !opts.MinHeight.falsy() {
		minHeight = opts.MinHeight.parse(maxHeight, 0)
	}

	var width, height float64

	if opts.Autosize {
		root := opts.Root
		if !root.Truthy() {
			root = body
		}
		root.Call("appendChild", w.Body)

		width = math.Max(math.Min(w.Body.Get("clientWidth").Float()+border*2+1, maxWidth), minWidth)
		height = math.Max(math.Min(w.Body.Get("clientHeight").Float()+w.Header+border+1, maxHeight), minHeight)

		w.DOM.Call("appendChild", w.Body)
	} else {
		width = math.Trunc(math.Max(maxWidth/2, minWidth))
		if !opts.Width.falsy() {
			width = opts.Width.parse(maxWidth, 0)
		}
		height = math.Trunc(math.Max(maxHeight/2, minHeight))
		if !opts.Height.falsy() {
			height = opts.Height.parse(maxHeight, 0)
		}
	}

	xv := left
	if !x.falsy() {
		xv = x.parse(viewportW, width)
	}
	yv := top
	if !y.falsy() {
		yv = y.parse(viewportH, height)
	}

	w.X = xv
	w.Y = yv
	w.Width = width
	w.Height = height
	w.MinWidth = minWidth
	w.MinHeight = minHeight
	w.Top = top
	w.Right = right
	w.Bottom = bottom
	w.Left = left
	w.Overflow = opts.Overflow

	w.OnClose = opts.OnClose
	w.OnFocus = opts.OnFocus
	w.OnBlur = opts.OnBlur
	w.OnMove = opts.OnMove
	w.OnResize = opts.OnResize
	w.OnFullscreen = opts.OnFullscreen
	w.OnMaximize = opts.OnMaximize
	w.OnMinimize = opts.OnMinimize
	w.OnRestore = opts.OnRestore
	w.OnHide = opts.OnHide
	w.OnShow = opts.OnShow
	w.OnDock = opts.OnDock
	w.OnUndock = opts.OnUndock
	if w.OnLoad == nil {
		w.OnLoad = opts.OnLoad
	}

	if opts.Hidden {
		w.Hide()
	} else {
		w.Focus()
	}

	if opts.Index != 0 {
		w.Index = opts.Index
		setStyle(w.DOM, "z-index", fmt.Sprintf("%d", opts.Index))
		if opts.Index > indexCounter {
			indexCounter = opts.Index
		}
	}

	w.dockMode = opts.DockMode

	switch {
	case opts.Dock != EdgeNone:
		// Placed before docking so the geometry Undock restores is the one the
		// options described, not whatever the strip happens to be.
		w.applySize()
		w.applyPos()
		w.Dock(opts.Dock, opts.DockSize)
	case opts.Max:
		w.Maximize()
	case opts.Min:
		w.Minimize()
	default:
		w.applySize()
		w.applyPos()
	}

	register(w)

	root := opts.Root
	if !root.Truthy() {
		root = body
	}
	root.Call("appendChild", w.DOM)

	if opts.OnCreate != nil {
		opts.OnCreate(w)
	}

	return w
}

// Mount moves the given DOM element into the window body. The element's
// original parent is remembered so Unmount can put it back.
func (w *WinBox) Mount(src js.Value) *WinBox {
	// handles mounting over
	w.Unmount()

	if !src.Get("_backstore").Truthy() {
		src.Set("_backstore", src.Get("parentNode"))
	}
	w.Body.Set("textContent", "")
	w.Body.Call("appendChild", src)

	return w
}

// Unmount moves the mounted element back to its original parent, or to
// dest when given.
func (w *WinBox) Unmount(dest ...js.Value) *WinBox {
	node := w.Body.Get("firstChild")

	if node.Truthy() {
		var d js.Value
		if len(dest) > 0 {
			d = dest[0]
		}
		root := d
		if !root.Truthy() {
			root = node.Get("_backstore")
		}
		if root.Truthy() {
			root.Call("appendChild", node)
		}
		if d.Truthy() {
			node.Set("_backstore", d)
		} else {
			node.Set("_backstore", js.Undefined())
		}
	}

	return w
}

// SetTitle sets the titlebar text.
func (w *WinBox) SetTitle(title string) *WinBox {
	w.Title = title
	node := getByClass(w.DOM, "wb-title")
	setText(node, title)
	return w
}

// SetIcon sets the titlebar icon from an image URL (data URIs work too).
func (w *WinBox) SetIcon(src string) *WinBox {
	img := getByClass(w.DOM, "wb-icon")
	setStyle(img, "background-image", "url("+src+")")
	setStyle(img, "display", "inline-block")
	return w
}

// SetBackground sets the window background (any CSS background value).
func (w *WinBox) SetBackground(background string) *WinBox {
	setStyle(w.DOM, "background", background)
	return w
}

// SetURL loads a URL into the window body via an iframe. The window's
// OnLoad callback (if any) fires when a newly created iframe finished
// loading.
func (w *WinBox) SetURL(url string) *WinBox {
	return w.setURL(url)
}

func (w *WinBox) setURL(url string) *WinBox {
	node := w.Body.Get("firstChild")

	if node.Truthy() && strings.EqualFold(node.Get("tagName").String(), "iframe") {
		node.Set("src", url)
	} else {
		w.Body.Set("innerHTML", `<iframe src="`+url+`"></iframe>`)
		if w.OnLoad != nil {
			f := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
				if w.OnLoad != nil {
					w.OnLoad(w)
				}
				return nil
			})
			w.funcs = append(w.funcs, f)
			w.Body.Get("firstChild").Set("onload", f)
		}
	}

	return w
}

// Focus brings the window to front and marks it focused.
func (w *WinBox) Focus() *WinBox {
	if !w.Focused {
		if len(stackWin) > 1 {
			for i := len(stackWin) - 1; i >= 0; i-- {
				lastFocus := stackWin[i]
				if lastFocus.Focused {
					lastFocus.Blur()
					for j, s := range stackWin {
						if s == w {
							stackWin = append(stackWin[:j], stackWin[j+1:]...)
							stackWin = append(stackWin, w)
							break
						}
					}
					break
				}
			}
		}

		indexCounter++
		setStyle(w.DOM, "z-index", fmt.Sprintf("%d", indexCounter))
		w.Index = indexCounter
		w.AddClass("focus")
		w.Focused = true
		if w.OnFocus != nil {
			w.OnFocus(w)
		}
	}

	return w
}

// Blur removes focus from the window.
func (w *WinBox) Blur() *WinBox {
	if w.Focused {
		w.RemoveClass("focus")
		w.Focused = false
		if w.OnBlur != nil {
			w.OnBlur(w)
		}
	}

	return w
}

// Hide makes the window invisible without closing it.
func (w *WinBox) Hide() *WinBox {
	if !w.Hidden {
		if w.OnHide != nil {
			w.OnHide(w)
		}
		w.Hidden = true
		w.AddClass("hide")
		// A dock put away gives its strip back to everyone else.
		if len(stackDock) > 0 {
			updateDocks()
		}
	}
	return w
}

// Show makes a hidden window visible again.
func (w *WinBox) Show() *WinBox {
	if w.Hidden {
		if w.OnShow != nil {
			w.OnShow(w)
		}
		w.Hidden = false
		w.RemoveClass("hide")
		if len(stackDock) > 0 {
			updateDocks()
		}
	}
	return w
}

// Minimize collapses the window into the taskbar area at the bottom.
func (w *WinBox) Minimize() *WinBox {
	if isFullscreen != nil {
		cancelFullscreen()
	}

	if w.Max {
		w.RemoveClass("max")
		w.Max = false
	}

	if !w.Min {
		// THE FLAG GOES UP BEFORE ANYTHING MOVES THE WINDOW. updateMinStack parks
		// a minimized window in its slot along the bottom by the same path a drag
		// takes, so OnMove and OnResize fire with the SLOT's geometry — a title
		// bar's worth of height, at the left edge. Persisting geometry from those
		// callbacks is what they are for, and with the flag still false there was
		// no way for a callback to tell being PARKED from being ARRANGED: an app
		// saved the slot as the window's remembered size, the window came back
		// from the minimize as a bare title bar, and it came back that way on
		// every later load too, because the wrong size had been written to
		// storage. Setting Min first is the whole fix.
		stackMin = append(stackMin, w)
		w.AddClass("min")
		w.Min = true
		updateMinStack()
		w.DOM.Set("title", w.Title)

		// A minimized dock stops reserving its strip, so the docks behind it and
		// the content area both grow.
		if w.dock != EdgeNone {
			updateDocks()
		}

		if w.Focused {
			w.Blur()
			focusNext()
		}

		if w.OnMinimize != nil {
			w.OnMinimize(w)
		}
	}

	return w
}

// Restore returns a minimized or maximized window to its normal state.
func (w *WinBox) Restore() *WinBox {
	if isFullscreen != nil {
		cancelFullscreen()
	}

	if w.Min {
		removeMinStack(w)
		// A window that was docked when it was minimized goes back to its edge,
		// not to the floating geometry Undock is holding for it.
		if w.dock != EdgeNone {
			updateDocks()
		} else {
			w.applySize()
			w.applyPos()
		}
		if w.OnRestore != nil {
			w.OnRestore(w)
		}
	}

	if w.Max {
		w.Max = false
		w.RemoveClass("max")
		w.applySize()
		w.applyPos()
		if w.OnRestore != nil {
			w.OnRestore(w)
		}
	}

	return w
}

// Maximize expands the window to fill the viewport (minus Top/Right/
// Bottom/Left offsets).
func (w *WinBox) Maximize() *WinBox {
	if isFullscreen != nil {
		cancelFullscreen()
	}

	if w.Min {
		removeMinStack(w)
	}

	if w.dock != EdgeNone {
		w.Undock()
	}

	if !w.Max {
		w.AddClass("max")
		// Before the raw calls, for the reason spelled out in Minimize: these
		// fire OnResize and OnMove, and an app that persists what they report
		// would remember the whole viewport as the window's own size — after
		// which Restore has nothing smaller to go back to.
		w.Max = true
		// The area left by any reserving docks, which with none of them is the
		// whole viewport and so the original's arithmetic exactly.
		cx, cy, cw, ch := dockContentBox()
		w.resizeRaw(cw-w.Left-w.Right, ch-w.Top-w.Bottom)
		w.moveRaw(cx+w.Left, cy+w.Top)
		if w.OnMaximize != nil {
			w.OnMaximize(w)
		}
	}

	return w
}

// Fullscreen toggles browser fullscreen on the window body.
func (w *WinBox) Fullscreen() *WinBox {
	if w.Min {
		removeMinStack(w)
		w.applySize()
		w.applyPos()
	}

	// fullscreen could be changed by the user manually!
	if isFullscreen == nil || !cancelFullscreen() {
		// requestFullscreen runs async and returns a promise; set the state
		// after it was fired, since a browser without proper fullscreen
		// support may bypass it silently
		w.Body.Call(prefixRequest)
		isFullscreen = w
		w.Full = true
		if w.OnFullscreen != nil {
			w.OnFullscreen(w)
		}
	}

	return w
}

// Close removes the window. When the OnClose callback returns true the
// close is canceled; Close then returns true. Pass force to signal the
// callback that closing must not be prevented.
func (w *WinBox) Close(force bool) bool {
	// Closing clears DOM and Body below, so a second close would call Unmount
	// on an undefined element and panic — taking the page with it. A host that
	// closes from both a button and an event reaches this, and the second call
	// has nothing left to do.
	if !w.DOM.Truthy() {
		return false
	}

	if w.OnClose != nil && w.OnClose(w, force) {
		return true
	}

	if w.Min {
		removeMinStack(w)
	}
	if w.dock != EdgeNone {
		w.dock = EdgeNone
		for i, d := range stackDock {
			if d == w {
				stackDock = append(stackDock[:i], stackDock[i+1:]...)
				break
			}
		}
		defer updateDocks()
	}

	for i, s := range stackWin {
		if s == w {
			stackWin = append(stackWin[:i], stackWin[i+1:]...)
			break
		}
	}

	w.Unmount()
	w.DOM.Call("remove")
	w.DOM.Set("textContent", "")
	w.DOM.Set("winbox", js.Null())

	for _, f := range w.funcs {
		f.Release()
	}
	w.funcs = nil

	w.Body = js.Value{}
	w.DOM = js.Value{}

	if w.Focused {
		focusNext()
	}

	return false
}

// Move sets the window position. Zero-value Units re-apply the stored
// position (useful after changing X/Y directly).
func (w *WinBox) Move(x, y Unit) *WinBox {
	var xv, yv float64
	if x.kind == unitUnset && y.kind == unitUnset {
		xv = w.X
		yv = w.Y
	} else {
		if !x.falsy() {
			xv = x.parse(rootW-w.Left-w.Right, w.Width)
		}
		if !y.falsy() {
			yv = y.parse(rootH-w.Top-w.Bottom, w.Height)
		}
		w.X = xv
		w.Y = yv
	}

	setStyle(w.DOM, "left", fmt.Sprintf("%vpx", xv))
	setStyle(w.DOM, "top", fmt.Sprintf("%vpx", yv))

	if w.OnMove != nil {
		w.OnMove(w, xv, yv)
	}
	return w
}

// Resize sets the window size. Zero-value Units re-apply the stored size
// (useful after changing Width/Height directly).
func (w *WinBox) Resize(width, height Unit) *WinBox {
	var wv, hv float64
	if width.kind == unitUnset && height.kind == unitUnset {
		wv = w.Width
		hv = w.Height
	} else {
		// A percentage is of the room the window has now, not of a maximum it
		// may not have been given.
		if !width.falsy() {
			wv = width.parse(rootW-w.Left-w.Right, 0)
		}
		if !height.falsy() {
			hv = height.parse(rootH-w.Top-w.Bottom, 0)
		}
		if w.MaxWidth > 0 {
			wv = math.Min(wv, w.MaxWidth)
		}
		if w.MaxHeight > 0 {
			hv = math.Min(hv, w.MaxHeight)
		}
		w.Width = wv
		w.Height = hv

		wv = math.Max(wv, w.MinWidth)
		hv = math.Max(hv, w.MinHeight)
	}

	setStyle(w.DOM, "width", fmt.Sprintf("%vpx", wv))
	setStyle(w.DOM, "height", fmt.Sprintf("%vpx", hv))

	if w.OnResize != nil {
		w.OnResize(w, wv, hv)
	}
	return w
}

// applyPos re-applies the stored position (the JS move() no-arg path).
func (w *WinBox) applyPos() {
	w.Move(Unit{}, Unit{})
}

// applySize re-applies the stored size (the JS resize() no-arg path).
func (w *WinBox) applySize() {
	w.Resize(Unit{}, Unit{})
}

// moveRaw positions the window without touching the stored X/Y
// (the JS move(x, y, true) path).
func (w *WinBox) moveRaw(x, y float64) {
	setStyle(w.DOM, "left", fmt.Sprintf("%vpx", x))
	setStyle(w.DOM, "top", fmt.Sprintf("%vpx", y))
	if w.OnMove != nil {
		w.OnMove(w, x, y)
	}
}

// resizeRaw sizes the window without touching the stored Width/Height
// (the JS resize(w, h, true) path).
func (w *WinBox) resizeRaw(width, height float64) {
	setStyle(w.DOM, "width", fmt.Sprintf("%vpx", width))
	setStyle(w.DOM, "height", fmt.Sprintf("%vpx", height))
	if w.OnResize != nil {
		w.OnResize(w, width, height)
	}
}

// AddControl adds a custom button to the titlebar controls.
func (w *WinBox) AddControl(control Control) *WinBox {
	node := document.Call("createElement", "span")
	icons := getByClass(w.DOM, "wb-control")

	if control.Class != "" {
		node.Set("className", control.Class)
	}
	if control.Image != "" {
		setStyle(node, "background-image", "url("+control.Image+")")
	}
	if control.Click != nil {
		click := control.Click
		f := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			var ev js.Value
			if len(args) > 0 {
				ev = args[0]
			}
			click(ev, w)
			return nil
		})
		w.funcs = append(w.funcs, f)
		node.Set("onclick", f)
	}

	icons.Call("insertBefore", node, icons.Get("childNodes").Index(control.Index))

	return w
}

// RemoveControl removes the first titlebar control matching the given
// class name.
func (w *WinBox) RemoveControl(class string) *WinBox {
	control := getByClass(w.DOM, class)
	if control.Truthy() {
		control.Call("remove")
	}
	return w
}

// AddClass adds a CSS class to the window element. Built-in modifiers:
// no-min, no-max, no-full, no-close, no-resize, no-move, no-header,
// no-animation, no-shadow.
func (w *WinBox) AddClass(class string) *WinBox {
	addClass(w.DOM, class)
	return w
}

// RemoveClass removes a CSS class from the window element.
func (w *WinBox) RemoveClass(class string) *WinBox {
	removeClass(w.DOM, class)
	return w
}

// HasClass reports whether the window element has the given CSS class.
func (w *WinBox) HasClass(class string) bool {
	return hasClass(w.DOM, class)
}

// ToggleClass toggles a CSS class on the window element.
func (w *WinBox) ToggleClass(class string) *WinBox {
	if w.HasClass(class) {
		return w.RemoveClass(class)
	}
	return w.AddClass(class)
}
