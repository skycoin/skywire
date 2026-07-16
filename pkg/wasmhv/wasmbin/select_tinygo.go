//go:build tinygo

package wasmbin

import "github.com/skycoin/skywire/pkg/wasmhv/wasmbin/wasmtinygo"

// TinyGo builds embed the TinyGo-compiled wasm-visor + TinyGo's wasm_exec.js
// (see select_go.go for the standard-Go pair).
var (
	gzWasm     = wasmtinygo.WasmGz
	wasmExecJS = wasmtinygo.WasmExecJS
)
