//go:build !mobile

// Package netview pkg/tpviz/netview/embed_on.go c3-app-rewards
package netview

import _ "embed"

// WasmGz is the module, gzipped.
//
//go:embed netview.wasm.gz
var WasmGz []byte
