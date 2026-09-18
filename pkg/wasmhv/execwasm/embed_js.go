//go:build js

// Package execwasm pkg/wasmhv/execwasm/embed_js.go c3-wasm-embed
package execwasm

import (
	"io/fs"
)

// The js build IS the module; it never carries a copy of itself.
func embeddedOpen() (fs.File, error) { return nil, fs.ErrNotExist }

// No blob, so no size.
func embeddedSize() int64 { return 0 }

// The js build carries no blob, so it records no revision either.
func embeddedRevision() string { return "" }
