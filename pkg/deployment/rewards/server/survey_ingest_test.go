package rewardsserver

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

// A stored survey with a new reward address asks for a recalculation, and
// the same survey again does not.
func TestSurveyPushTriggersRecalc(t *testing.T) {
	wd := t.TempDir()
	pk, _ := cipher.GenerateKeyPair()
	s := &Scheduler{}
	SetScheduler(s)
	defer SetScheduler(nil)

	mux := http.NewServeMux()
	registerSurveyIngestRoutes(mux, wd)
	push := func(addr string) {
		t.Helper()
		body := `{"public_key":"` + pk.Hex() + `","skywire_version":"v1.3.112","skycoin_address":"` + addr + `"}`
		req := httptest.NewRequest(http.MethodPost, "/node-info", strings.NewReader(body))
		req.RemoteAddr = pk.Hex() + ":80"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
	}
	push("addr1")
	if p := s.takePending(); p != "survey "+pk.Hex() {
		t.Fatalf("first survey: pending %q", p)
	}
	push("addr1")
	if p := s.takePending(); p != "" {
		t.Fatalf("same survey: pending %q", p)
	}
	push("addr2")
	if p := s.takePending(); p != "survey "+pk.Hex() {
		t.Fatalf("new address: pending %q", p)
	}
}
