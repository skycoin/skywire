//go:build js && !tinygo

// Package wasmbin pkg/wasmhv/wasmbin/select_js.go c3-vis-wasm
//
// The js/wasm build of the full skywire binary IS a browser visor — embedding
// wasm-visor blobs inside it would be wasm-in-wasm for ~11 MB of dead weight.
// A browser build has no HTTP listener to serve the PWA from anyway; blob
// lookups report absence and callers degrade the way a corrupt blob does.
package wasmbin

// blob is a gzipped wasm-visor paired with its matching wasm_exec.js loader.
type blob struct {
	gz     []byte
	execJS []byte
}

var (
	variants       = map[Variant]blob{}
	defaultVariant = Go
)
