// Package visor pkg/visor/execwasm_serve.go c3-vis-api
package visor

import (
	"net/http"
	"os"

	"github.com/skycoin/skywire/pkg/wasmhv/execwasm"
)

// execModuleSource resolves where the skywire command module comes from: an
// explicit path (the --exec-wasm flag or hypervisor.wasm_serve.exec_wasm),
// else the module installed beside the binary. ok is false when there is none.
func execModuleSource(explicit string) (path string, ok bool) {
	if explicit != "" {
		return explicit, true
	}
	p := execwasm.DefaultPath()
	if p == "" {
		return "", false
	}
	if _, err := os.Stat(p); err != nil {
		return p, false
	}
	return p, true
}

// serveExecWasm answers GET /skywire.wasm from the module at path.
func serveExecWasm(w http.ResponseWriter, r *http.Request, path string) {
	w.Header().Set("Content-Type", "application/wasm")
	w.Header().Set("Cache-Control", "no-cache")
	execwasm.Serve(w, r, path)
}
