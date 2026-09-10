// Package execwasm pkg/wasmhv/execwasm/serve_test.go c3-wasm-embed
package execwasm

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServeGz(t *testing.T) {
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
	ServeGz(w, r, gz, "abcd")
	if w.Code != 200 || w.Header().Get("Content-Encoding") != "gzip" || !bytes.Equal(w.Body.Bytes(), gz) {
		t.Fatalf("gzip path: code=%d enc=%q len=%d", w.Code, w.Header().Get("Content-Encoding"), w.Body.Len())
	}
	if w.Header().Get("ETag") != `"abcd"` {
		t.Fatalf("etag %q", w.Header().Get("ETag"))
	}

	// identity client gets it inflated.
	r = httptest.NewRequest(http.MethodGet, "/skywire.wasm", nil)
	w = httptest.NewRecorder()
	ServeGz(w, r, gz, "abcd")
	body, _ := io.ReadAll(w.Body)
	if w.Code != 200 || w.Header().Get("Content-Encoding") != "" || !bytes.Equal(body, raw) {
		t.Fatalf("identity path: code=%d enc=%q len=%d", w.Code, w.Header().Get("Content-Encoding"), len(body))
	}

	// conditional request short-circuits.
	r = httptest.NewRequest(http.MethodGet, "/skywire.wasm", nil)
	r.Header.Set("If-None-Match", `"abcd"`)
	w = httptest.NewRecorder()
	ServeGz(w, r, gz, "abcd")
	if w.Code != http.StatusNotModified {
		t.Fatalf("conditional: code=%d", w.Code)
	}

	// nothing embedded → 404.
	w = httptest.NewRecorder()
	ServeGz(w, httptest.NewRequest(http.MethodGet, "/skywire.wasm", nil), nil, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("absent: code=%d", w.Code)
	}
}

// TestServe covers the embedded-only entry point in whichever state this build
// is in: with a staged module it serves it under its stamp; without one it
// sends the browser to the page origin's /skywire.wasm.
func TestServe(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/tpviz-gl.wasm", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	Serve(w, r)
	if !Present() {
		if w.Code != http.StatusFound || w.Header().Get("Location") != OriginPath {
			t.Fatalf("absent: code=%d location=%q, want 302 → %s", w.Code, w.Header().Get("Location"), OriginPath)
		}
		t.Logf("no module embedded in this build: Serve redirects to %s", OriginPath)
		return
	}
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/wasm" || w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("present: code=%d ct=%q enc=%q", w.Code, w.Header().Get("Content-Type"), w.Header().Get("Content-Encoding"))
	}
	if w.Header().Get("ETag") != `"`+Stamp()+`"` || !bytes.Equal(w.Body.Bytes(), Gz()) {
		t.Fatalf("present: etag=%q stamp=%q len=%d", w.Header().Get("ETag"), Stamp(), w.Body.Len())
	}
}

func TestLoaderJS(t *testing.T) {
	exec := []byte("globalThis.Go = class { constructor() { this.argv = ['js']; } };\n")
	out := string(LoaderJS(exec, "cipher"))
	if !strings.HasPrefix(out, string(exec)) {
		t.Fatal("loader must start with the loader it wraps")
	}
	for _, want := range []string{"class extends _Go", "super();", "this.argv=['skywire','desk-host','--role','cipher']"} {
		if !strings.Contains(out, want) {
			t.Errorf("prelude missing %q", want)
		}
	}
}
