// Package wasmbin carries the compiled wasm-visor binary (gzipped) inside the
// skywire binary, so `cli hv gen` — and the visor serving the HV UI — produce a
// browser visor with NO external --wasm file, including from a plain
// `go install`.
//
// The actual bytes come from one of two build-tag-selected embed libraries,
// mirroring how skycoin maintains wasm-go and wasm-tinygo: a standard-Go build
// embeds wasmgo (the Go-compiled wasm-visor + Go's wasm_exec.js, ~9.5 MB gzip),
// a TinyGo build embeds wasmtinygo (the TinyGo-compiled wasm-visor + TinyGo's
// wasm_exec.js, ~2.9 MB gzip). See select_go.go / select_tinygo.go. A wasm and
// its wasm_exec.js are toolchain-specific (the TinyGo loader provides the WASI
// shims its module imports), so each binary carries exactly the pair matching
// its own compilation — never a mismatched loader.
//
// Both committed blobs travel in the published Go module and are updated
// INTENTIONALLY: `make embed-wasm-visor` (Go) / `make embed-wasm-visor-tinygo`
// (TinyGo fork). Ordinary `make wasm-visor` writes only to build/ and does not
// touch these files. Old blob versions can be pruned from history periodically —
// see docs/design/wasm-visor-distribution.md.
package wasmbin

import (
	"bytes"
	"compress/gzip"
	"io"
)

// Embedded reports whether a wasm-visor binary is compiled in (true in a normal
// build — the file is committed).
func Embedded() bool { return len(gzWasm) > 0 }

// Get returns the embedded wasm-visor binary, decompressed. It is the blob for
// this binary's own toolchain (Go or TinyGo).
func Get() ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(gzWasm))
	if err != nil {
		return nil, err
	}
	defer zr.Close() //nolint:errcheck
	return io.ReadAll(zr)
}

// WasmExecJS returns the wasm_exec.js loader matching the embedded wasm-visor's
// toolchain. Serve this alongside Get()'s bytes — a TinyGo wasm needs TinyGo's
// loader (WASI shims) and a Go wasm needs Go's.
func WasmExecJS() []byte { return wasmExecJS }
