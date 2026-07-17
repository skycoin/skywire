//go:build js && wasm

// Package dmsgclient pkg/dmsg/dmsgclient/jslog_js.go c1-net-dmsg
package dmsgclient

import (
	"syscall/js"
)

// jslog routes a debug line to a JS hook (window.__skylog) when present, so the
// browser harness / control bridge can surface dmsg-disc-over-dmsg internals.
// No-op when the hook is absent. Build-tagged to the browser; a no-op elsewhere.
func jslog(s string) {
	if h := js.Global().Get("__skylog"); h.Truthy() {
		h.Invoke(s)
	}
}
