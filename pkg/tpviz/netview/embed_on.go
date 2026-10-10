//go:build !mobile

package netview

import _ "embed"

// WasmGz is the module, gzipped.
//
//go:embed netview.wasm.gz
var WasmGz []byte
