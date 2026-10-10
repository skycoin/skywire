//go:build js && wasm

package xterm

import (
	"syscall/js"
	"time"

	"github.com/0magnet/xterm-go/vt"
)

// Touch: what a finger does on a phone or a tablet.
//
// Left to the browser, a drag over the terminal scrolls the page — the
// screen lies over the viewport that scrolls, so the gesture never reaches
// it — and a tap arrives as emulated mouse events, which do not reliably
// open the soft keyboard. So one finger is handled here: a drag scrolls the
// scrollback, a cell height of finger for each line, as the wheel turns
// pixels into lines (or, when a program has the mouse, is reported to it as
// wheel turns, as the wheel is); and a tap is a click, reported to a program
// that has the mouse, which also focuses the textarea and so brings up the
// keyboard. A second finger is the browser's again, for pinch zoom.
//
// Mouse input is untouched: none of this listens to anything but touch
// events, and the emulated mouse events a handled touch would have produced
// are canceled so that a tap is not also a click.

const (
	// touchSlop is how far a finger may wander and still be tapping.
	touchSlop = 10.0
	// touchTapTime is how long a tap may last; longer is a press, which is
	// left to the browser.
	touchTapTime = 500 * time.Millisecond
)

type touchState struct {
	active         bool
	id             int
	startX, startY float64
	lastY          float64
	start          time.Time
	moved          bool
	partial        float64 // lines dragged but not yet scrolled
}

func (t *Terminal) wireTouch() {
	nonPassive := map[string]any{"passive": false}
	t.element.Call("addEventListener", "touchstart", t.fn(func(_ js.Value, args []js.Value) any {
		if args[0].Get("defaultPrevented").Bool() {
			return nil // handled by something over the cells (xterm.go)
		}
		t.touchStart(args[0])
		return nil
	}), nonPassive)
	t.element.Call("addEventListener", "touchmove", t.fn(func(_ js.Value, args []js.Value) any {
		t.touchMove(args[0])
		return nil
	}), nonPassive)
	t.element.Call("addEventListener", "touchend", t.fn(func(_ js.Value, args []js.Value) any {
		t.touchEnd(args[0])
		return nil
	}), nonPassive)
	t.element.Call("addEventListener", "touchcancel", t.fn(func(js.Value, []js.Value) any {
		t.touch.active = false
		return nil
	}))
}

// ourTouch is the touch this gesture is following, from a list of them.
func (t *Terminal) ourTouch(list js.Value) (js.Value, bool) {
	for i := range list.Get("length").Int() {
		tc := list.Call("item", i)
		if tc.Get("identifier").Int() == t.touch.id {
			return tc, true
		}
	}
	return js.Value{}, false
}

func (t *Terminal) touchStart(ev js.Value) {
	touches := ev.Get("touches")
	if touches.Get("length").Int() != 1 {
		// A second finger: a pinch, which is the browser's.
		t.touch.active = false
		return
	}
	tc := touches.Call("item", 0)
	t.touch = touchState{
		active: true,
		id:     tc.Get("identifier").Int(),
		startX: tc.Get("clientX").Float(),
		startY: tc.Get("clientY").Float(),
		lastY:  tc.Get("clientY").Float(),
		start:  time.Now(),
	}
}

func (t *Terminal) touchMove(ev js.Value) {
	if !t.touch.active {
		return
	}
	tc, ok := t.ourTouch(ev.Get("changedTouches"))
	if !ok {
		return
	}
	x, y := tc.Get("clientX").Float(), tc.Get("clientY").Float()
	if !t.touch.moved {
		dx, dy := x-t.touch.startX, y-t.touch.startY
		if dx*dx+dy*dy < touchSlop*touchSlop {
			return
		}
		t.touch.moved = true
	}
	if ev.Get("cancelable").Bool() {
		ev.Call("preventDefault")
	}
	if t.cellH == 0 {
		return
	}
	// A finger moving up pulls the text up with it, which is scrolling
	// down: towards the bottom, as a positive wheel delta is.
	t.touch.partial += (t.touch.lastY - y) / t.cellH
	t.touch.lastY = y
	lines := int(t.touch.partial)
	t.touch.partial -= float64(lines)
	if lines == 0 {
		return
	}
	if t.Core.MouseService().AreMouseEventsActive() {
		action := vt.MouseActionDown // wheel down
		n := lines
		if lines < 0 {
			action, n = vt.MouseActionUp, -lines
		}
		for range n {
			t.reportMouseAt(x, y, vt.MouseButtonWheel, action)
		}
		return
	}
	t.Core.ScrollLines(lines)
}

func (t *Terminal) touchEnd(ev js.Value) {
	if !t.touch.active {
		return
	}
	tc, ok := t.ourTouch(ev.Get("changedTouches"))
	if !ok {
		return
	}
	t.touch.active = false
	if t.touch.moved {
		if ev.Get("cancelable").Bool() {
			ev.Call("preventDefault")
		}
		return
	}
	if time.Since(t.touch.start) > touchTapTime {
		return
	}
	// A tap. Canceling it stops the browser following up with emulated
	// mouse events, which would make it a click a second time.
	if ev.Get("cancelable").Bool() {
		ev.Call("preventDefault")
	}
	x, y := tc.Get("clientX").Float(), tc.Get("clientY").Float()
	if t.Core.MouseService().AreMouseEventsActive() {
		t.reportMouseAt(x, y, 0, vt.MouseActionDown)
		t.reportMouseAt(x, y, 0, vt.MouseActionUp)
	} else {
		t.ClearSelection()
	}
	// From inside the touch's own handler, which is what lets a phone open
	// its keyboard for it.
	t.Focus()
}

// reportMouseAt reports a mouse event at a point on the page, for input that
// is not a MouseEvent.
func (t *Terminal) reportMouseAt(clientX, clientY float64, button, action int) {
	rect := t.screen.Call("getBoundingClientRect")
	x := int(clientX - rect.Get("left").Float())
	y := int(clientY - rect.Get("top").Float())
	t.Core.MouseService().TriggerMouseEvent(&vt.MouseEvent{
		Col: int(float64(x) / t.cellW), Row: int(float64(y) / t.cellH), X: x, Y: y,
		Button: button,
		Action: action,
	})
}
