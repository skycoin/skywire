//go:build js && wasm

// Package deskhost pkg/wasmhv/deskhost/tpviz_js.go c3-vis-wasm
// The network-visualizer role of the one skywire command module.
//
// # One binary, another role
//
// Like the websh terminal (shell_js.go), the tpviz WebGL view needs a DOM +
// WebGL context, which the visor — running in a SharedWorker — does not have.
// Rather than ship a second wasm (pkg/tpviz/legacy/tpviz-gl.wasm) for the
// browser to fetch, this module carries the view as a role: the network-viz tab
// loads THIS SAME module a second time in the main thread as
// `skywire desk-host --role netview` (go.argv, set by the page before go.run),
// and Run installs only the view and never boots a visor. It runs where the
// canvas lives (the main thread), so no OffscreenCanvas is needed — the same
// reason the shell role can draw an xterm.
//
// The view reaches the real visor (which lives in the worker) for its data the
// same way the shell applets do: through the globalThis.skywireVisor postMessage
// proxy the tab already holds (network-view / transports). It pulls no data of
// its own and boots no dmsg/router/app subsystems.
//
// installNetView just publishes the tpvizGL API (pkg/tpviz/wasmgl.Register);
// the TypeScript view (pkg/tpviz/ui/src/cosmos-go-graph.ts) then drives
// tpvizGL.init/setData/... unchanged. There is no separate tpviz-gl.wasm any
// more: the native tpviz server serves THIS module at /tpviz-gl.wasm (out of
// pkg/wasmhv/execwasm), so the only browser-side change is that
// cosmos-go-graph.ts sets go.argv to the netview role before instantiating.
package deskhost

import (
	"github.com/skycoin/skywire/pkg/tpviz/wasmgl"
)

// installNetView publishes globalThis.tpvizGL for the network-viz tab. It does
// not block; the caller (Run's netview role) parks in keepAlive().
func installNetView() {
	wasmgl.Register()
}
