//go:build js && wasm

package winbox

import (
	"fmt"
	"math"
	"strings"
	"syscall/js"
	"time"
)

var (
	stackMin []*WinBox
	stackWin []*WinBox

	body          js.Value
	idCounter     int
	indexCounter  = 10
	isFullscreen  *WinBox
	prefixRequest string
	prefixExit    string
	rootW, rootH  float64
	windowClicked bool
)

// Stack returns all open windows in stacking order (last = topmost).
func Stack() []*WinBox {
	return stackWin
}

func setup() {
	body = document.Get("body")

	InjectCSS()

	obj := js.Global().Get("Object")
	eventOptions = obj.New()
	eventOptions.Set("capture", true)
	eventOptions.Set("passive", false)
	eventOptionsPassive = obj.New()
	eventOptionsPassive.Set("capture", true)
	eventOptionsPassive.Set("passive", true)
	captureTrue = js.ValueOf(true)

	for _, prefix := range []string{
		"requestFullscreen",
		"msRequestFullscreen",
		"webkitRequestFullscreen",
		"mozRequestFullscreen",
	} {
		if body.Get(prefix).Truthy() {
			prefixRequest = prefix
			break
		}
	}

	if prefixRequest != "" {
		prefixExit = prefixRequest
		prefixExit = strings.Replace(prefixExit, "request", "exit", 1)
		prefixExit = strings.Replace(prefixExit, "mozRequest", "mozCancel", 1)
		prefixExit = strings.Replace(prefixExit, "Request", "Exit", 1)
	}

	addListener(window, "resize", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		initRoot()
		// updateDocks re-places the docks against the new viewport and calls
		// updateMinStack itself; with nothing docked it is the bare
		// updateMinStack the original does here and nothing more.
		if len(stackDock) > 0 {
			updateDocks()
		} else {
			updateMinStack()
		}
		return nil
	}), js.Undefined())

	addListener(body, "mousedown", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		windowClicked = false
		return nil
	}), captureTrue)

	addListener(body, "mousedown", js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		if !windowClicked {
			for i := len(stackWin) - 1; i >= 0; i-- {
				lastFocus := stackWin[i]
				if lastFocus.Focused {
					lastFocus.Blur()
					break
				}
			}
		}
		return nil
	}), js.Undefined())

	initRoot()
}

func initRoot() {
	doc := document.Get("documentElement")
	rootW = doc.Get("clientWidth").Float()
	rootH = doc.Get("clientHeight").Float()
}

func register(w *WinBox) {
	addWindowListener(w, "drag")
	addWindowListener(w, "n")
	addWindowListener(w, "s")
	addWindowListener(w, "w")
	addWindowListener(w, "e")
	addWindowListener(w, "nw")
	addWindowListener(w, "ne")
	addWindowListener(w, "se")
	addWindowListener(w, "sw")

	w.listen(getByClass(w.DOM, "wb-min"), "click", func(event js.Value) {
		preventEvent(event, false)
		if w.Min {
			w.Restore().Focus()
		} else {
			w.Minimize()
		}
	}, js.Undefined())

	w.listen(getByClass(w.DOM, "wb-max"), "click", func(event js.Value) {
		preventEvent(event, false)
		if w.Max {
			w.Restore().Focus()
		} else {
			w.Maximize().Focus()
		}
	}, js.Undefined())

	if prefixRequest != "" {
		w.listen(getByClass(w.DOM, "wb-full"), "click", func(event js.Value) {
			preventEvent(event, false)
			w.Fullscreen().Focus()
		}, js.Undefined())
	} else {
		w.AddClass("no-full")
	}

	w.listen(getByClass(w.DOM, "wb-close"), "click", func(event js.Value) {
		preventEvent(event, false)
		w.Close(false)
	}, js.Undefined())

	w.listen(w.DOM, "mousedown", func(event js.Value) {
		windowClicked = true
	}, captureTrue)

	w.listen(w.Body, "mousedown", func(event js.Value) {
		// stop propagation would disable global listeners used inside window
		// contents; use event bubbling so the other click listeners can skip
		// this handler
		w.Focus()
	}, captureTrue)
}

