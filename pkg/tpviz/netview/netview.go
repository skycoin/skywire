// Package netview pkg/tpviz/netview/netview.go c3-vis-api
//
// Package netview embeds the WebGL graph view (cmd/netview) as a js/wasm
// module of its own, about 1 MB gzipped, so every build serves it. Rebuild it
// with `make netview-wasm`; `make check-netview-wasm` fails when it is stale.
package netview

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"

	"github.com/skycoin/skywire/pkg/wasmhv"
)

// ExecJS is the loader for WasmGz. The module is built with the toolchain
// that wasmhv.WasmExecJS comes from.
var ExecJS = wasmhv.WasmExecJS

// ETag names this build of the module.
var ETag = func() string {
	h := sha256.Sum256(WasmGz)
	return `"` + hex.EncodeToString(h[:8]) + `"`
}()

// ServeWasm answers a GET for the module, gzipped as it is stored, or
// inflated for a client that does not accept gzip.
func ServeWasm(w http.ResponseWriter, r *http.Request) {
	if len(WasmGz) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/wasm")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", ETag)
	w.Header().Add("Vary", "Accept-Encoding")
	if r.Header.Get("If-None-Match") == ETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(WasmGz) //nolint:errcheck
		return
	}
	zr, err := gzip.NewReader(bytes.NewReader(WasmGz))
	if err != nil {
		http.Error(w, "netview: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = io.Copy(w, zr) //nolint:errcheck,gosec // our own embedded module
}

// ServeExecJS answers a GET for the loader.
func ServeExecJS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(ExecJS) //nolint:errcheck
}
