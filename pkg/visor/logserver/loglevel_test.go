// Package logserver pkg/visor/logserver/loglevel_test.go c3-vis-core
package logserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
)

type fakeLogLevelController struct {
	level   logrus.Level
	ttl     time.Duration
	cleared bool
}

func (f *fakeLogLevelController) LogLevelStatus() LogLevelStatus {
	return LogLevelStatus{Level: f.level.String()}
}

func (f *fakeLogLevelController) SetTempLogLevel(level logrus.Level, ttl time.Duration) (LogLevelStatus, error) {
	if ttl > time.Hour {
		return LogLevelStatus{}, errors.New("ttl too long")
	}
	f.level, f.ttl = level, ttl
	return f.LogLevelStatus(), nil
}

func (f *fakeLogLevelController) ClearTempLogLevel() LogLevelStatus {
	f.cleared = true
	return f.LogLevelStatus()
}

func newLogLevelTestAPI(c LogLevelController) *http.ServeMux {
	r := http.NewServeMux()
	api := &API{logger: logging.MustGetLogger("logserver-test")}
	if c != nil {
		api.SetLogLevelController(c)
	}
	api.registerLogLevelRoutes(func(p string, h http.HandlerFunc) { r.Handle(p, h) })
	return r
}

func serveLogLevel(t *testing.T, r http.Handler, method, target string) (*httptest.ResponseRecorder, LogLevelStatus) {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	var st LogLevelStatus
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &st))
	}
	return rec, st
}

func TestLogLevelRoutes(t *testing.T) {
	c := &fakeLogLevelController{level: logrus.InfoLevel}
	r := newLogLevelTestAPI(c)

	rec, st := serveLogLevel(t, r, http.MethodGet, "/debug/loglevel")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "info", st.Level)

	rec, st = serveLogLevel(t, r, http.MethodPost, "/debug/loglevel?level=debug&ttl=10m")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "debug", st.Level)
	require.Equal(t, 10*time.Minute, c.ttl)

	// No ttl means the default.
	_, _ = serveLogLevel(t, r, http.MethodPost, "/debug/loglevel?level=trace")
	require.Equal(t, DefaultTempLogLevelTTL, c.ttl)

	rec, _ = serveLogLevel(t, r, http.MethodDelete, "/debug/loglevel")
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, c.cleared)
}

func TestLogLevelRoutesRejectBadInput(t *testing.T) {
	c := &fakeLogLevelController{level: logrus.InfoLevel}
	r := newLogLevelTestAPI(c)
	for _, target := range []string{
		"/debug/loglevel",                     // no level
		"/debug/loglevel?level=verbose",       // unknown level
		"/debug/loglevel?level=debug&ttl=10",  // ttl without a unit
		"/debug/loglevel?level=debug&ttl=2h",  // refused by the controller
		"/debug/loglevel?level=debug&ttl=abc", // not a duration
	} {
		rec, _ := serveLogLevel(t, r, http.MethodPost, target)
		require.Equalf(t, http.StatusBadRequest, rec.Code, "POST %s", target)
	}
	require.Equal(t, logrus.InfoLevel, c.level)
}

func TestLogLevelRoutesWithoutController(t *testing.T) {
	r := newLogLevelTestAPI(nil)
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		rec, _ := serveLogLevel(t, r, method, "/debug/loglevel?level=debug")
		require.Equalf(t, http.StatusServiceUnavailable, rec.Code, method)
	}
}
