// Package wasmbin pkg/wasmhv/wasmbin/embed.go c3-vis-wasm
// skywire binary, so `cli hv gen` — and the visor serving the HV UI — produce a
// browser visor with NO external --wasm file, including from a plain
// `go install`.
//
// Two toolchains produce a wasm-visor, mirroring how skycoin maintains wasm-go
// and wasm-tinygo: standard Go (wasmgo — full crypto/tls + net/http, ~9.5 MB
// gzip) and the 0magnet/tinygo fork (wasmtinygo — far smaller at ~2.9 MB gzip,
// with the documented TinyGo feature gaps). A wasm binary and its wasm_exec.js
// loader are toolchain-specific (TinyGo's loader provides the WASI shims its
// module imports), so the two always travel as a pair and are never mixed.
//
// What gets embedded is build-tag selected (see select_go.go / select_tinygo.go):
//   - a standard-Go build embeds BOTH pairs, so a visor serving the PWA can pick
//     the small TinyGo blob at serve time while keeping the Go one available;
//   - a TinyGo build embeds ONLY the TinyGo pair, to stay small (the Go blob
//     would add ~9.5 MB it has no reason to carry).
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
	"fmt"
	"io"
	"sort"
)

// Variant identifies which toolchain compiled an embedded wasm-visor.
type Variant string

const (
	// Go is the standard-Go-compiled wasm-visor: full crypto/tls + net/http,
	// larger (~9.5 MB gzip).
	Go Variant = "go"
	// TinyGo is the TinyGo-fork-compiled wasm-visor: much smaller (~2.9 MB gzip),
	// with the documented TinyGo feature gaps.
	TinyGo Variant = "tinygo"
)

// variants and defaultVariant are declared by the build-tag-selected file
// (select_go.go for standard Go, select_tinygo.go for TinyGo). Exactly one is
// compiled in, so there is no duplicate declaration.

// Embedded reports whether any wasm-visor binary is compiled in (true in a
// normal build — the blobs are committed).
func Embedded() bool { return len(variants) > 0 }

// Has reports whether the given variant is embedded in this binary. A TinyGo
// build has only TinyGo; a standard-Go build has both.
func Has(v Variant) bool { _, ok := variants[v]; return ok }

// Available returns the embedded variants, sorted for stable output.
func Available() []Variant {
	out := make([]Variant, 0, len(variants))
	for v := range variants {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Default returns the variant that the variant-less Get()/WasmExecJS() serve.
func Default() Variant { return defaultVariant }

// GetVariant returns the named wasm-visor binary, decompressed.
func GetVariant(v Variant) ([]byte, error) {
	b, ok := variants[v]
	if !ok {
		return nil, fmt.Errorf("wasmbin: wasm-visor variant %q not embedded in this build", v)
	}
	zr, err := gzip.NewReader(bytes.NewReader(b.gz))
	if err != nil {
		return nil, err
	}
	defer zr.Close() //nolint:errcheck
	return io.ReadAll(zr)
}

// Get returns the default embedded wasm-visor binary, decompressed.
func Get() ([]byte, error) { return GetVariant(defaultVariant) }

// GetVariantGz returns the named wasm-visor binary still gzipped, as committed
// (nil if that variant is not embedded).
//
// This is for handing the blob to a consumer that serves it compressed rather
// than one that instantiates it. Browsers decompress a Content-Encoding: gzip
// response themselves, and WebAssembly.instantiateStreaming is happy with the
// result, so passing the committed bytes through avoids inflating tens of
// megabytes per request on the server. Use GetVariant when you need the wasm
// itself.
func GetVariantGz(v Variant) []byte { return variants[v].gz }

// WasmExecJSVariant returns the wasm_exec.js loader matching the named variant
// (nil if that variant is not embedded). Serve it alongside GetVariant(v)'s
// bytes — a TinyGo wasm needs TinyGo's loader and a Go wasm needs Go's.
func WasmExecJSVariant(v Variant) []byte { return variants[v].execJS }

// WasmExecJS returns the wasm_exec.js loader matching the default variant.
func WasmExecJS() []byte { return WasmExecJSVariant(defaultVariant) }
