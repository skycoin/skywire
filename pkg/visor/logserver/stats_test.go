package logserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/stats"
)

// fakeStatsReader implements StatsReader with in-memory fixtures so
// the handler tests don't need a real bbolt file.
type fakeStatsReader struct {
	transports []*stats.TransportRecord
	tierData   map[string]map[string][]byte // tier → date → bitmap
	svcData    map[string]map[string][]byte
	getErr     error
}

func (f *fakeStatsReader) AllTransportRecords() ([]*stats.TransportRecord, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.transports, nil
}
func (f *fakeStatsReader) GetTransportRecord(id uuid.UUID) (*stats.TransportRecord, error) {
	for _, r := range f.transports {
		if r.ID == id {
			return r, nil
		}
	}
	return nil, nil
}
func (f *fakeStatsReader) TierNames() ([]string, error) {
	out := make([]string, 0, len(f.tierData))
	for k := range f.tierData {
		out = append(out, k)
	}
	return out, nil
}
func (f *fakeStatsReader) TierBitmap(tier string, date time.Time) ([]byte, error) {
	if m, ok := f.tierData[tier]; ok {
		return m[date.UTC().Format("2006-01-02")], nil
	}
	return nil, nil
}
func (f *fakeStatsReader) TierDates(tier string) ([]string, error) {
	if m, ok := f.tierData[tier]; ok {
		out := make([]string, 0, len(m))
		for d := range m {
			out = append(out, d)
		}
		return out, nil
	}
	return nil, nil
}
func (f *fakeStatsReader) ServiceNames() ([]string, error) {
	out := make([]string, 0, len(f.svcData))
	for k := range f.svcData {
		out = append(out, k)
	}
	return out, nil
}
func (f *fakeStatsReader) ServiceBitmap(svc string, date time.Time) ([]byte, error) {
	if m, ok := f.svcData[svc]; ok {
		return m[date.UTC().Format("2006-01-02")], nil
	}
	return nil, nil
}
func (f *fakeStatsReader) ServiceDates(svc string) ([]string, error) {
	if m, ok := f.svcData[svc]; ok {
		out := make([]string, 0, len(m))
		for d := range m {
			out = append(out, d)
		}
		return out, nil
	}
	return nil, nil
}

// newTestAPI builds a minimal API with the auth route group exposed
// so tests can hit /stats/* directly without assembling the rest of
// the log server. Whitelist is empty (open access) so we don't fight
// the auth middleware in tests — auth coverage belongs elsewhere.
func newTestAPI(t *testing.T, sr StatsReader) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	api := &API{
		logger:      logging.MustGetLogger("logserver-test"),
		startedAt:   time.Now(),
		statsReader: sr,
	}
	api.registerStatsRoutes(r.Group("/"))
	return r
}

func TestStatsTransportsLiveSnapshot(t *testing.T) {
	id := uuid.New()
	now := time.Now().UTC()
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	sr := &fakeStatsReader{
		transports: []*stats.TransportRecord{{
			ID:        id,
			Edges:     []cipher.PubKey{pk1, pk2},
			Type:      "stcpr",
			Label:     "automatic",
			FirstSeen: now.Add(-time.Hour),
			LastSeen:  now,
			Current: &stats.LiveSnapshot{
				SentBytes:    1000,
				RecvBytes:    2000,
				LatencyAvgMS: 200,
				SampledAt:    now,
			},
			Daily: []stats.DailyRollup{{Date: now.Format("2006-01-02"), SentBytes: 1000}},
		}},
	}

	r := newTestAPI(t, sr)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/stats/transports", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var got []statsTransportSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if len(got) != 1 || got[0].ID != id {
		t.Fatalf("unexpected response: %+v", got)
	}
	if got[0].Current == nil || got[0].Current.SentBytes != 1000 {
		t.Fatalf("Current snapshot lost in round trip: %+v", got[0].Current)
	}
	// /stats/transports should not carry Daily — that's history's job.
	body := w.Body.String()
	if contains(body, `"daily"`) {
		t.Fatalf("/stats/transports response should omit daily rollups; got: %s", body)
	}
}

