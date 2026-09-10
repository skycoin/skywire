//go:build js

// Package execwasm pkg/wasmhv/execwasm/embed_js.go c3-wasm-embed
package execwasm

// The js build IS the module; it never carries a copy of itself.
func embeddedGz() []byte { return nil }
