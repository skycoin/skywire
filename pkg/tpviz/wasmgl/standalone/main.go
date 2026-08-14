//go:build js && wasm

// Package main pkg/tpviz/wasmgl/standalone/main.go
// The standalone build of the Go/wasm WebGL tpviz view — the separate wasm
// module the NATIVE tpviz server (pkg/visor/hypervisor.go → /tp-viz/) serves
// and the TypeScript view fetches + instantiates (cosmos-go-graph.ts). It is a
// thin entrypoint around pkg/tpviz/wasmgl.Register: install the tpvizGL API and
// park.
//
// The wasm-visor does NOT use this build — it imports pkg/tpviz/wasmgl directly
// and runs Register() as its "netview" role, so the same view is a role of the
// one visor blob rather than a second wasm to download. Build with:
//
//	make tpviz-gl   # → pkg/tpviz/legacy/tpviz-gl.wasm (TinyGo)
package main

import (
	"github.com/skycoin/skywire/pkg/tpviz/wasmgl"
)

func main() {
	wasmgl.Register()
	select {} // this build owns the program lifetime; the netview role parks in keepAlive() instead
}
