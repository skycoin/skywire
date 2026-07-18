// Package wasmtinygo pkg/wasmhv/wasmbin/wasmtinygo/wasmtinygo.go c3-vis-wasm
// matching wasm_exec.js. It is the embed library selected for TinyGo builds
// (see ../select_tinygo.go). The TinyGo wasm-visor is ~2.9 MB gzip vs ~9.5 MB
// for standard Go, and its wasm_exec.js provides the WASI
// (wasi_snapshot_preview1) shims the TinyGo module imports — which the Go loader
// does not — so it is only correct when paired with a TinyGo-compiled binary,
// mirroring how skycoin maintains wasm-go and wasm-tinygo. Update with
// `make embed-wasm-visor-tinygo` (needs the 0magnet/tinygo fork).
package wasmtinygo

import _ "embed"

// WasmGz is the gzipped TinyGo-compiled wasm-visor binary.
//
//go:embed wasm-visor.wasm.gz
var WasmGz []byte

// WasmExecJS is TinyGo's targets/wasm_exec.js, matching WasmGz.
//
//go:embed wasm_exec.js
var WasmExecJS []byte
