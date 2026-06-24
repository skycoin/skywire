// Package wasmbin carries the compiled wasm-visor binary (gzipped) inside the
// skywire binary, so `cli hv gen` — and, later, the visor serving the HV UI —
// produce a browser visor with NO external --wasm file, including from a plain
// `go install`.
//
// The embedded file (wasm-visor.wasm.gz, ~8 MB gzipped from the ~37 MB std-Go
// wasm-visor) is COMMITTED so it travels in the published Go module. It is
// updated INTENTIONALLY with `make embed-wasm-visor` (rebuild + deterministic
// re-gzip); ordinary `make wasm-visor` writes only to build/ and does not touch
// this file, so day-to-day builds don't churn the blob and git history only
// grows on deliberate embed updates. Those old blob versions can be pruned from
// history periodically — see docs/design/wasm-visor-distribution.md.
package wasmbin

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"io"
)

//go:embed wasm-visor.wasm.gz
var gzWasm []byte

// Embedded reports whether a wasm-visor binary is compiled in (true in a normal
// build — the file is committed).
func Embedded() bool { return len(gzWasm) > 0 }

// Get returns the embedded wasm-visor binary, decompressed.
func Get() ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(gzWasm))
	if err != nil {
		return nil, err
	}
	defer zr.Close() //nolint:errcheck
	return io.ReadAll(zr)
}
