// Package httputil pkg/httputil/log_level_test.go c0-com-http
package httputil

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

// collectHook records every entry the logger emits.
type collectHook struct{ entries []*logrus.Entry }

func (h *collectHook) Levels() []logrus.Level { return logrus.AllLevels }
func (h *collectHook) Fire(e *logrus.Entry) error {
	h.entries = append(h.entries, e)
	return nil
}

func serveOnce(t *testing.T, level logrus.Level, status int, delay time.Duration) []*logrus.Entry {
	t.Helper()
	log := logrus.New()
	log.SetOutput(discardWriter{})
	log.SetLevel(level)
	hook := &collectHook{}
	log.AddHook(hook)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(delay)
		w.WriteHeader(status)
	})
	NewLogMiddleware(log)(next).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/p", nil))
	return hook.entries
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// The request log's level follows the outcome: an ordinary answered request is
// Debug (a service answering thousands a minute must not fill its log at Info),
// a server error is Warn, and a slow request is Info even when it succeeded.
func TestLogMiddleware_LevelByOutcome(t *testing.T) {
	one := func(status int, delay time.Duration) *logrus.Entry {
		es := serveOnce(t, logrus.DebugLevel, status, delay)
		require.Len(t, es, 1)
		return es[0]
	}

	require.Equal(t, logrus.DebugLevel, one(http.StatusOK, 0).Level)
	require.Equal(t, logrus.DebugLevel, one(http.StatusUnprocessableEntity, 0).Level,
		"a client's own malformed request must not be loggable at Info by that client")
	require.Equal(t, logrus.WarnLevel, one(http.StatusInternalServerError, 0).Level)

	old := RequestLogSlow
	RequestLogSlow = 10 * time.Millisecond
	defer func() { RequestLogSlow = old }()
	slow := one(http.StatusOK, 40*time.Millisecond)
	require.Equal(t, logrus.InfoLevel, slow.Level)
	require.Equal(t, "Served slow request.", slow.Message)
	RequestLogSlow = old

	// Every entry still carries the fields operators filter on.
	e := one(http.StatusOK, 0)
	require.Equal(t, "Served request.", e.Message)
	for _, k := range []string{"status", "took", "remote", "request", "method"} {
		require.Contains(t, e.Data, k)
	}
}

// At Info the ordinary request log is silent — the whole point of the change.
func TestLogMiddleware_QuietAtInfo(t *testing.T) {
	require.Empty(t, serveOnce(t, logrus.InfoLevel, http.StatusOK, 0))
	require.Len(t, serveOnce(t, logrus.InfoLevel, http.StatusBadGateway, 0), 1,
		"a server error must still be logged at Info")
}
