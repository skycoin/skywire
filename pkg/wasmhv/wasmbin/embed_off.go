//go:build !embedwasm

package wasmbin

import "errors"

// ErrNotEmbedded is returned by Get when the wasm-visor binary was not compiled
// in (the default; build with `-tags embedwasm` after `make wasm-visor`).
var ErrNotEmbedded = errors.New("wasm-visor binary not embedded in this build " +
	"(build with `-tags embedwasm` after `make wasm-visor`, or pass --wasm)")

// Embedded reports whether a wasm-visor binary is compiled into this build.
func Embedded() bool { return false }

// Get returns ErrNotEmbedded: this build did not embed the wasm-visor binary.
func Get() ([]byte, error) { return nil, ErrNotEmbedded }
