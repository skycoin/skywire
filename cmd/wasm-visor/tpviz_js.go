//go:build js && wasm

// Package main cmd/wasm-visor/tpviz_js.go c3-vis-wasm
// The network-visualizer role of the one wasm-visor blob.
//
// # One binary, another role
//
// Like the websh terminal (shell_js.go), the tpviz WebGL view needs a DOM +
// WebGL context, which the visor — running in a SharedWorker — does not have.
// Rather than ship a second wasm (pkg/tpviz/legacy/tpviz-gl.wasm) for the
// browser to fetch, this binary carries the view as a role: the network-viz tab
// loads THIS SAME blob a second time in the main thread with
// globalThis.__SKYWIRE_WASM_ROLE__ = "netview", and main() (main.go) installs
// only the view and never boots a visor. It runs where the canvas lives (the
// main thread), so no OffscreenCanvas is needed — the same reason the shell
// role can draw an xterm.
//
// The view reaches the real visor (which lives in the worker) for its data the
// same way the shell applets do: through the globalThis.skywireVisor postMessage
// proxy the tab already holds (network-view / transports). It pulls no data of
// its own and boots no dmsg/router/app subsystems.
//
// installNetView just publishes the tpvizGL API (pkg/tpviz/wasmgl.Register);
// the TypeScript view (pkg/tpviz/ui/src/cosmos-go-graph.ts) then drives
// tpvizGL.init/setData/... unchanged. There is no separate tpviz-gl.wasm any
// more: the native tpviz server serves THIS blob at /tpviz-gl.wasm (out of
// pkg/wasmhv/wasmbin), so the only browser-side change is that
// cosmos-go-graph.ts sets __SKYWIRE_WASM_ROLE__="netview" before instantiating.
package main

import (
	"github.com/skycoin/skywire/pkg/tpviz/wasmgl"
)

// installNetView publishes globalThis.tpvizGL for the network-viz tab. It does
// not block; the caller (main.go's netview role) parks in keepAlive().
func installNetView() {
	wasmgl.Register()
}
