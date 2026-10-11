// Package execwasm pkg/wasmhv/execwasm/serve_test.go c3-wasm-embed
package execwasm

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func writeModule(t *testing.T, raw []byte, manifest bool) (path string, gz []byte) {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	gz = buf.Bytes()
	path = filepath.Join(t.TempDir(), ModuleName)
	if err := os.WriteFile(path, gz, 0o600); err != nil {
		t.Fatal(err)
	}
	if manifest {
		sum := sha256.Sum256(gz)
		m := fmt.Sprintf(`{"version":"v1","revision":"r","sha256":%q,"size":%d}`, hex.EncodeToString(sum[:]), len(gz))
		if err := os.WriteFile(ManifestPath(path), []byte(m), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path, gz
}

func TestServe(t *testing.T) {
	raw := bytes.Repeat([]byte("\x00asm module bytes "), 512)
	path, gz := writeModule(t, raw, true)
	sum := sha256.Sum256(gz)
	stamp := hex.EncodeToString(sum[:])[:16]
	if got := Stamp(path); got != stamp {
		t.Fatalf("Stamp = %q, want the manifest's %q", got, stamp)
	}

	r := httptest.NewRequest(http.MethodGet, "/skywire.wasm", nil)
	r.Header.Set("Accept-Encoding", "gzip, br")
	w := httptest.NewRecorder()
	Serve(w, r, path)
	if w.Code != 200 || w.Header().Get("Content-Encoding") != "gzip" || !bytes.Equal(w.Body.Bytes(), gz) {
		t.Fatalf("gzip client: code %d encoding %q, %d bytes", w.Code, w.Header().Get("Content-Encoding"), w.Body.Len())
	}
	if w.Header().Get("ETag") != `"`+stamp+`"` {
		t.Fatalf("ETag = %q", w.Header().Get("ETag"))
	}

	r = httptest.NewRequest(http.MethodGet, "/skywire.wasm", nil)
	w = httptest.NewRecorder()
	Serve(w, r, path)
	if w.Code != 200 || w.Header().Get("Content-Encoding") != "" || !bytes.Equal(w.Body.Bytes(), raw) {
		t.Fatalf("plain client: code %d encoding %q", w.Code, w.Header().Get("Content-Encoding"))
	}

	r = httptest.NewRequest(http.MethodGet, "/skywire.wasm", nil)
	r.Header.Set("If-None-Match", `"`+stamp+`"`)
	w = httptest.NewRecorder()
	Serve(w, r, path)
	if w.Code != http.StatusNotModified {
		t.Fatalf("conditional request: code %d", w.Code)
	}

	w = httptest.NewRecorder()
	Serve(w, httptest.NewRequest(http.MethodGet, "/skywire.wasm", nil), filepath.Join(t.TempDir(), ModuleName))
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing module: code %d", w.Code)
	}
}

func TestStampWithoutManifest(t *testing.T) {
	path, _ := writeModule(t, []byte("module"), false)
	s := Stamp(path)
	if len(s) != 16 {
		t.Fatalf("Stamp = %q, want a size and mtime fingerprint", s)
	}
	if Stamp("") != "" || Stamp(filepath.Join(t.TempDir(), "none")) != "" {
		t.Fatal("Stamp of no module is not empty")
	}
}
