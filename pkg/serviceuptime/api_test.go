package serviceuptime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newTempRecorder builds a Recorder backed by a fresh bbolt file with a fixed
// clock, so CurrentSessionHandler has a live in-flight session to serve.
func newTempRecorder(t *testing.T, now time.Time, cfg Config) *Recorder {
	t.Helper()
	cfg.Now = func() time.Time { return now }
	r, err := New(filepath.Join(t.TempDir(), "uptime.db"), cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() }) //nolint:errcheck
	return r
}

// doGet runs h against GET target and returns the recorded response.
func doGet(t *testing.T, h http.HandlerFunc, target string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, target, nil))
	return rr
}

// seedSessions writes rows starting at base+offset hours; returns their count.
func seedSessions(t *testing.T, s *Store, base time.Time, offsets ...int) {
	t.Helper()
	for _, h := range offsets {
		st := base.Add(time.Duration(h) * time.Hour)
		require.NoError(t, s.PutSession(SessionRecord{
			StartedAt: st,
			LastSeen:  st.Add(time.Hour),
			Version:   "v1",
			Service:   "test",
		}))
	}
}

func TestCurrentSessionHandler(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	r := newTempRecorder(t, now, Config{Service: "transport-discovery", Version: "v1.3.46"})

	rr := doGet(t, CurrentSessionHandler(r), "/uptime/now")

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	var got SessionRecord
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	require.Equal(t, "transport-discovery", got.Service)
	require.Equal(t, "v1.3.46", got.Version)
	require.True(t, got.StartedAt.Equal(now), "StartedAt = %v, want %v", got.StartedAt, now)
}

func TestSessionsHandler_AllAscending(t *testing.T) {
	s := newTempStore(t)
	base := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	seedSessions(t, s, base, 0, 5, 1, 10, 3)

	rr := doGet(t, SessionsHandler(s), "/uptime/sessions")

	require.Equal(t, http.StatusOK, rr.Code)
	var got []SessionRecord
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	require.Len(t, got, 5)
	for i := 1; i < len(got); i++ {
		require.Falsef(t, got[i].StartedAt.Before(got[i-1].StartedAt),
			"not ascending: %v before %v", got[i-1].StartedAt, got[i].StartedAt)
	}
}

func TestSessionsHandler_SinceRFC3339(t *testing.T) {
	s := newTempStore(t)
	base := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	seedSessions(t, s, base, 0, 1, 5, 10)

	since := base.Add(2 * time.Hour).Format(time.RFC3339)
	rr := doGet(t, SessionsHandler(s), "/uptime/sessions?since="+since)

	require.Equal(t, http.StatusOK, rr.Code)
	var got []SessionRecord
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	require.Len(t, got, 2) // +0 and +1 dropped
}

func TestSessionsHandler_SinceUnix(t *testing.T) {
	s := newTempStore(t)
	base := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	seedSessions(t, s, base, 0, 1, 5, 10)

	since := strconv.FormatInt(base.Add(2*time.Hour).Unix(), 10)
	rr := doGet(t, SessionsHandler(s), "/uptime/sessions?since="+since)

	require.Equal(t, http.StatusOK, rr.Code)
	var got []SessionRecord
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	require.Len(t, got, 2)
}

func TestSessionsHandler_SinceInvalid(t *testing.T) {
	s := newTempStore(t)
	rr := doGet(t, SessionsHandler(s), "/uptime/sessions?since=not-a-date")
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestSessionsHandler_Limit(t *testing.T) {
	s := newTempStore(t)
	base := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	seedSessions(t, s, base, 0, 1, 2, 3, 4)

	// limit=2 → the two most-recent (tail of the ascending list).
	rr := doGet(t, SessionsHandler(s), "/uptime/sessions?limit=2")
	require.Equal(t, http.StatusOK, rr.Code)
	var got []SessionRecord
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	require.Len(t, got, 2)
	require.True(t, got[len(got)-1].StartedAt.Equal(base.Add(4*time.Hour)))
	require.True(t, got[0].StartedAt.Equal(base.Add(3*time.Hour)))

	// limit >= len and non-numeric are both ignored → all rows returned.
	for _, q := range []string{"limit=99", "limit=abc"} {
		rr := doGet(t, SessionsHandler(s), "/uptime/sessions?"+q)
		var all []SessionRecord
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &all))
		require.Lenf(t, all, 5, "query %q should return all rows", q)
	}
}

func TestTimelineHandler(t *testing.T) {
	s := newTempStore(t)
	day := time.Date(2026, 4, 28, 0, 0, 0, 0, time.UTC)
	for _, slot := range []int{0, 100, 287} {
		require.NoError(t, s.MarkSlot(day, slot))
	}

	rr := doGet(t, TimelineHandler(s), "/uptime/timeline?date=2026-04-28")

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "application/octet-stream", rr.Header().Get("Content-Type"))
	require.Equal(t, "2026-04-28", rr.Header().Get("X-Uptime-Date"))
	bm := rr.Body.Bytes()
	require.Len(t, bm, 36) // 288 slots / 8 bits
	for _, slot := range []int{0, 100, 287} {
		require.Truef(t, GetSlot(bm, slot), "slot %d should be set", slot)
	}
	require.False(t, GetSlot(bm, 1))
}

func TestTimelineHandler_DefaultsToToday(t *testing.T) {
	s := newTempStore(t)
	today := time.Now().UTC().Format(dateFmt)

	rr := doGet(t, TimelineHandler(s), "/uptime/timeline")

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, today, rr.Header().Get("X-Uptime-Date"))
	require.Len(t, rr.Body.Bytes(), 36)
}

func TestTimelineHandler_InvalidDate(t *testing.T) {
	s := newTempStore(t)
	rr := doGet(t, TimelineHandler(s), "/uptime/timeline?date=28-04-2026")
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestDatesHandler(t *testing.T) {
	s := newTempStore(t)
	want := []string{"2026-04-26", "2026-04-28"}
	for _, d := range want {
		day, err := time.Parse(dateFmt, d)
		require.NoError(t, err)
		require.NoError(t, s.MarkSlot(day, 0))
	}

	rr := doGet(t, DatesHandler(s), "/uptime/dates")

	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	var got []string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &got))
	require.ElementsMatch(t, want, got)
}
