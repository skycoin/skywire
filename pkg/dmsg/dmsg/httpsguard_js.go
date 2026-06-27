//go:build js && wasm

package dmsg

import (
	"strings"
	"syscall/js"
)

// pageHTTPS reports whether the wasm page was served over HTTPS. From an HTTPS
// origin the browser forbids opening a plain ws:// WebSocket (mixed content), so
// such a dial throws "Failed to construct 'WebSocket'" and floods the console.
func pageHTTPS() bool {
	loc := js.Global().Get("location")
	if !loc.Truthy() {
		return false
	}
	return loc.Get("protocol").String() == "https:"
}

// insecureWSBlocked reports whether dialing url would be blocked by the browser
// as mixed content: a plain ws:// endpoint from an HTTPS page. wss:// is always
// allowed. We check this BEFORE constructing the WebSocket so the dmsg client
// skips such a server with a clean Go error instead of a thrown browser
// exception — the served wasm-visor needs a wss:// seed (or WebTransport).
func insecureWSBlocked(url string) bool {
	return pageHTTPS() && strings.HasPrefix(strings.ToLower(strings.TrimSpace(url)), "ws://")
}
