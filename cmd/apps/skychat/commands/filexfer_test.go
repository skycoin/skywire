// Package commands cmd/apps/skychat/filexfer_test.go
package commands

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSafeFileName(t *testing.T) {
	cases := []struct {
		in, id, want string
	}{
		{"photo.png", "x", "photo.png"},
		{"../../etc/passwd", "x", "passwd"},
		{"/abs/path/file.txt", "x", "file.txt"},
		{"nested/dir/name.dat", "x", "name.dat"},
		{"", "abc", "file-abc"},
		{"...", "abc", "file-abc"},
		{`..\..\windows\evil.exe`, "x", "evil.exe"},
	}
	for _, c := range cases {
		if got := safeFileName(c.in, c.id); got != c.want {
			t.Errorf("safeFileName(%q,%q) = %q, want %q", c.in, c.id, got, c.want)
		}
		// A sanitized name must never contain a path separator or traversal.
		got := safeFileName(c.in, c.id)
		if strings.ContainsAny(got, `/\`) || strings.Contains(got, "..") {
			t.Errorf("safeFileName(%q) leaked a path: %q", c.in, got)
		}
	}
}

func TestHumanSize(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}
	for _, c := range cases {
		if got := humanSize(c.n); got != c.want {
			t.Errorf("humanSize(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

// TestSendFileHandlerRejects covers the validation paths that don't need a live
// transport: wrong method, missing target, and no file source.
func TestSendFileHandlerRejects(t *testing.T) {
	h := sendFileHandler(context.Background())

	// GET is rejected.
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/send-file", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: code = %d, want 405", rec.Code)
	}

	// POST with a path but neither pk nor group → 400.
	form := url.Values{"path": {"/tmp/whatever"}}
	req := httptest.NewRequest(http.MethodPost, "/send-file", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("no target: code = %d, want 400 (body %q)", rec.Code, rec.Body.String())
	}

	// POST with a pk but no file source → 400 from resolveUploadOrPath.
	form = url.Values{"pk": {"0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"}}
	req = httptest.NewRequest(http.MethodPost, "/send-file", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("no file: code = %d, want 400 (body %q)", rec.Code, rec.Body.String())
	}
}

// TestDownloadFileHandlerMissing confirms a request for a file that isn't in the
// downloads dir 404s rather than escaping the dir.
func TestDownloadFileHandlerMissing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/files/../../../etc/passwd", nil)
	rec := httptest.NewRecorder()
	downloadFileHandler(rec, req)
	// The traversal is sanitized to "passwd"; with no such file in the downloads
	// dir the result is 404, never the real /etc/passwd.
	if rec.Code == http.StatusOK {
		t.Fatalf("download of traversal path returned 200 — sanitizer failed")
	}
}
