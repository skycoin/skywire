package netgraph

import (
	"net/http"

	"github.com/skycoin/skywire/pkg/tpviz/netview"
)

// The graph is drawn by cosmos-go, the WebGL engine tpviz uses, which runs in
// the netview module every build embeds.

// EngineWasm serves the netview module.
func EngineWasm(w http.ResponseWriter, r *http.Request) { netview.ServeWasm(w, r) }

// EngineLoader serves the netview module's wasm_exec.js.
func EngineLoader(w http.ResponseWriter, r *http.Request) { netview.ServeExecJS(w, r) }
