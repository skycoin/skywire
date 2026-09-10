// Package visor pkg/visor/execwasm_serve.go c3-vis-api
package visor

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"

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
// embedded module. The embedded bytes are gzipped; they go out as-is with
// Content-Encoding: gzip when the client accepts it and are inflated on the
// fly otherwise, so the module is never held inflated in memory.
func serveExecWasm(w http.ResponseWriter, r *http.Request, path string) {
	w.Header().Set("Content-Type", "application/wasm")
	w.Header().Set("Cache-Control", "no-cache")
	if path != "" {
		http.ServeFile(w, r, path)
		return
	}
	serveExecWasmGz(w, r, execwasm.Gz(), execwasm.Stamp())
}

func serveExecWasmGz(w http.ResponseWriter, r *http.Request, gz []byte, stamp string) {
	if len(gz) == 0 {
		http.NotFound(w, r)
		return
	}
	if stamp != "" {
		etag := `"` + stamp + `"`
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.Header().Set("Vary", "Accept-Encoding")
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(gz) //nolint:errcheck
		return
	}
	zr, err := gzip.NewReader(strings.NewReader(string(gz)))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer zr.Close()      //nolint:errcheck
	_, _ = io.Copy(w, zr) //nolint:errcheck
}
