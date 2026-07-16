//go:build !tinygo

package wasmbin

import "github.com/skycoin/skywire/pkg/wasmhv/wasmbin/wasmgo"

// Standard-Go builds embed the Go-compiled wasm-visor + Go's wasm_exec.js.
// TinyGo builds swap in the TinyGo pair (select_tinygo.go). Keeping selection
// in build-tagged files means a binary carries exactly one wasm, paired with
// the wasm_exec.js of its own toolchain.
var (
	gzWasm     = wasmgo.WasmGz
	wasmExecJS = wasmgo.WasmExecJS
)
