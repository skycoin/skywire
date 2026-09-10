// Package visor pkg/visor/execwasm_serve.go c3-vis-api
package visor

import (
	"net/http"

	"github.com/skycoin/skywire/pkg/wasmhv/execwasm"
)

// execModuleSource resolves where the skywire command module comes from:
// an explicit path (config or flag), else the embedded module, else the
// package location on disk. path=="" with ok=true means embedded.
func execModuleSource(explicit string) (path string, ok bool) {
	if explicit != "" {
		return explicit, true
	}
	if execwasm.Present() {
		return "", true
	}
	if p := DefaultExecWasmPath(); p != "" {
		return p, true
	}
	return "", false
}

// serveExecWasm answers GET /skywire.wasm from path when set, else from the
// embedded module (execwasm.ServeGz: gzip as embedded when the client accepts
// it, inflated on the fly otherwise).
func serveExecWasm(w http.ResponseWriter, r *http.Request, path string) {
	w.Header().Set("Content-Type", "application/wasm")
	w.Header().Set("Cache-Control", "no-cache")
	if path != "" {
		http.ServeFile(w, r, path)
		return
	}
	serveExecWasmGz(w, r, execwasm.Gz(), execwasm.Stamp())
}

// serveExecWasmGz is execwasm.ServeGz; the surfaces that serve the module in
// a role (tpviz, the wallet cipher) call it there without importing this
// package.
func serveExecWasmGz(w http.ResponseWriter, r *http.Request, gz []byte, stamp string) {
	execwasm.ServeGz(w, r, gz, stamp)
}
