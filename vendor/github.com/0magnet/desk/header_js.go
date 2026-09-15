//go:build js && wasm

package desk

import (
	"syscall/js"

	"github.com/0magnet/winbox-go"
)

// MountInHeader moves el into the title bar of the window whose outer element
// is wbEl (winbox's ".winbox" node), to the right of the title and level with
// the window controls — where a browser keeps its tabs. It reports false, and
// leaves el where it is, when wbEl has no title bar to speak of.
//
// The title bar is winbox's drag handle. A press on anything inside el must
// not start a window drag — that swallows the click and defeats an HTML5 tab
// drag — so el's children are marked with winbox's no-drag class as they
// appear, and the handle stands aside for them. A press on el's own empty
// space still moves the window, as the rest of the bar does.
func MountInHeader(wbEl, el js.Value) bool {
	if !wbEl.Truthy() || !el.Truthy() {
		return false
	}
	drag := wbEl.Call("querySelector", ".wb-drag")
	if !drag.Truthy() {
		return false
	}
	ds := drag.Get("style")
	ds.Set("display", "flex")
	ds.Set("alignItems", "flex-end")
	ds.Set("minWidth", "0")
	if title := wbEl.Call("querySelector", ".wb-title"); title.Truthy() {
		ts := title.Get("style")
		ts.Set("flex", "0 1 auto")
		ts.Set("maxWidth", "40%")
		ts.Set("marginRight", "8px")
		ts.Set("alignSelf", "center")
	}
	es := el.Get("style")
	es.Set("flex", "1 1 auto")
	es.Set("minWidth", "0")
	es.Set("height", "100%")
	es.Set("alignItems", "flex-end")
	es.Set("padding", "0 4px")
	es.Set("background", "transparent")
	es.Set("border", "0")
	es.Set("borderBottom", "0")
	es.Set("minHeight", "0")
	es.Set("overflowX", "auto")
	es.Set("overflowY", "hidden")
	es.Set("lineHeight", "normal")
	es.Set("cursor", "default")
	markNoDrag(el)
	drag.Call("appendChild", el)
	return true
}

// markNoDrag gives every child of el winbox's no-drag class, now and as
// children are added later — a pane builds its tabs over time, and the desk
// is what knows the window manager, not the pane.
//
// This replaces a mousedown listener on el that stopped propagation for
// presses on its children. That listener never ran: the drag handle's own
// listener is in the capture phase and stops the event on its way DOWN, before
// anything inside the bar is reached. So every tab press armed a window drag,
// the window left with the pointer, and the release fell somewhere else — no
// tab switching, and no "+" tab, in any window with a strip in its title bar.
func markNoDrag(el js.Value) {
	mark := func() {
		kids := el.Get("children")
		for i := 0; i < kids.Length(); i++ {
			kids.Index(i).Get("classList").Call("add", winbox.NoDragClass)
		}
	}
	mark()
	if mo := js.Global().Get("MutationObserver"); mo.Truthy() {
		mo.New(js.FuncOf(func(js.Value, []js.Value) any {
			mark()
			return nil
		})).Call("observe", el, map[string]any{"childList": true})
	}
}
