// Package wasmgo pkg/wasmhv/wasmbin/wasmgo/wasmgo.go c3-vis-wasm
// matching wasm_exec.js. It is the embed library selected for non-TinyGo builds
// (see ../select_go.go). A wasm binary and its wasm_exec.js are toolchain-
// specific — the Go and TinyGo loaders differ (TinyGo's provides the WASI /
// wasi_snapshot_preview1 shims its wasm imports) — so each binary embeds only
// the pair that matches its own compilation, mirroring how skycoin maintains
// wasm-go and wasm-tinygo. Update with `make embed-wasm-visor`.
package wasmgo

import _ "embed"

// WasmGz is the gzipped standard-Go wasm-visor binary.
//
//go:embed wasm-visor.wasm.gz
var WasmGz []byte

// WasmExecJS is Go's lib/wasm/wasm_exec.js, matching WasmGz.
//
//go:embed wasm_exec.js
var WasmExecJS []byte
