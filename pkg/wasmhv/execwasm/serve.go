// Package execwasm pkg/wasmhv/execwasm/serve.go c3-wasm-embed
package execwasm

import (
	"compress/gzip"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// Serve answers a GET for the module at path. A gzipped module goes out as is
// with Content-Encoding: gzip when the client accepts it and is inflated on
// the fly otherwise, so it is never held in memory; any other file is served
// as it is. Its Stamp is the ETag. Content-Type and Cache-Control are the
// caller's to set.
func Serve(w http.ResponseWriter, r *http.Request, path string) {
	if !strings.HasSuffix(path, ".gz") {
		http.ServeFile(w, r, path)
		return
	}
	f, err := os.Open(path) //nolint:gosec // the module the operator installed
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close() //nolint:errcheck
	fi, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if s := Stamp(path); s != "" {
		etag := `"` + s + `"`
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.Header().Set("Vary", "Accept-Encoding")
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
		_, _ = io.Copy(w, f) //nolint:errcheck
		return
	}
	zr, err := gzip.NewReader(f)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer zr.Close()      //nolint:errcheck
	_, _ = io.Copy(w, zr) //nolint:errcheck,gosec // G110: the module is our own build artifact
}
