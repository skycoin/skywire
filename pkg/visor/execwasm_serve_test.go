// Package visor pkg/visor/execwasm_serve_test.go c3-vis-api
package visor

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServeExecWasmGz(t *testing.T) {
	raw := bytes.Repeat([]byte("\x00asm module bytes "), 512)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(raw)
	_ = zw.Close()
	gz := buf.Bytes()

	// gzip-accepting client gets the bytes as embedded.
	r := httptest.NewRequest(http.MethodGet, "/skywire.wasm", nil)
	r.Header.Set("Accept-Encoding", "gzip, br")
	w := httptest.NewRecorder()
	serveExecWasmGz(w, r, gz, "abcd")
	if w.Code != 200 || w.Header().Get("Content-Encoding") != "gzip" || !bytes.Equal(w.Body.Bytes(), gz) {
		t.Fatalf("gzip path: code=%d enc=%q len=%d", w.Code, w.Header().Get("Content-Encoding"), w.Body.Len())
	}
	if w.Header().Get("ETag") != `"abcd"` {
		t.Fatalf("etag %q", w.Header().Get("ETag"))
	}

	// identity client gets it inflated.
	r = httptest.NewRequest(http.MethodGet, "/skywire.wasm", nil)
	w = httptest.NewRecorder()
	serveExecWasmGz(w, r, gz, "abcd")
	body, _ := io.ReadAll(w.Body)
	if w.Code != 200 || w.Header().Get("Content-Encoding") != "" || !bytes.Equal(body, raw) {
		t.Fatalf("identity path: code=%d enc=%q len=%d", w.Code, w.Header().Get("Content-Encoding"), len(body))
	}

	// conditional request short-circuits.
	r = httptest.NewRequest(http.MethodGet, "/skywire.wasm", nil)
	r.Header.Set("If-None-Match", `"abcd"`)
	w = httptest.NewRecorder()
	serveExecWasmGz(w, r, gz, "abcd")
	if w.Code != http.StatusNotModified {
		t.Fatalf("conditional: code=%d", w.Code)
	}

	// nothing embedded → 404.
	w = httptest.NewRecorder()
	serveExecWasmGz(w, httptest.NewRequest(http.MethodGet, "/skywire.wasm", nil), nil, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("absent: code=%d", w.Code)
	}
}
