package wasmhv

import _ "embed"

// OverrideJS is pkg/wasmhv/override.js — the classic <script> that, in a
// generated standalone file, boots the WASM dmsg client and routes the UI's
// /api over dmsg (or to the in-wasm core in standalone mode). Embedded so
// GenerateStandalone consumers don't need to locate it on disk.
//
//go:embed override.js
var OverrideJS []byte

// BrowseJS is pkg/wasmhv/browse.js — the dmsg virtual-browser engine (the same
// file the wasm-visor dev harness loads). Injected into a generated standalone
// file in VISOR mode so the page gets a browse/host overlay (skynet sites
// rendered + self-hosting over dmsg, via globalThis.skywireVisor). Unused in
// viewer/standalone-hypervisor modes (no skywireVisor).
//
//go:embed browse.js
var BrowseJS []byte

// AutoUpdateJS is pkg/wasmhv/autoupdate.js — the wasm-visor self-update poller for
// the `hv serve` page: it compares a /wasm-version fingerprint against the version
// the page booted with and reloads to a newer build (toast + opt-out). Injected
// ONLY by hv serve, so it never runs for a native-hosted hypervisor UI.
//
//go:embed autoupdate.js
var AutoUpdateJS []byte

// HvBootJS is pkg/wasmhv/hv-boot.js — the clean boot bootstrap for serving the
// wasm-VISOR hypervisor UI as separate files (the `hv serve` / dev-harness model,
// as opposed to the single-file generator's inlined override.js). It sets
// CFG.visor, loads wasm_exec.js + wasm-visor.wasm, calls skywireVisor.boot(), and
// exposes the boot promise as CFG.ready — which the UI's SkywireHttpBackend awaits
// before its first /api call. Routing is owned by the Angular HttpBackend, so no
// fetch/XHR monkey-patch (unlike override.js).
//
//go:embed hv-boot.js
var HvBootJS []byte

// WasmExecJS is Go's lib/wasm/wasm_exec.js, vendored here so a generated file is
// self-contained. It MUST match the Go toolchain that built the embedded/passed
// dmsg.wasm — refresh it with the wasm build (the Makefile bundle target copies
// it from $(go env GOROOT)/lib/wasm/wasm_exec.js).
//
//go:embed wasm_exec.js
var WasmExecJS []byte
