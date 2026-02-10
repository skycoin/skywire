//go:build js && wasm

package ui

import (
	"syscall/js"
)

var doc = js.Global().Get("document")

// getElement returns a DOM element by ID, or js.Null if not found
func getElement(id string) js.Value {
	el := doc.Call("getElementById", id)
	return el
}

// SetText sets the textContent of an element by ID
func SetText(id, text string) {
	el := getElement(id)
	if !el.IsNull() && !el.IsUndefined() {
		el.Set("textContent", text)
	}
}

// SetHTML sets the innerHTML of an element by ID
func SetHTML(id, html string) {
	el := getElement(id)
	if !el.IsNull() && !el.IsUndefined() {
		el.Set("innerHTML", html)
	}
}

// SetVisible shows or hides an element by setting display style
func SetVisible(id string, visible bool) {
	el := getElement(id)
	if el.IsNull() || el.IsUndefined() {
		return
	}
	if visible {
		el.Get("style").Set("display", "")
	} else {
		el.Get("style").Set("display", "none")
	}
}

// GetChecked returns the checked state of a checkbox element
func GetChecked(id string) bool {
	el := getElement(id)
	if el.IsNull() || el.IsUndefined() {
		return false
	}
	return el.Get("checked").Bool()
}

// GetValue returns the value of an input element
func GetValue(id string) string {
	el := getElement(id)
	if el.IsNull() || el.IsUndefined() {
		return ""
	}
	return el.Get("value").String()
}

// OnEvent registers an event listener on an element by ID
func OnEvent(id, event string, callback func()) js.Func {
	el := getElement(id)
	if el.IsNull() || el.IsUndefined() {
		return js.Func{}
	}
	fn := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		callback()
		return nil
	})
	el.Call("addEventListener", event, fn)
	return fn
}

// AddClass adds a CSS class to an element
func AddClass(id, class string) {
	el := getElement(id)
	if !el.IsNull() && !el.IsUndefined() {
		el.Get("classList").Call("add", class)
	}
}

// RemoveClass removes a CSS class from an element
func RemoveClass(id, class string) {
	el := getElement(id)
	if !el.IsNull() && !el.IsUndefined() {
		el.Get("classList").Call("remove", class)
	}
}

// RegisterGlobalFunc registers a Go function as a global JavaScript function
func RegisterGlobalFunc(name string, fn func(args []js.Value)) js.Func {
	f := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		fn(args)
		return nil
	})
	js.Global().Set(name, f)
	return f
}