// listen wraps fn in a js.Func, attaches it and tracks it for release on close.
func (w *WinBox) listen(node js.Value, event string, fn func(event js.Value), opt js.Value) {
	f := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		var ev js.Value
		if len(args) > 0 {
			ev = args[0]
		}
		fn(ev)
		return nil
	})
	w.funcs = append(w.funcs, f)
	addListener(node, event, f, opt)
}

func removeMinStack(w *WinBox) {
	for i, s := range stackMin {
		if s == w {
			stackMin = append(stackMin[:i], stackMin[i+1:]...)
			break
		}
	}
	updateMinStack()
	w.RemoveClass("min")
	w.Min = false
	w.DOM.Set("title", "")
}

func updateMinStack() {
	splitscreenIndex := map[string]float64{}
	splitscreenLength := map[string]float64{}

	for _, w := range stackMin {
		key := fmt.Sprintf("%v:%v", w.Left, w.Top)
		splitscreenLength[key]++
	}

	// Along the bottom of the area the docks leave, not of the raw viewport, so
	// minimized windows stack above a bottom dock instead of underneath it.
	// With nothing docked cx and cy are zero and cw/ch are the viewport, which
	// is the original's arithmetic unchanged.
	cx, cy, cw, ch := dockContentBox()

	for _, w := range stackMin {
		key := fmt.Sprintf("%v:%v", w.Left, w.Top)
		width := math.Min((cw-w.Left-w.Right)/splitscreenLength[key], 250)
		w.resizeRaw(math.Trunc(width+1), w.Header)
		w.moveRaw(math.Trunc(cx+w.Left+splitscreenIndex[key]*width), cy+ch-w.Bottom-w.Header)
		splitscreenIndex[key]++
	}
}

func focusNext() {
	for i := len(stackWin) - 1; i >= 0; i-- {
		lastFocus := stackWin[i]
		if !lastFocus.Min {
			lastFocus.Focus()
			break
		}
	}
}

