// Package dist ships the window manager as ready-to-embed page assets: the
// TinyGo-compiled wasm module (gzipped), the wrapped wasm_exec loader class,
// and the page-side loader that starts the module and publishes the global
// WinBox constructor.
//
// A consumer serves (or inlines) three pieces:
//
//	ExecJS   — TinyGo's wasm_exec.js, wrapped so its loader class lands on
//	           globalThis.__winboxGo instead of globalThis.Go (the page's own
//	           wasm module may use a different Go toolchain's wasm_exec).
//	LoaderJS — fetches/inflates the module and resolves __winboxReady once
//	           `new WinBox({...})` works. Module bytes come from
//	           __WINBOX_WASM_B64__ (inlined gzip, base64), __WINBOX_WASM_URL__,
//	           or "winbox.wasm" relative to document.baseURI.
//	WasmGz   — the module itself, to serve at that URL (inflate first, or
//	           serve with Content-Encoding: gzip) or to inline as base64.
//
// The artifacts are BUILT AND COMMITTED (`make dist`): the module is a wasm
// artifact from a second toolchain (TinyGo), which `go build` of a consumer
// cannot produce.
package dist

import (
	_ "embed"
)

//go:embed winbox.wasm.gz
var wasmGz []byte

//go:embed winbox-exec.js
var execJS []byte

//go:embed winbox-loader.js
var loaderJS []byte

// WasmGz returns the gzipped wasm module (cmd/winbox-js built with TinyGo).
func WasmGz() []byte { return wasmGz }

// ExecJS returns TinyGo's wasm_exec.js wrapped onto globalThis.__winboxGo.
func ExecJS() []byte { return execJS }

// LoaderJS returns the page-side loader that starts the module and publishes
// globalThis.WinBox + __winboxReady.
func LoaderJS() []byte { return loaderJS }
