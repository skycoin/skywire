//go:build js && !tinygo

// Package skyenv pkg/skyenv/carriers_js.go c0-com-env
package skyenv

// DefaultDmsgCarriers pins freshly generated configs to the WebSocket
// carrier under js/wasm: a browser (or node) runtime has no raw TCP,
// so the native tcp-first preference can never connect, while every
// dmsg server in the fleet advertises wss. Writing it into the config
// keeps the behavior visible and editable rather than hidden in the
// runtime. Gated !tinygo because the TinyGo install-page build
// generates configs for NATIVE visors, which must not be pinned to ws.
var DefaultDmsgCarriers = []string{"ws"}
