//go:build js && wasm

package desk

import "syscall/js"

// MountInHeader moves el into the title bar of the window whose outer element
// is wbEl (winbox's ".winbox" node), to the right of the title and level with
// the window controls — where a browser keeps its tabs. It reports false, and
// leaves el where it is, when wbEl has no title bar to speak of.
//
// The title bar is winbox's drag handle. Pressing on anything inside el would
// start a window drag, which swallows the click and defeats an HTML5 tab drag,
// so presses that land on el's children stop there; a press on el's own empty
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
	stop := js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) > 0 && !a[0].Get("target").Equal(el) {
			a[0].Call("stopPropagation")
		}
		return nil
	})
	el.Call("addEventListener", "mousedown", stop)
	el.Call("addEventListener", "touchstart", stop)
	drag.Call("appendChild", el)
	return true
}
