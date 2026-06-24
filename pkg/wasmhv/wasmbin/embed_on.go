//go:build embedwasm

// Package wasmbin optionally carries the compiled wasm-visor binary inside the
// skywire binary, so `cli hv gen` (and, later, the visor serving the HV UI) can
// produce a browser visor with NO external --wasm file.
//
// The binary is embedded ONLY under the `embedwasm` build tag, and the embedded
// file (wasm-visor.wasm.gz, ~8.5MB) is .gitignore'd — it is produced by
// `make wasm-visor` and never committed, so git history does not accumulate a
// new ~38MB blob on every build. Default builds (no tag) use the stub in
// embed_off.go and carry nothing. See docs/design/wasm-visor-distribution.md.
package wasmbin

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"io"
)

//go:embed wasm-visor.wasm.gz
var gzWasm []byte

// Embedded reports whether a wasm-visor binary is compiled into this build.
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
