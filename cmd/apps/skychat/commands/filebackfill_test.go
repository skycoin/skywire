// Package commands cmd/apps/skychat/commands/filebackfill_test.go
//
// Unit coverage for the file-backfill request/re-send protocol: the
// outstanding-request registry, locating a held file by id, the holder-side
// envelope recognition, and the /request-file handler's validation.
package commands

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestRequestedFilesRegistry(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()
	clearRequestedFile("fid-1")

	if isRequestedFile("fid-1", pk) {
		t.Fatal("fresh registry should not report a request")
	}
	markFileRequested("fid-1", pk)
	if !isRequestedFile("fid-1", pk) {
		t.Error("after mark: should be requested from pk")
	}
	if isRequestedFile("fid-1", other) {
		t.Error("must not match a different peer")
	}
	if isRequestedFile("fid-other", pk) {
		t.Error("must not match a different id")
	}
	clearRequestedFile("fid-1")
	if isRequestedFile("fid-1", pk) {
		t.Error("after clear: should not report a request")
	}
}

func TestFindFileByID(t *testing.T) {
	dir, err := downloadsDir()
	if err != nil {
		t.Fatalf("downloadsDir: %v", err)
	}

	// Sender-copy naming: <id><ext>.
	sent := filepath.Join(dir, "abc123.png")
	if err := os.WriteFile(sent, []byte("img"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(sent) }) //nolint
	if p, ok := findFileByID("abc123", "photo.png"); !ok || p != sent {
		t.Errorf("sender-copy lookup: p=%q ok=%v, want %q", p, ok, sent)
	}

	// Received naming: safeFileName(name, id).
	recv := filepath.Join(dir, safeFileName("doc.pdf", "def456"))
	if err := os.WriteFile(recv, []byte("pdf"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(recv) }) //nolint
	if p, ok := findFileByID("def456", "doc.pdf"); !ok || p != recv {
		t.Errorf("received lookup: p=%q ok=%v, want %q", p, ok, recv)
	}

	if _, ok := findFileByID("no-such-id", "x.png"); ok {
		t.Error("absent file should not be found")
	}
}

func TestHandleFileRequestFrame_Parsing(t *testing.T) {
	if appLog == nil {
		appLog = func(string, ...any) {}
	}
	peer, _ := cipher.GenerateKeyPair()

	// Non-envelopes / wrong type / missing id → not consumed (fall through).
	for _, raw := range [][]byte{
		[]byte("hello plain"),
		[]byte("{malformed"),
		[]byte(`{"type":"chat-msg","id":"x"}`),
		[]byte(`{"type":"file-request"}`), // missing file_id
	} {
		if handleFileRequestFrame(context.Background(), peer, raw) {
			t.Errorf("payload %q should NOT be consumed", raw)
		}
	}

	// A valid request for a file we don't hold is consumed (true) without a
	// re-send (findFileByID misses → no goroutine spawned).
	req, _ := json.Marshal(fileReqMsg{Type: fileReqType, FileID: "not-held-xyz", Name: "x.png"}) //nolint:errcheck
	if !handleFileRequestFrame(context.Background(), peer, req) {
		t.Error("a valid file-request should be consumed")
	}
}

func TestHandleFileRequestFrame_HeldFileReSend(t *testing.T) {
	if appLog == nil {
		appLog = func(string, ...any) {}
	}
	peer, _ := cipher.GenerateKeyPair()

	// Seed a sender-copy (<id><ext>) so findFileByID hits and the holder path
	// re-sends. The re-send goroutine fails fast (fileMgr is nil in unit tests),
	// which is fine — we only assert the frame was recognized + consumed.
	dir, err := downloadsDir()
	if err != nil {
		t.Fatalf("downloadsDir: %v", err)
	}
	held := filepath.Join(dir, "held-req-id.png")
	if err := os.WriteFile(held, []byte("img-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(held) }) //nolint

	req, _ := json.Marshal(fileReqMsg{Type: fileReqType, FileID: "held-req-id", Name: "pretty.png"}) //nolint:errcheck
	if !handleFileRequestFrame(context.Background(), peer, req) {
		t.Error("a file-request for a held file should be consumed")
	}
}

func TestRequestFileHandler_Validation(t *testing.T) {
	if appLog == nil {
		appLog = func(string, ...any) {}
	}
	h := requestFileHandler(context.Background())

	// Wrong method.
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/request-file", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: code=%d, want 405", rr.Code)
	}

	// Missing file_id (checked before the PK).
	rr = httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/request-file", strings.NewReader(`{"pk":"x"}`)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("no file_id: code=%d, want 400", rr.Code)
	}

	// Malformed JSON body → 400 (decode error, before the field checks).
	rr = httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/request-file", strings.NewReader(`{not json`)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("bad body: code=%d, want 400", rr.Code)
	}

	// Bad PK.
	rr = httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/request-file", strings.NewReader(`{"pk":"not-a-pk","file_id":"x"}`)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("bad pk: code=%d, want 400", rr.Code)
	}

	// Valid request but standalone (appCl nil in tests) → 503, not a panic.
	pk, _ := cipher.GenerateKeyPair()
	body := `{"pk":"` + pk.Hex() + `","file_id":"x","file_name":"a.png"}`
	rr = httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodPost, "/request-file", strings.NewReader(body)))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("standalone: code=%d, want 503", rr.Code)
	}
}
