package visor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/logging"
)

func postBootReport(h http.HandlerFunc, body, client string) int {
	req := httptest.NewRequest(http.MethodPost, "/boot-report", strings.NewReader(body))
	req.Header.Set("X-Forwarded-For", client)
	w := httptest.NewRecorder()
	h(w, req)
	return w.Code
}

func TestBootReportHandler(t *testing.T) {
	h := bootReportHandler(logging.MustGetLogger("boot-report-test"))

	if code := postBootReport(h, `{"kind":"stalled","stage":"loading…","ms":120000,"console":["error: x"]}`, "a"); code != http.StatusNoContent {
		t.Fatalf("a report: status %d, want 204", code)
	}
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodGet, "/boot-report", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: status %d, want 405", w.Code)
	}
	if code := postBootReport(h, "not json", "b"); code != http.StatusBadRequest {
		t.Errorf("non-JSON: status %d, want 400", code)
	}
	if code := postBootReport(h, `{"kind":"`+strings.Repeat("x", bootReportMaxBytes)+`"}`, "c"); code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized: status %d, want 413", code)
	}
	for i := 0; i < bootReportPerWindow; i++ {
		postBootReport(h, `{"kind":"error"}`, "d")
	}
	if code := postBootReport(h, `{"kind":"error"}`, "d"); code != http.StatusTooManyRequests {
		t.Errorf("past the per-client limit: status %d, want 429", code)
	}
	if code := postBootReport(h, `{"kind":"error"}`, "e"); code != http.StatusNoContent {
		t.Errorf("another client: status %d, want 204", code)
	}
}

func TestBootReportLimiterWindowResets(t *testing.T) {
	l := &bootReportLimiter{perKey: make(map[string]int)}
	now := time.Now()
	for i := 0; i < bootReportPerWindow; i++ {
		l.allow("k", now)
	}
	if l.allow("k", now) {
		t.Fatal("allowed past the limit")
	}
	if !l.allow("k", now.Add(bootReportWindow)) {
		t.Fatal("still refused after the window")
	}
}

func TestDeskPageReportsBootFirst(t *testing.T) {
	page := string(deskShellHTML("<script src=\"/x.js\"></script>", "{}"))
	first := strings.Index(page, "<script")
	if first < 0 {
		t.Fatal("no script on the desk page")
	}
	end := strings.Index(page[first:], "</script>")
	if end < 0 || !strings.Contains(page[first:first+end], "__skywireBootReport = report") {
		t.Fatal("the boot reporter is not the first script on the desk page")
	}
	if !strings.Contains(page, "__skywireBootReport('failed'") {
		t.Error("a rejected boot is not reported")
	}
}
