// Package commands cmd/apps/skychat/commands/filexfer_media_test.go
//
// Unit coverage for two media helpers the render-in-chat feature added:
// saveSentCopy (the sender keeps an id-named served copy so its own UI can
// render a thumbnail and re-requests can find the bytes) and the /thumb/<name>
// HTTP handler's serve/reject paths (only the 405 path was covered before).
package commands

import (
	"bytes"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/skycoin/skywire/pkg/skychat/xfer"
)

func TestSaveSentCopy(t *testing.T) {
	src := filepath.Join(t.TempDir(), "orig.png")
	if err := os.WriteFile(src, []byte("PNG-BYTES"), 0o600); err != nil {
		t.Fatal(err)
	}
	offer := xfer.Offer{ID: "ssc-1", Name: "orig.png"}

	if err := saveSentCopy(src, offer); err != nil {
		t.Fatalf("saveSentCopy: %v", err)
	}
	dir, err := downloadsDir()
	if err != nil {
		t.Fatalf("downloadsDir: %v", err)
	}
	saved := filepath.Join(dir, sentCopyName(offer)) // ssc-1.png
	t.Cleanup(func() { _ = os.Remove(saved) })

	b, err := os.ReadFile(saved)
	if err != nil || string(b) != "PNG-BYTES" {
		t.Fatalf("served copy content = %q err=%v", b, err)
	}
	// The kept copy is discoverable by id (so a backfill re-request finds it).
	if p, ok := findFileByID(offer.ID, offer.Name); !ok || p != saved {
		t.Errorf("findFileByID(%s) = %q ok=%v, want %q", offer.ID, p, ok, saved)
	}

	// A missing source is surfaced as an error (no copy written).
	if err := saveSentCopy(filepath.Join(t.TempDir(), "gone"), xfer.Offer{ID: "x", Name: "y.png"}); err == nil {
		t.Error("saveSentCopy should error when the source is missing")
	}
}

func TestThumbnailHandler_ServesJPEG(t *testing.T) {
	dir, err := downloadsDir()
	if err != nil {
		t.Fatalf("downloadsDir: %v", err)
	}
	name := "thumb-serve.png"
	path := filepath.Join(dir, name)
	writePNGAt(t, path, 800, 600)
	t.Cleanup(func() { _ = os.Remove(path) })

	rec := httptest.NewRecorder()
	thumbnailHandler(rec, httptest.NewRequest(http.MethodGet, "/thumb/"+name, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("Content-Type = %q, want image/jpeg", ct)
	}
	// Body must be a decodable JPEG, downscaled into the 640 box.
	img, err := jpeg.Decode(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("response body is not a valid JPEG: %v", err)
	}
	if b := img.Bounds(); b.Dx() > thumbMaxDim || b.Dy() > thumbMaxDim {
		t.Errorf("thumbnail %dx%d exceeds %d box", b.Dx(), b.Dy(), thumbMaxDim)
	}
}

func TestThumbnailHandler_NonImageReturns415(t *testing.T) {
	if appLog == nil {
		appLog = func(string, ...any) {}
	}
	dir, err := downloadsDir()
	if err != nil {
		t.Fatalf("downloadsDir: %v", err)
	}
	name := "thumb-notimage.txt"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	rec := httptest.NewRecorder()
	thumbnailHandler(rec, httptest.NewRequest(http.MethodGet, "/thumb/"+name, nil))
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("non-image /thumb: code=%d, want 415 (so the UI <img> onerror falls back to /files/)", rec.Code)
	}
}
