// Package httputil pkg/httputil/compress_test.go c0-com-util
package httputil

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(t *testing.T, h http.Handler, gzipOK bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if gzipOK {
		r.Header.Set("Accept-Encoding", "gzip, deflate")
	}
	w := httptest.NewRecorder()
	CompressMin(1024, gzip.DefaultCompression)(h).ServeHTTP(w, r)
	return w
}

func TestCompressMin_SmallStaysPlain(t *testing.T) {
	w := serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		mustWrite(t, w, []byte(`{"ok":true}`))
	}), true)
	if w.Code != http.StatusCreated || w.Header().Get("Content-Encoding") != "" || w.Body.String() != `{"ok":true}` {
		t.Fatalf("small body must pass through: code=%d enc=%q body=%q", w.Code, w.Header().Get("Content-Encoding"), w.Body.String())
	}
}

func TestCompressMin_LargeJSONIsGzipped(t *testing.T) {
	body := []byte("[" + strings.Repeat(`{"pk":"0123456789abcdef"},`, 300) + `{"pk":"end"}]`)
	w := serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Two writes: one below, one over the threshold.
		mustWrite(t, w, body[:500])
		mustWrite(t, w, body[500:])
	}), true)
	if w.Code != 200 || w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("large json must be gzipped: code=%d enc=%q", w.Code, w.Header().Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read gzip body: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("round trip mismatch: %d vs %d bytes", len(got), len(body))
	}
	if w.Body.Len() >= len(body) {
		t.Fatalf("no compression gain: %d >= %d", w.Body.Len(), len(body))
	}
}

func TestCompressMin_NonCompressibleAndNoAccept(t *testing.T) {
	big := bytes.Repeat([]byte{0xff, 0x00, 0x7a}, 2000)
	w := serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/wasm")
		mustWrite(t, w, big)
	}), true)
	if w.Header().Get("Content-Encoding") != "" || !bytes.Equal(w.Body.Bytes(), big) {
		t.Fatal("application/wasm must not be gzipped")
	}
	w = serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mustWrite(t, w, bytes.Repeat([]byte("a"), 5000))
	}), false)
	if w.Header().Get("Content-Encoding") != "" || w.Body.Len() != 5000 {
		t.Fatal("client without gzip must get plain bytes")
	}
}

func TestCompressMin_EarlyFlushStaysPlain(t *testing.T) {
	w := serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		mustWrite(t, w, []byte("first chunk\n"))
		w.(http.Flusher).Flush()
		mustWrite(t, w, bytes.Repeat([]byte("x"), 3000))
	}), true)
	if w.Header().Get("Content-Encoding") != "" || w.Body.Len() != len("first chunk\n")+3000 || !w.Flushed {
		t.Fatalf("flushed stream must stay plain: enc=%q len=%d flushed=%v", w.Header().Get("Content-Encoding"), w.Body.Len(), w.Flushed)
	}
}

// mustWrite writes a test handler's body and fails the test if it errors. A
// handler that cannot write is a real failure rather than something to blank
// out, and this repo lints with errcheck's check-blank on, so `_, _ =` is not
// available as a shortcut anyway.
func mustWrite(t *testing.T, w io.Writer, b []byte) {
	t.Helper()
	if _, err := w.Write(b); err != nil {
		t.Errorf("write: %v", err)
	}
}
