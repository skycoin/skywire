package wasmhv

import _ "embed"

// OverrideJS is pkg/wasmhv/override.js — the classic <script> that, in a
// generated standalone file, boots the WASM dmsg client and routes the UI's
// /api over dmsg (or to the in-wasm core in standalone mode). Embedded so
// GenerateStandalone consumers don't need to locate it on disk.
//
//go:embed override.js
var OverrideJS []byte

// WasmExecJS is Go's lib/wasm/wasm_exec.js, vendored here so a generated file is
// self-contained. It MUST match the Go toolchain that built the embedded/passed
// dmsg.wasm — refresh it with the wasm build (the Makefile bundle target copies
// it from $(go env GOROOT)/lib/wasm/wasm_exec.js).
//
//go:embed wasm_exec.js
var WasmExecJS []byte
