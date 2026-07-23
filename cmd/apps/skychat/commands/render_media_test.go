// Package commands cmd/apps/skychat/commands/render_media_test.go
//
// Coverage for the media-in-chat additive fields on the legacy /sse
// stream: a file event carries file_* metadata (+ a ready-made
// /files/ download URL on received files), while plain text events stay
// byte-shape-compatible (no file_* keys) so `cli skychat listen` is
// unaffected.
package commands

import (
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/skycoin/skywire/cmd/apps/skychat/history"
	"github.com/skycoin/skywire/pkg/skychat/xfer"
)

func TestRenderLegacySSE_FileFields(t *testing.T) {
	ev := chatEvent{
		ID: "f1", Channel: channelDM, Dir: "in", From: "alice",
		Text:       "📎 photo.png (2.0 KB)",
		FileID:     "f1",
		FileName:   "photo.png",
		FileSize:   2048,
		FileStatus: "received",
		FilePath:   "/var/lib/skychat-downloads/photo.png",
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(renderLegacySSE(ev)), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m["file_id"] != "f1" || m["file_name"] != "photo.png" || m["file_status"] != "received" {
		t.Errorf("file fields missing/wrong: %v", m)
	}
	if sz, ok := m["file_size"].(float64); !ok || sz != 2048 {
		t.Errorf("file_size = %v, want 2048", m["file_size"])
	}
	// file_url is the /files/<basename-of-FilePath> download path.
	if m["file_url"] != "/files/photo.png" {
		t.Errorf("file_url = %v, want /files/photo.png", m["file_url"])
	}
}

func TestRenderLegacySSE_NoFileFieldsOnPlainText(t *testing.T) {
	ev := chatEvent{ID: "t1", Dir: "in", From: "bob", Text: "hi", Transport: "dmsg"}
	var m map[string]any
	if err := json.Unmarshal([]byte(renderLegacySSE(ev)), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, k := range []string{"file_id", "file_name", "file_size", "file_url", "file_status"} {
		if _, ok := m[k]; ok {
			t.Errorf("plain text event should not carry %q (back-compat)", k)
		}
	}
}

func TestRenderLegacySSE_OutFileHasNoURL(t *testing.T) {
	// The sender's own "out" file event has no saved local path, so no
	// file_url — but the descriptive file_* fields are still present.
	ev := chatEvent{
		ID: "f2", Dir: "out", To: "carol",
		FileID: "f2", FileName: "doc.pdf", FileSize: 10, FileStatus: "sent",
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(renderLegacySSE(ev)), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := m["file_url"]; ok {
		t.Error("out file event should not carry file_url (no saved local path)")
	}
	if m["file_id"] != "f2" || m["file_status"] != "sent" {
		t.Errorf("out file fields wrong: %v", m)
	}
}

// writeTestPNG writes a solid w×h PNG to a temp file and returns its path.
func writeTestPNG(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 128, 255})
		}
	}
	path := filepath.Join(t.TempDir(), "img.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMakeThumbnail_DownscalesPreservingAspect(t *testing.T) {
	thumb, err := makeThumbnail(writeTestPNG(t, 1000, 800), 640)
	if err != nil {
		t.Fatalf("makeThumbnail: %v", err)
	}
	b := thumb.Bounds()
	// 1000x800 fit into a 640 box -> 640x512, aspect preserved.
	if b.Dx() != 640 || b.Dy() != 512 {
		t.Errorf("thumbnail = %dx%d, want 640x512", b.Dx(), b.Dy())
	}
}

func TestMakeThumbnail_SmallImageNotUpscaled(t *testing.T) {
	// A source smaller than the box stays its own size (Fit never enlarges).
	thumb, err := makeThumbnail(writeTestPNG(t, 100, 60), 640)
	if err != nil {
		t.Fatalf("makeThumbnail: %v", err)
	}
	if b := thumb.Bounds(); b.Dx() != 100 || b.Dy() != 60 {
		t.Errorf("small image = %dx%d, want 100x60 (no upscale)", b.Dx(), b.Dy())
	}
}

