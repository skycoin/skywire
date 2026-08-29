//go:build js && wasm

package winbox

import "syscall/js"

var (
	document = js.Global().Get("document")
	window   = js.Global()
)

// event listener options, created lazily in setup() so package init
// stays free of JS allocations.
var (
	eventOptions        js.Value // { capture: true, passive: false }
	eventOptionsPassive js.Value // { capture: true, passive: true }
	captureTrue         js.Value // true
)

func addListener(node js.Value, event string, fn js.Func, opt js.Value) {
	if node.Truthy() {
		if opt.IsUndefined() {
			node.Call("addEventListener", event, fn, false)
		} else {
			node.Call("addEventListener", event, fn, opt)
		}
	}
}

func removeListener(node js.Value, event string, fn js.Func, opt js.Value) {
	if node.Truthy() {
		if opt.IsUndefined() {
			node.Call("removeEventListener", event, fn, false)
		} else {
			node.Call("removeEventListener", event, fn, opt)
		}
	}
}

func preventEvent(event js.Value, prevent bool) {
	event.Call("stopPropagation")
	if prevent {
		event.Call("preventDefault")
	}
}

func getByClass(root js.Value, name string) js.Value {
	return root.Call("getElementsByClassName", name).Index(0)
}

func addClass(node js.Value, classname string) {
	node.Get("classList").Call("add", classname)
}

func hasClass(node js.Value, classname string) bool {
	return node.Get("classList").Call("contains", classname).Bool()
}

func removeClass(node js.Value, classname string) {
	node.Get("classList").Call("remove", classname)
}

// setStyle caches the last applied value on the node as a JS property
// (same trick as the original) to skip redundant style writes.
func setStyle(node js.Value, style, value string) {
	cache := "_s_" + style
	if !node.Get(cache).Equal(js.ValueOf(value)) {
		node.Get("style").Call("setProperty", style, value)
		node.Set(cache, value)
	}
}

func setText(node js.Value, value string) {
	textnode := node.Get("firstChild")
	if textnode.Truthy() {
		textnode.Set("nodeValue", value)
	} else {
		node.Set("textContent", value)
	}
}
