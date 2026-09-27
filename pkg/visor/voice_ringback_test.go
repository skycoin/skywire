// Package visor pkg/visor/voice_ringback_test.go
package visor

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	skycall "github.com/skycoin/skywire/pkg/skychat/call"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// TestRingbackSurvivesRestart: the tone a user picked is still what callers
// hear after the visor restarts, and clearing it leaves nothing behind.
func TestRingbackSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	log := logging.MustGetLogger("test")
	pk, _ := cipher.GenerateKeyPair()
	tone := []byte("OggS pretend opus")

	if err := saveRingback(dir, tone, "audio/ogg"); err != nil {
		t.Fatalf("save: %v", err)
	}
	mgr := skycall.NewManager(skycall.Config{LocalPK: pk})
	loadRingback(dir, mgr, log)
	if data, mime := mgr.Ringback(); !bytes.Equal(data, tone) || mime != "audio/ogg" {
		t.Fatalf("after reload: %q %q", data, mime)
	}

	if err := saveRingback(dir, nil, ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	for _, name := range []string{voiceRingbackFile, voiceRingbackFile + voiceRingbackMimeExt} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s left behind after clearing: %v", name, err)
		}
	}
	fresh := skycall.NewManager(skycall.Config{LocalPK: pk})
	loadRingback(dir, fresh, log)
	if data, _ := fresh.Ringback(); data != nil {
		t.Fatal("a cleared tone came back")
	}
}

// TestWriteRingbackIsOnlyEverAudio: a peer picked the type, so a type that is
// not audio is not what the page gets.
func TestWriteRingbackIsOnlyEverAudio(t *testing.T) {
	rr := httptest.NewRecorder()
	WriteRingback(rr, visorapi.VoiceRingback{Data: []byte("<script>"), Mime: "text/html"})
	if ct := rr.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("served as %q", ct)
	}
	rr = httptest.NewRecorder()
	WriteRingback(rr, visorapi.VoiceRingback{Data: []byte("x"), Mime: "audio/x-m4a"})
	if ct := rr.Header().Get("Content-Type"); ct != "audio/mp4" {
		t.Errorf("served as %q, want audio/mp4", ct)
	}
	if rr.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("served sniffable")
	}
}