func TestMakeThumbnail_NonImageAndMissingError(t *testing.T) {
	txt := filepath.Join(t.TempDir(), "notimage.txt")
	if err := os.WriteFile(txt, []byte("plain text, not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := makeThumbnail(txt, 640); err == nil {
		t.Error("makeThumbnail should error on a non-image file")
	}
	if _, err := makeThumbnail(filepath.Join(t.TempDir(), "missing.png"), 640); err == nil {
		t.Error("makeThumbnail should error on a missing file")
	}
}

func TestThumbnailHandler_RejectsNonGet(t *testing.T) {
	rec := httptest.NewRecorder()
	thumbnailHandler(rec, httptest.NewRequest(http.MethodPost, "/thumb/x.png", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /thumb: code=%d, want 405", rec.Code)
	}
}

func TestMediaContentType(t *testing.T) {
	cases := map[string]string{
		"clip.mp4":   "video/mp4",
		"movie.MOV":  "video/quicktime", // case-insensitive
		"v.webm":     "video/webm",
		"a.ogv":      "video/ogg",
		"song.mp3":   "audio/mpeg",
		"sound.ogg":  "audio/ogg",
		"voice.m4a":  "audio/mp4",
		"track.flac": "audio/flac",
		"rec.opus":   "audio/opus",
		"s.wav":      "audio/wav",
		// Non-media / unknown → empty (falls through to ServeFile's sniffing).
		"photo.png":   "",
		"doc.pdf":     "",
		"archive.zip": "",
		"noext":       "",
	}
	for name, want := range cases {
		if got := mediaContentType(name); got != want {
			t.Errorf("mediaContentType(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestHistoryHandler_EmitsFileFields confirms a persisted file message
// surfaces its file_* metadata through the /history JSON, so a fresh
// browser can re-render the media (thumbnail/player) from history.
func TestHistoryHandler_EmitsFileFields(t *testing.T) {
	st, err := history.NewBoltStore(filepath.Join(t.TempDir(), "hist.db"), history.DefaultLimits())
	if err != nil {
		t.Fatalf("NewBoltStore: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() }) //nolint:errcheck
	if err := st.Append(history.Message{
		Peer: "peerpk", From: "peerpk", Text: "📎 pic.png",
		FileName: "pic.png", FileSize: 10, FileStatus: "received", FileURL: "/files/pic.png",
		Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	prev := historyStore
	historyStore = st
	t.Cleanup(func() { historyStore = prev })

	rec := httptest.NewRecorder()
	historyHandler(rec, httptest.NewRequest(http.MethodGet, "/history?limit=10", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
	var msgs []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &msgs); err != nil {
		t.Fatalf("decode /history: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("want 1 message, got %d", len(msgs))
	}
	if msgs[0]["file_name"] != "pic.png" || msgs[0]["file_url"] != "/files/pic.png" || msgs[0]["file_status"] != "received" {
		t.Errorf("/history did not emit file fields: %v", msgs[0])
	}
}

func TestSentCopyName(t *testing.T) {
	cases := []struct{ id, name, want string }{
		{"a1b2c3", "photo.png", "a1b2c3.png"},
		{"deadbeef", "clip.MP4", "deadbeef.MP4"}, // extension case preserved
		{"cafe01", "noext", "cafe01"},
	}
	for _, c := range cases {
		got := sentCopyName(xfer.Offer{ID: c.id, Name: c.name})
		if got != c.want {
			t.Errorf("sentCopyName(%s,%s) = %q, want %q", c.id, c.name, got, c.want)
		}
		// Must be safeFileName-idempotent so /files/ + /thumb/ resolve it unchanged.
		if safeFileName(got, "") != got {
			t.Errorf("sentCopyName %q not safeFileName-idempotent (-> %q)", got, safeFileName(got, ""))
		}
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	if err := os.WriteFile(src, []byte("hello copy 123"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "dst.bin")
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("copyFile: %v", err)
	}
	b, err := os.ReadFile(dst)
	if err != nil || string(b) != "hello copy 123" {
		t.Errorf("copied content = %q err=%v", b, err)
	}
	if err := copyFile(filepath.Join(dir, "missing"), dst); err == nil {
		t.Error("copyFile should error on a missing source")
	}
}