func TestStatsTransportsHistoryFiltersByRange(t *testing.T) {
	id := uuid.New()
	sr := &fakeStatsReader{
		transports: []*stats.TransportRecord{{
			ID:   id,
			Type: "stcpr",
			Daily: []stats.DailyRollup{
				{Date: "2026-04-20", SentBytes: 100},
				{Date: "2026-04-23", SentBytes: 200},
				{Date: "2026-04-26", SentBytes: 300},
			},
		}},
	}
	r := newTestAPI(t, sr)
	url := "/stats/transports/history?since=2026-04-22T00:00:00Z&until=2026-04-25T00:00:00Z"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var got []statsTransportHistory
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Daily) != 1 || got[0].Daily[0].Date != "2026-04-23" {
		t.Fatalf("expected exactly one daily row for 2026-04-23, got %+v", got)
	}
}

func TestStatsTransportsHistoryIDFilter(t *testing.T) {
	want := uuid.New()
	other := uuid.New()
	sr := &fakeStatsReader{
		transports: []*stats.TransportRecord{
			{ID: want, Type: "stcpr", Daily: []stats.DailyRollup{{Date: "2026-04-26", SentBytes: 1}}},
			{ID: other, Type: "sudph", Daily: []stats.DailyRollup{{Date: "2026-04-26", SentBytes: 2}}},
		},
	}
	r := newTestAPI(t, sr)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/stats/transports/history?id="+want.String(), nil))
	var got []statsTransportHistory
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != want {
		t.Fatalf("id filter miss: %+v", got)
	}
}

func TestStatsTransportsHistoryRejectsBadTime(t *testing.T) {
	sr := &fakeStatsReader{}
	r := newTestAPI(t, sr)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/stats/transports/history?since=yesterday", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestStatsUptimeRendersBitmap(t *testing.T) {
	bm := make([]byte, stats.BitmapSize)
	stats.SetSlot(bm, 0)
	stats.SetSlot(bm, 144)
	stats.SetSlot(bm, 287)
	now := time.Now().UTC().Format("2006-01-02")
	sr := &fakeStatsReader{
		tierData: map[string]map[string][]byte{
			"dmsg": {now: bm},
		},
	}
	r := newTestAPI(t, sr)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/stats/uptime", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var got map[string]map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	rendered, ok := got["dmsg"][now]
	if !ok {
		t.Fatalf("missing dmsg/%s in response: %+v", now, got)
	}
	if len(rendered) != stats.SlotsPerDay {
		t.Fatalf("rendered len = %d, want %d", len(rendered), stats.SlotsPerDay)
	}
	if rendered[0] != '.' || rendered[144] != '.' || rendered[287] != '.' {
		t.Fatal("set slots not rendered as dots")
	}
}

func TestStatsServicesEndpointMirrorsUptimeShape(t *testing.T) {
	bm := make([]byte, stats.BitmapSize)
	stats.SetSlot(bm, 50)
	now := time.Now().UTC().Format("2006-01-02")
	sr := &fakeStatsReader{
		svcData: map[string]map[string][]byte{
			"vpn-server": {now: bm},
		},
	}
	r := newTestAPI(t, sr)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/stats/services", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var got map[string]map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["vpn-server"][now] == "" || got["vpn-server"][now][50] != '.' {
		t.Fatalf("vpn-server bitmap render miss: %+v", got)
	}
}

func TestStatsHandlersReturn503WhenReaderUnset(t *testing.T) {
	r := newTestAPI(t, nil)
	for _, path := range []string{
		"/stats/transports",
		"/stats/transports/history",
		"/stats/uptime",
		"/stats/services",
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: status = %d, want 503", path, w.Code)
		}
	}
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
