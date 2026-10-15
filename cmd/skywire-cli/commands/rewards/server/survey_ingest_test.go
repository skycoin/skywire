package clirewardsserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// A visor that finds its stored survey unchanged does not push it again, so the
// checksum request has to keep the survey from aging out of cli log --cleanup.
func TestStoredChecksumRefreshesSurveyTime(t *testing.T) {
	wd := t.TempDir()
	pk, _ := cipher.GenerateKeyPair()
	survey := filepath.Join(wd, "log_backups", pk.Hex(), "node-info.json")
	if err := os.MkdirAll(filepath.Dir(survey), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(survey, []byte(`{"skywire_version":"v1.3.101"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-10 * 24 * time.Hour)
	if err := os.Chtimes(survey, old, old); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	registerSurveyIngestRoutes(mux, wd)
	req := httptest.NewRequest(http.MethodGet, "/node-info/stored-checksum", nil)
	req.RemoteAddr = pk.Hex() + ":80"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	info, err := os.Stat(survey)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(info.ModTime()) > time.Minute {
		t.Fatalf("survey mtime not refreshed: %s", info.ModTime())
	}
}
