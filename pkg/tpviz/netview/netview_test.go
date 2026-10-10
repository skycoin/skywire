package netview

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServeWasm(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x.wasm", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	ServeWasm(rec, req)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Encoding") != "gzip" || !bytes.Equal(rec.Body.Bytes(), WasmGz) {
		t.Fatalf("gzip: status %d, encoding %q, %d bytes", rec.Code, rec.Header().Get("Content-Encoding"), rec.Body.Len())
	}

	rec = httptest.NewRecorder()
	ServeWasm(rec, httptest.NewRequest(http.MethodGet, "/x.wasm", nil))
	zr, err := gzip.NewReader(bytes.NewReader(WasmGz))
	if err != nil {
		t.Fatal(err)
	}
	want, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Encoding") != "" || !bytes.Equal(rec.Body.Bytes(), want) {
		t.Fatalf("plain: status %d, encoding %q, %d bytes", rec.Code, rec.Header().Get("Content-Encoding"), rec.Body.Len())
	}
	if !bytes.HasPrefix(want, []byte("\x00asm")) {
		t.Fatal("embedded module is not wasm")
	}

	req = httptest.NewRequest(http.MethodGet, "/x.wasm", nil)
	req.Header.Set("If-None-Match", ETag)
	rec = httptest.NewRecorder()
	ServeWasm(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("If-None-Match: status %d", rec.Code)
	}
}