func addWindowListener(w *WinBox, dir string) {
	node := getByClass(w.DOM, "wb-"+dir)
	if !node.Truthy() {
		return
	}

	var touch bool
	var x, y float64
	var dblclickTimer int64
	var mousemoveFn, mouseupFn js.Func

	mousemoveFn = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		event := args[0]
		preventEvent(event, false)

		if touch {
			event = event.Get("touches").Index(0)
		}

		pageX := event.Get("pageX").Float()
		pageY := event.Get("pageY").Float()
		offsetX := pageX - x
		offsetY := pageY - y

		oldW := w.Width
		oldH := w.Height
		oldX := w.X
		oldY := w.Y

		var resizeW, resizeH, moveX, moveY bool

		// A docked window answers the pointer differently: its one live handle
		// sets the thickness, and dragging its title pulls it off the edge —
		// the same way dragging a maximized window restores it below.
		if w.dock != EdgeNone {
			if dir != "drag" {
				if dir != dockHandleFor(w.dock) {
					return nil
				}
				// Tracked straight from the pointer rather than through
				// Width/Height: those still hold the floating geometry Undock
				// will restore, so deriving a delta from them would resize the
				// dock to the wrong base and lose the window's old size.
				switch w.dock {
				case EdgeTop:
					w.dockThick += offsetY
				case EdgeBottom:
					w.dockThick -= offsetY
				case EdgeLeft:
					w.dockThick += offsetX
				case EdgeRight:
					w.dockThick -= offsetX
				case EdgeNone:
				}
				w.clampDockThick()
				updateDocks()
				x, y = pageX, pageY
				return nil
			}
			w.Undock()
		}

		if dir == "drag" {
			if w.HasClass("no-move") {
				return nil
			}
			w.X += offsetX
			w.Y += offsetY
			moveX, moveY = true, true
		} else {
			switch dir {
			case "e", "se", "ne":
				w.Width += offsetX
				resizeW = true
			case "w", "sw", "nw":
				w.X += offsetX
				w.Width -= offsetX
				resizeW = true
				moveX = true
			}
			switch dir {
			case "s", "se", "sw":
				w.Height += offsetY
				resizeH = true
			case "n", "ne", "nw":
				w.Y += offsetY
				w.Height -= offsetY
				resizeH = true
				moveY = true
			}
		}

		// The limit is what the viewport allows now, which is read live and so
		// follows a zoom or a rotation. A caller's own maximum, if it set one,
		// applies on top of that.
		if resizeW {
			limit := rootW - w.X - w.Right
			if w.MaxWidth > 0 && w.MaxWidth < limit {
				limit = w.MaxWidth
			}
			w.Width = math.Max(math.Min(w.Width, limit), w.MinWidth)
			resizeW = w.Width != oldW
		}

		if resizeH {
			limit := rootH - w.Y - w.Bottom
			if w.MaxHeight > 0 && w.MaxHeight < limit {
				limit = w.MaxHeight
			}
			w.Height = math.Max(math.Min(w.Height, limit), w.MinHeight)
			resizeH = w.Height != oldH
		}

		if resizeW || resizeH {
			w.applySize()
		}

		if moveX {
			if w.Max {
				switch {
				case pageX < rootW/3:
					w.X = w.Left
				case pageX > rootW/3*2:
					w.X = rootW - w.Width - w.Right
				default:
					w.X = rootW/2 - w.Width/2
				}
				w.X += offsetX
			}

			high := rootW - w.Width - w.Right
			low := w.Left
			if w.Overflow {
				high = rootW - 30
				low = 30 - w.Width
			}
			w.X = math.Max(math.Min(w.X, high), low)
			moveX = w.X != oldX
		}

		if moveY {
			if w.Max {
				w.Y = w.Top + offsetY
			}

			high := rootH - w.Height - w.Bottom
			if w.Overflow {
				high = rootH - w.Header
			}
			w.Y = math.Max(math.Min(w.Y, high), w.Top)
			moveY = w.Y != oldY
		}

		if moveX || moveY {
			if w.Max {
				w.Restore()
			}
			w.applyPos()
		}

		if resizeW || moveX {
			x = pageX
		}
		if resizeH || moveY {
			y = pageY
		}
		return nil
	})

	mouseupFn = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		preventEvent(args[0], false)
		removeClass(body, "wb-lock")

		if touch {
			removeListener(window, "touchmove", mousemoveFn, eventOptionsPassive)
			removeListener(window, "touchend", mouseupFn, eventOptionsPassive)
		} else {
			removeListener(window, "mousemove", mousemoveFn, eventOptionsPassive)
			removeListener(window, "mouseup", mouseupFn, eventOptionsPassive)
		}
		return nil
	})

	mousedownFn := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		event := args[0]

		// prevent the full iteration through the fallback chain of a touch
		// event (touch > mouse > click)
		preventEvent(event, true)
		w.Focus()

		if dir == "drag" {
			if w.Min {
				w.Restore()
				return nil
			}

			if !w.HasClass("no-max") {
				now := time.Now().UnixMilli()
				diff := now - dblclickTimer
				dblclickTimer = now

				if diff < 300 {
					if w.Max {
						w.Restore()
					} else {
						w.Maximize()
					}
					return nil
				}
			}
		}

		if !w.Min {
			addClass(body, "wb-lock")

			touches := event.Get("touches")
			if touches.Truthy() && touches.Index(0).Truthy() {
				touch = true
				event = touches.Index(0)
				addListener(window, "touchmove", mousemoveFn, eventOptionsPassive)
				addListener(window, "touchend", mouseupFn, eventOptionsPassive)
			} else {
				touch = false
				addListener(window, "mousemove", mousemoveFn, eventOptionsPassive)
				addListener(window, "mouseup", mouseupFn, eventOptionsPassive)
			}

			x = event.Get("pageX").Float()
			y = event.Get("pageY").Float()
		}
		return nil
	})

	w.funcs = append(w.funcs, mousemoveFn, mouseupFn, mousedownFn)

	addListener(node, "mousedown", mousedownFn, eventOptions)
	addListener(node, "touchstart", mousedownFn, eventOptions)
}

func hasFullscreen() bool {
	return document.Get("fullscreen").Truthy() ||
		document.Get("fullscreenElement").Truthy() ||
		document.Get("webkitFullscreenElement").Truthy() ||
		document.Get("mozFullScreenElement").Truthy()
}

func cancelFullscreen() bool {
	isFullscreen.Full = false

	if hasFullscreen() {
		// exitFullscreen runs async and returns a promise; the important part
		// is that the promise callback runs before "onresize" fires
		document.Call(prefixExit)
		return true
	}
	return false
}
