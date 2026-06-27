// Package httputil pkg/httputil/httputil_more_test.go: unit tests for the HTTP
// helpers — error wrapping, health fetch, JSON read/write, query parsing,
// address split, and the logger middlewares.
package httputil

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func discardLog() logrus.FieldLogger {
	l := logrus.New()
	l.SetOutput(io.Discard)
	return l
}

// --- error.go ---

func TestErrorFromResp(t *testing.T) {
	t.Run("success is nil", func(t *testing.T) {
		resp := &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}
		assert.NoError(t, ErrorFromResp(resp))
	})

	t.Run("error carries status and body", func(t *testing.T) {
		resp := &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader("  boom  "))}
		err := ErrorFromResp(resp)
		require.Error(t, err)
		he, ok := err.(*HTTPError)
		require.True(t, ok)
		assert.Equal(t, 500, he.Status)
		assert.Equal(t, "boom", he.Body) // trimmed
	})
}

func TestHTTPErrorString(t *testing.T) {
	e := &HTTPError{Status: http.StatusNotFound, Body: "nope"}
	assert.Equal(t, "404 Not Found: nope", e.Error())
}

func TestHTTPErrorTimeoutTemporary(t *testing.T) {
	assert.True(t, (&HTTPError{Status: http.StatusGatewayTimeout}).Timeout())
	assert.True(t, (&HTTPError{Status: http.StatusRequestTimeout}).Timeout())
	assert.False(t, (&HTTPError{Status: http.StatusInternalServerError}).Timeout())

	assert.True(t, (&HTTPError{Status: http.StatusGatewayTimeout}).Temporary()) // via Timeout
	assert.True(t, (&HTTPError{Status: http.StatusServiceUnavailable}).Temporary())
	assert.True(t, (&HTTPError{Status: http.StatusTooManyRequests}).Temporary())
	assert.False(t, (&HTTPError{Status: http.StatusBadRequest}).Temporary())
}

// --- health.go ---

func TestGetServiceHealth(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/health", r.URL.Path)
			_ = json.NewEncoder(w).Encode(HealthCheckResponse{ServiceName: "svc", PublicKey: "pk"})
		}))
		defer srv.Close()

		h, err := GetServiceHealth(context.Background(), srv.URL)
		require.NoError(t, err)
		require.NotNil(t, h)
		assert.Equal(t, "svc", h.ServiceName)
		assert.Equal(t, "pk", h.PublicKey)
	})

	t.Run("non-200 returns HTTPError", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(HTTPError{Status: 503, Body: "down"})
		}))
		defer srv.Close()

		_, err := GetServiceHealth(context.Background(), srv.URL)
		require.Error(t, err)
		he, ok := err.(*HTTPError)
		require.True(t, ok)
		assert.Equal(t, 503, he.Status)
	})

	t.Run("unreachable", func(t *testing.T) {
		_, err := GetServiceHealth(context.Background(), "http://127.0.0.1:1")
		assert.Error(t, err)
	})
}

// --- httputil.go ---

func TestWriteJSON(t *testing.T) {
	t.Run("object", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		WriteJSON(w, r, http.StatusCreated, map[string]int{"n": 1})
		assert.Equal(t, http.StatusCreated, w.Code)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
		assert.JSONEq(t, `{"n":1}`, w.Body.String())
	})

	t.Run("pretty", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/?pretty=1", nil)
		WriteJSON(w, r, http.StatusOK, map[string]int{"n": 1})
		assert.Contains(t, w.Body.String(), "\n  ") // indented
	})

	t.Run("error value", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		WriteJSON(w, r, http.StatusBadRequest, errors.New("bad"))
		assert.JSONEq(t, `{"error":"bad"}`, w.Body.String())
	})
}

func TestReadJSON(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"n":5}`))
		var v struct {
			N int `json:"n"`
		}
		require.NoError(t, ReadJSON(r, &v))
		assert.Equal(t, 5, v.N)
	})

	t.Run("unknown field rejected", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"unknown":1}`))
		var v struct {
			N int `json:"n"`
		}
		assert.Error(t, ReadJSON(r, &v))
	})
}

func TestBoolFromQuery(t *testing.T) {
	req := func(q string) *http.Request { return httptest.NewRequest(http.MethodGet, "/?flag="+q, nil) }

	for _, truthy := range []string{"true", "on", "1"} {
		got, err := BoolFromQuery(req(truthy), "flag", false)
		require.NoError(t, err)
		assert.True(t, got)
	}
	for _, falsy := range []string{"false", "off", "0"} {
		got, err := BoolFromQuery(req(falsy), "flag", true)
		require.NoError(t, err)
		assert.False(t, got)
	}

	// missing → default
	got, err := BoolFromQuery(httptest.NewRequest(http.MethodGet, "/", nil), "flag", true)
	require.NoError(t, err)
	assert.True(t, got)

	// invalid → error
	_, err = BoolFromQuery(req("maybe"), "flag", false)
	assert.Error(t, err)
}

func TestSplitRPCAddr(t *testing.T) {
	host, port, err := SplitRPCAddr("localhost:8080")
	require.NoError(t, err)
	assert.Equal(t, "localhost", host)
	assert.Equal(t, uint16(8080), port)

	_, _, err = SplitRPCAddr("localhost:notaport")
	assert.Error(t, err)
}

func TestGetLogger(t *testing.T) {
	t.Run("absent returns a logger", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		assert.NotNil(t, GetLogger(r))
	})

	t.Run("present returns the context logger", func(t *testing.T) {
		want := discardLog()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r = r.WithContext(context.WithValue(r.Context(), LoggerKey, want))
		assert.Equal(t, want, GetLogger(r))
	})
}

func TestSetLoggerMiddleware(t *testing.T) {
	var seen logrus.FieldLogger
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = GetLogger(r) })

	t.Run("with request id sets context logger", func(t *testing.T) {
		h := SetLoggerMiddleware(discardLog())(next)
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r = r.WithContext(context.WithValue(r.Context(), middleware.RequestIDKey, "req-1"))
		h.ServeHTTP(httptest.NewRecorder(), r)
		assert.NotNil(t, seen)
	})

	t.Run("without request id falls through", func(t *testing.T) {
		h := SetLoggerMiddleware(discardLog())(next)
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		h.ServeHTTP(httptest.NewRecorder(), r)
		assert.NotNil(t, seen) // GetLogger still returns a fallback logger
	})
}

// --- log.go ---

func TestLogMiddleware(t *testing.T) {
	mw := NewLogMiddleware(discardLog())
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		LogEntrySetField(r, "user", "alice") // exercised under the middleware
		w.WriteHeader(http.StatusTeapot)
	})

	t.Run("with request id", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/path", nil)
		r = r.WithContext(context.WithValue(r.Context(), middleware.RequestIDKey, "rid"))
		w := httptest.NewRecorder()
		mw(next).ServeHTTP(w, r)
		assert.Equal(t, http.StatusTeapot, w.Code)
	})

	t.Run("without request id", func(t *testing.T) {
		w := httptest.NewRecorder()
		mw(next).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/path", nil))
		assert.Equal(t, http.StatusTeapot, w.Code)
	})
}

// TestLogEntrySetFieldNoMiddleware verifies the helper is a no-op when the log
// middleware is not installed (no structuredLogger in context).
func TestLogEntrySetFieldNoMiddleware(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	assert.NotPanics(t, func() { LogEntrySetField(r, "k", "v") })
}
