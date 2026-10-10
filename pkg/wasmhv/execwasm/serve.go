// Package execwasm pkg/wasmhv/execwasm/serve.go c3-wasm-embed
package execwasm

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// ServeGz answers a GET for the module from gz, the gzipped module bytes, with
// stamp as its ETag (a conditional request short-circuits to 304). The bytes go
// out as-is with Content-Encoding: gzip when the client accepts it and are
// inflated on the fly otherwise, so the module is never held inflated in
// memory. len(gz)==0 is a 404. Content-Type and Cache-Control are the caller's
// to set.
func ServeGz(w http.ResponseWriter, r *http.Request, gz []byte, stamp string) {
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
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer zr.Close()      //nolint:errcheck
	_, _ = io.Copy(w, zr) //nolint:errcheck,gosec // G110: the module is our own build artifact, not untrusted input
}

// ServeEmbedded is ServeGz for the EMBEDDED module, streamed straight out of
// the binary's read-only mapping instead of a heap copy of it: same ETag, same
// 304, same gzip-or-inflate behavior, but nothing here allocates more than an
// io.Copy buffer no matter how many requests arrive. Content-Type and
// Cache-Control are the caller's to set, as with ServeGz. No module embedded
// is a 404 (Serve turns that into the redirect its callers want).
func ServeEmbedded(w http.ResponseWriter, r *http.Request) {
	n := Size()
	if n == 0 {
		http.NotFound(w, r)
		return
	}
	if s := Stamp(); s != "" {
		etag := `"` + s + `"`
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	f, err := Open()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close() //nolint:errcheck
	w.Header().Set("Vary", "Accept-Encoding")
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", strconv.FormatInt(n, 10))
		_, _ = io.Copy(w, f) //nolint:errcheck
		return
	}
	zr, err := gzip.NewReader(f)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer zr.Close()      //nolint:errcheck
	_, _ = io.Copy(w, zr) //nolint:errcheck,gosec // G110: the module is our own build artifact, not untrusted input
}
