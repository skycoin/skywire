//go:build js && wasm

// Package dmsg pkg/dmsg/dmsg/jsaddr_js.go: tiny net.Addr / net.Error helpers
// shared by the browser WebSocket (ws_js_tinygo.go, tinygo only) and WebTransport
// (wt_js_tinygo.go, all js/wasm) carrier adapters. Lives in a js&&wasm file so
// std-Go js/wasm — which uses coder/websocket for the dmsg WS session but still
// needs the browser-native WT dial — gets them too.
package dmsg

// wsAddr is the net.Addr for a browser WebSocket / WebTransport connection.
type wsAddr string

func (wsAddr) Network() string  { return "ws" }
func (a wsAddr) String() string { return string(a) }

// wsTimeoutError is the net.Error returned when a read deadline elapses.
type wsTimeoutError struct{}

func (wsTimeoutError) Error() string   { return "ws: i/o timeout" }
func (wsTimeoutError) Timeout() bool   { return true }
func (wsTimeoutError) Temporary() bool { return true }
