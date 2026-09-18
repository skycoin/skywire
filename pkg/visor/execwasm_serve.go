// Package visor pkg/visor/execwasm_serve.go c3-vis-api
package visor

import (
	"net/http"

	"github.com/skycoin/skywire/pkg/wasmhv/execwasm"
)

// execModuleSource resolves where the skywire command module comes from: an
// explicit path (the --exec-wasm flag or hypervisor.wasm_serve.exec_wasm, a
// developer override), else the module embedded by the two-stage build.
// path=="" with ok=true means embedded; ok=false means this build has none.
func execModuleSource(explicit string) (path string, ok bool) {
	if explicit != "" {
		return explicit, true
	}
	return "", execwasm.Present()
}

// serveExecWasm answers GET /skywire.wasm from path when set, else from the
// embedded module (execwasm.ServeEmbedded: gzip as embedded when the client
// accepts it, inflated on the fly otherwise, streamed out of the binary's
// read-only mapping rather than a heap copy of it).
func serveExecWasm(w http.ResponseWriter, r *http.Request, path string) {
	w.Header().Set("Content-Type", "application/wasm")
	w.Header().Set("Cache-Control", "no-cache")
	if path != "" {
		http.ServeFile(w, r, path)
		return
	}
	execwasm.ServeEmbedded(w, r)
}
