package netgraph

import (
	"net/http"

	"github.com/skycoin/skywire/pkg/wasmhv"
	"github.com/skycoin/skywire/pkg/wasmhv/execwasm"
)

// The graph is drawn by cosmos-go, the WebGL engine tpviz uses, which runs as
// the netview role of the skywire js/wasm module the native binary embeds.

// EngineWasm serves the embedded module, gzipped as it is stored.
func EngineWasm(w http.ResponseWriter, r *http.Request) { execwasm.Serve(w, r) }

// EngineLoader serves Go's wasm_exec.js pinned to the netview role.
func EngineLoader(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(execwasm.LoaderJS(wasmhv.WasmExecJS, "netview")) //nolint:errcheck
}
