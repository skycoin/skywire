// Package logserver pkg/visor/logserver/wasm.go c3-vis-core
package logserver

import (
	"net/http"
	"path/filepath"
)

// SetWasmModule serves the js/wasm module at path and its manifest beside it
// under GET /wasm/, so other visors can refresh their copy from this one.
func (api *API) SetWasmModule(path string) {
	api.wasmModulePath = path
}

// wasmModule serves a file of the module set by SetWasmModule, as its bytes:
// the gzipped module is not given a Content-Encoding, since the caller checks
// it against the manifest's sha256.
func (api *API) wasmModule(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if api.wasmModulePath == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, filepath.Join(filepath.Dir(api.wasmModulePath), name))
	}
}
