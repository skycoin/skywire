//go:build js && wasm

package winbox

import (
	_ "embed"
)

//go:embed assets/winbox.css
var styleSheet string

const styleID = "winbox-style"

// CSS returns the embedded stylesheet (icons inlined as data URIs).
func CSS() string {
	return styleSheet
}

// InjectCSS appends the embedded stylesheet to document.head once.
// It is called automatically by New; call it manually only if you need
// the styles before the first window is created, or skip it entirely by
// providing your own stylesheet with an element id "winbox-style".
func InjectCSS() {
	if document.Call("getElementById", styleID).Truthy() {
		return
	}
	style := document.Call("createElement", "style")
	style.Set("id", styleID)
	style.Set("textContent", styleSheet)
	document.Get("head").Call("appendChild", style)
}
