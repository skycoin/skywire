// Package httputil pkg/httputil/get_logger_test.go c0-com-http
package httputil

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
)

// A context miss is the common case (SetLoggerMiddleware only populates the
// context when a request ID is present), so it must not build a logger. It
// used to return a fresh logging.NewMasterLogger() per call.
func TestGetLoggerFallbackIsShared(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/p", nil)

	first := GetLogger(r)
	second := GetLogger(r)

	require.NotNil(t, first)
	require.Same(t, first, second, "a context miss must reuse one logger, not construct one per call")

	allocs := testing.AllocsPerRun(100, func() { _ = GetLogger(r) })
	require.Zero(t, allocs, "a context miss must not allocate")
}

// A logger in the context still wins.
func TestGetLoggerPrefersContext(t *testing.T) {
	ctxLog := logrus.New().WithField("RequestID", "abc")
	r := httptest.NewRequest(http.MethodGet, "/p", nil).
		WithContext(context.WithValue(context.Background(), LoggerKey, logrus.FieldLogger(ctxLog)))

	require.Same(t, ctxLog, GetLogger(r))
}

// The fallback routes through the master logger, so the process's hooks and
// configured level apply to it. The per-call master logger it replaced was its
// own root and its entries escaped both.
func TestGetLoggerFallbackObeysMasterLogger(t *testing.T) {
	logging.SetOutputTo(io.Discard)
	defer logging.SetOutputTo(os.Stderr)

	oldLevel := logging.GetLevel()
	defer logging.SetLevel(oldLevel)

	hook := &collectHook{}
	logging.AddHook(hook)

	const msg = "httputil-fallback-probe"
	count := func() int {
		n := 0
		for _, e := range hook.entries {
			if e.Message == msg {
				n++
			}
		}
		return n
	}

	r := httptest.NewRequest(http.MethodGet, "/p", nil)

	logging.SetLevel(logrus.InfoLevel)
	GetLogger(r).Debug(msg)
	require.Zero(t, count(), "the configured level must apply to the fallback logger")

	logging.SetLevel(logrus.DebugLevel)
	GetLogger(r).Debug(msg)
	require.Equal(t, 1, count(), "the process hooks must see the fallback logger's entries")
}
