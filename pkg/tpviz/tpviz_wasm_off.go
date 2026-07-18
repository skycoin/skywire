//go:build !tpvizwasm

// Package tpviz pkg/tpviz/tpviz_wasm_off.go c4-app-rewards
package tpviz

import "embed"

// Default build: the experimental WASM tpviz view is NOT embedded (keeps ~11 MB
// out of every visor/dmsg-server binary). wasmDistFS stays empty and the /wasm
// endpoints return 404 with a hint to rebuild with -tags tpvizwasm. Build the
// wasm first with `make tpviz-wasm-tinygo` (writes pkg/tpviz/dist/), then
// `go build -tags tpvizwasm` to test it.

var wasmDistFS embed.FS

const wasmViewBuilt = false
