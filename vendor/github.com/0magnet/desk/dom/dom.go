//go:build js && wasm

// Package dom builds elements without the ceremony.
//
// Everything here is a thin wrapper over syscall/js. It exists because
// document.createElement, setAttribute, appendChild and addEventListener spelled
// out through js.Value turn a five-line widget into forty, and the shape of the
// markup disappears into the noise. With these the shape is the code:
//
//	dom.El("div", dom.Class("row"),
//	    dom.El("span", dom.Class("name"), dom.Text(name)),
//	    dom.El("span", dom.Class("size"), dom.Text(size)))
//
// Callbacks are retained so they are not collected while the DOM still refers
// to them; Release frees a batch when the thing that made them goes away.
package dom

import "syscall/js"

var document = js.Global().Get("document")

// Node is an element or an option that configures one. El takes a mixture.
type Node interface{ apply(js.Value) }

type optFunc func(js.Value)

func (f optFunc) apply(el js.Value) { f(el) }

type child struct{ v js.Value }

func (c child) apply(el js.Value) { el.Call("appendChild", c.v) }

// El makes an element and applies everything given to it.
func El(tag string, nodes ...Node) js.Value {
	el := document.Call("createElement", tag)
	for _, n := range nodes {
		if n != nil {
			n.apply(el)
		}
	}
	return el
}

// Child adds an existing element.
func Child(v js.Value) Node { return child{v} }

// Children adds several.
func Children(vs ...js.Value) Node {
	return optFunc(func(el js.Value) {
		for _, v := range vs {
			el.Call("appendChild", v)
		}
	})
}

// Class sets the class attribute.
func Class(c string) Node {
	return optFunc(func(el js.Value) { el.Set("className", c) })
}

// Text sets the text content, which also escapes it — the reason nothing here
// offers innerHTML.
func Text(s string) Node {
	return optFunc(func(el js.Value) { el.Set("textContent", s) })
}

// Attr sets an attribute.
func Attr(k, v string) Node {
	return optFunc(func(el js.Value) { el.Call("setAttribute", k, v) })
}

// Style sets one CSS property.
func Style(k, v string) Node {
	return optFunc(func(el js.Value) { el.Get("style").Set(k, v) })
}

// FSChangedEvent is dispatched on window when the desk's shared filesystem is
// replaced under panes that are already using it — which happens when a --auth
// token arrives after the page is running and memory is swapped for the
// machine. A pane showing a directory listing has no other way to know its
// contents just became a different filesystem.
//
// It lives here, in the leaf both sides already import, rather than beside the
// filesystem itself: the file manager would otherwise have to import the
// terminal to learn a string, and with it websh, which is a shell interpreter
// this module deliberately does not make a window manager depend on.
const FSChangedEvent = "desk:fschanged"

// Funcs collects callbacks so they can be released together.
type Funcs struct {
	fns    []js.Func
	detach []func()
}

// On adds a listener, retaining the callback in f.
func (f *Funcs) On(event string, handler func(ev js.Value)) Node {
	jf := js.FuncOf(func(_ js.Value, args []js.Value) any {
		var ev js.Value
		if len(args) > 0 {
			ev = args[0]
		}
		handler(ev)
		return nil
	})
	f.fns = append(f.fns, jf)
	return optFunc(func(el js.Value) { el.Call("addEventListener", event, jf) })
}

// OnTarget adds a listener to something that is not the element being built —
// window or document — and arranges for Release to REMOVE it as well as free
// it.
//
// The removal is the point. A listener on an element dies with the element, so
// releasing the callback is enough; window outlives every pane, so a listener
// left on it is called after Release freed its callback, and calling a released
// js.Func panics. A wasm panic is not a broken feature, it is a blank page.
func (f *Funcs) OnTarget(target js.Value, event string, handler func(ev js.Value)) {
	if !target.Truthy() {
		return
	}
	jf := js.FuncOf(func(_ js.Value, args []js.Value) any {
		var ev js.Value
		if len(args) > 0 {
			ev = args[0]
		}
		handler(ev)
		return nil
	})
	f.fns = append(f.fns, jf)
	f.detach = append(f.detach, func() {
		target.Call("removeEventListener", event, jf)
	})
	target.Call("addEventListener", event, jf)
}

// Release frees every callback added through f. Not calling it leaks the Go
// closures for the life of the page, since the DOM holds references the Go
// runtime cannot see.
func (f *Funcs) Release() {
	// Detach BEFORE freeing: a listener still attached to window when its
	// callback is released is a panic waiting for the next event.
	for _, fn := range f.detach {
		fn()
	}
	f.detach = nil
	for _, jf := range f.fns {
		jf.Release()
	}
	f.fns = nil
}

// Clear removes an element's children.
func Clear(el js.Value) {
	for el.Get("firstChild").Truthy() {
		el.Call("removeChild", el.Get("firstChild"))
	}
}

// Stylesheet appends CSS to the document once per id.
func Stylesheet(id, css string) {
	if document.Call("getElementById", id).Truthy() {
		return
	}
	st := El("style", Attr("id", id), Text(css))
	document.Get("head").Call("appendChild", st)
}
