// Package commands cmd/apps/skychat/commands/filexfer_handlers_test.go
//
// Unit coverage for the file-transfer HTTP surface and its helpers that don't
// need a live mesh: the enabled-networks gate, the upload/path source resolver,
// the /send-file request validation + dispatch branches, and the /files/<name>
// download handler (method guard, served bytes, explicit media Content-Type).
//
// Downloads-dir tests do NOT isolate to t.TempDir — with appCl nil,
// downloadsDir() resolves to skyenv.LocalPath/skychat-downloads, so each test
// writes uniquely-named files and removes them on cleanup (same convention as
// TestFindFileByID / TestSaveSentCopy).
package commands

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
)

func TestEnabledFileNets(t *testing.T) {
	origSkynet, origDmsg := useSkynet, useDmsg
	t.Cleanup(func() { useSkynet, useDmsg = origSkynet, origDmsg })

	cases := []struct {
		skynet, dmsg bool
		want         []appnet.Type
	}{
		{true, true, []appnet.Type{appnet.TypeSkynet, appnet.TypeDmsg}},
		{true, false, []appnet.Type{appnet.TypeSkynet}},
		{false, true, []appnet.Type{appnet.TypeDmsg}},
		{false, false, nil},
	}
	for _, tc := range cases {
		useSkynet, useDmsg = tc.skynet, tc.dmsg
		got := enabledFileNets()
		if len(got) != len(tc.want) {
			t.Fatalf("skynet=%v dmsg=%v: got %v, want %v", tc.skynet, tc.dmsg, got, tc.want)
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Errorf("skynet=%v dmsg=%v: got[%d]=%v, want %v", tc.skynet, tc.dmsg, i, got[i], tc.want[i])
			}
		}
	}
}

func TestResolveUploadOrPath_MultipartUpload(t *testing.T) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "holiday photo.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("PNG-CONTENT")); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/send-file", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if err := req.ParseMultipartForm(maxUploadMemory); err != nil {
		t.Fatalf("ParseMultipartForm: %v", err)
	}

	path, cleanup, name, err := resolveUploadOrPath(req)
	if err != nil {
		t.Fatalf("resolveUploadOrPath: %v", err)
	}
	if cleanup == nil {
		t.Fatal("multipart upload must return a cleanup func")
	}
	defer cleanup()

	// The original filename is preserved for the offer.
	if name != "holiday photo.png" {
		t.Errorf("name = %q, want original filename", name)
	}
	// The bytes were spilled to a real temp file we can read back.
	b, err := os.ReadFile(path) //nolint:gosec
	if err != nil || string(b) != "PNG-CONTENT" {
		t.Fatalf("temp upload content = %q err=%v", b, err)
	}
	// cleanup removes the temp file.
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("cleanup should remove the temp upload, stat err=%v", err)
	}
}

func TestResolveUploadOrPath_PathAndMissing(t *testing.T) {
	// Path-based (CLI) send: the "path" form value is returned verbatim, no cleanup.
	req := httptest.NewRequest(http.MethodPost, "/send-file",
		strings.NewReader("path=/tmp/some/clip.mp4"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	path, cleanup, name, err := resolveUploadOrPath(req)
	if err != nil {
		t.Fatalf("path send: %v", err)
	}
	if path != "/tmp/some/clip.mp4" || name != "clip.mp4" || cleanup != nil {
		t.Errorf("path send: path=%q name=%q cleanup!=nil=%v", path, name, cleanup != nil)
	}

	// Neither an upload nor a path → an actionable error (surfaces as 400).
	empty := httptest.NewRequest(http.MethodPost, "/send-file", strings.NewReader(""))
	empty.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if _, _, _, err := resolveUploadOrPath(empty); err == nil {
		t.Error("no file and no path should error")
	}
}

func TestSendFileHandler_Branches(t *testing.T) {
	if appLog == nil {
		appLog = func(string, ...any) {}
	}
	// Group fan-out must take the standalone (no-RPC) path deterministically.
	origPair := pairEnable
	pairEnable = false
	t.Cleanup(func() { pairEnable = origPair })

	h := sendFileHandler(context.Background())

	// Wrong method → 405.
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/send-file", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: code=%d, want 405", rr.Code)
	}

	// No upload and no path → resolveUploadOrPath fails → 400.
	rr = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/send-file", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("no file: code=%d, want 400 (body=%q)", rr.Code, rr.Body.String())
	}

	// A path with a malformed pk → 400 (bad pk), after the source resolves.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/send-file",
		strings.NewReader("path=/tmp/x.png&pk=not-a-pk"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("bad pk: code=%d, want 400", rr.Code)
	}

	// A valid pk but the file-transfer manager isn't running (fileMgr nil in
	// unit tests) → the send attempt fails at the transport → 502.
	pk, _ := cipher.GenerateKeyPair()
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/send-file",
		strings.NewReader("path=/tmp/x.png&pk="+pk.Hex()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Errorf("pk send with no fileMgr: code=%d, want 502 (body=%q)", rr.Code, rr.Body.String())
	}

	// A group send with no active CXO group (standalone, no RPC) → 503.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/send-file",
		strings.NewReader("path=/tmp/x.png&group=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("group send with no active group: code=%d, want 503 (body=%q)", rr.Code, rr.Body.String())
	}
}

func TestDownloadFileHandler(t *testing.T) {
	if appLog == nil {
		appLog = func(string, ...any) {}
	}
	dir, err := downloadsDir()
	if err != nil {
		t.Fatalf("downloadsDir: %v", err)
	}

	// Wrong method → 405.
	rr := httptest.NewRecorder()
	downloadFileHandler(rr, httptest.NewRequest(http.MethodPost, "/files/x.txt", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: code=%d, want 405", rr.Code)
	}

	// An existing file is served with its bytes.
	name := "dl-serve-unit.txt"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("hello-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) }) //nolint
	rr = httptest.NewRecorder()
	downloadFileHandler(rr, httptest.NewRequest(http.MethodGet, "/files/"+name, nil))
	if rr.Code != http.StatusOK || rr.Body.String() != "hello-bytes" {
		t.Errorf("serve: code=%d body=%q", rr.Code, rr.Body.String())
	}

	// A recognized media extension gets an explicit Content-Type the host's
	// mime table might otherwise miss (so <audio>/<video> play reliably).
	mp3 := "dl-clip-unit.mp3"
	mp3Path := filepath.Join(dir, mp3)
	if err := os.WriteFile(mp3Path, []byte("ID3-fake"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(mp3Path) }) //nolint
	rr = httptest.NewRecorder()
	downloadFileHandler(rr, httptest.NewRequest(http.MethodGet, "/files/"+mp3, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("mp3 serve: code=%d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "audio/mpeg" {
		t.Errorf("mp3 Content-Type = %q, want audio/mpeg", ct)
	}
}
