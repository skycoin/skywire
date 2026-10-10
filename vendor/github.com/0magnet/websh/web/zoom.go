//go:build js && wasm

package web

import "syscall/js"

// The zoom range, in CSS pixels of cell height.
//
// The floor is low deliberately. A terminal is usually made smaller to fit more
// LINES, but the reason to go all the way down is the other one: at four or
// five pixels a cell a terminal-sized box holds a few hundred columns, and a
// full-screen program drawing a picture out of characters then has enough of
// them to draw with. That is cheap in a way enlarging the box is not — the same
// pixels, cut into more cells.
const (
	zoomMin  = 4
	zoomMax  = 32
	zoomStep = 1
)

// zoomBinding is one registered listener, kept so it can be taken off again.
type zoomBinding struct {
	event   string
	fn      js.Func
	capture bool
}

// wireZoom gives the terminal the gesture every terminal emulator has: ctrl
// with the wheel, ctrl with + or -, and ctrl-0 back to the size it opened at.
//
// The listeners go on the container rather than the window because a page may
// hold several of these, and the one being pointed at is the one that should
// change. Both have to take the gesture away from the browser first — ctrl-wheel
// and ctrl-plus zoom the PAGE until something calls preventDefault, and the
// wheel listener has to declare passive:false to be allowed to.
//
// The wheel is taken in the CAPTURE phase, before the terminal sees it. A
// full-screen program that asked for mouse reports gets every wheel event the
// terminal sees, and xterm-go stops one it has reported; listening on the way
// back up, the zoom never ran while such a program had the screen, which is
// exactly when zooming out for more cells is wanted.
func (s *Session) wireZoom(el js.Value, def float64) {
	s.zoomEl = el
	zoom := func(by float64) { s.Term.SetFontSize(clampZoom(s.Term.FontSize() + by)) }

	s.bindZoom(el, "wheel", func(e js.Value) bool {
		if e.Get("deltaY").Float() > 0 {
			zoom(-zoomStep)
		} else {
			zoom(zoomStep)
		}
		return true
	}, true)

	s.bindZoom(el, "keydown", func(e js.Value) bool {
		switch e.Get("key").String() {
		case "+", "=":
			zoom(zoomStep)
		case "-", "_":
			zoom(-zoomStep)
		case "0":
			s.Term.SetFontSize(def)
		default:
			return false
		}
		return true
	}, false)
}

// bindZoom registers one ctrl-modified listener. It handles the two things both
// listeners share: an event without ctrl is not ours, and one that is ours must
// not also reach the browser.
func (s *Session) bindZoom(el js.Value, event string, take func(js.Value) bool, capture bool) {
	fn := js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) == 0 || !a[0].Get("ctrlKey").Bool() {
			return nil
		}
		if take(a[0]) {
			a[0].Call("preventDefault")
			if capture {
				a[0].Call("stopPropagation") // a zoom, not a wheel report too
			}
		}
		return nil
	})
	s.zoomFns = append(s.zoomFns, zoomBinding{event: event, fn: fn, capture: capture})
	el.Call("addEventListener", event, fn, map[string]any{"passive": false, "capture": capture})
}

// releaseZoom unbinds the listeners and drops their Go side. Close calls it: a
// js.Func that is never released pins its closure for the life of the program,
// and a page that opens and closes terminal windows would leak a pair per
// window. Unbinding first matters as much — a released func still registered on
// a live element panics the moment the event arrives.
func (s *Session) releaseZoom() {
	for _, b := range s.zoomFns {
		if s.zoomEl.Truthy() {
			s.zoomEl.Call("removeEventListener", b.event, b.fn, map[string]any{"capture": b.capture})
		}
		b.fn.Release()
	}
	s.zoomFns = nil
	s.zoomEl = js.Value{}
}

// clampZoom holds a requested size inside the range. Stepping past an end has
// to STOP there rather than be refused, or a wheel spun hard against the floor
// leaves the size wherever the last accepted notch put it.
func clampZoom(v float64) float64 {
	if v < zoomMin {
		return zoomMin
	}
	if v > zoomMax {
		return zoomMax
	}
	return v
}
