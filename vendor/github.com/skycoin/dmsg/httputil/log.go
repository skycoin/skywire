package httputil

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/middleware"
	"github.com/sirupsen/logrus"
)

type structuredLogger struct {
	logger logrus.FieldLogger
}

// NewLogMiddleware creates a new instance of logging middleware. This will allow
// adding log fields in the handler and any further middleware. At the end of request, this
// log entry will be printed at Info level via passed logger
func NewLogMiddleware(logger logrus.FieldLogger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			sl := &structuredLogger{logger}
			start := time.Now()
			var requestID string
			if reqID := r.Context().Value(middleware.RequestIDKey); reqID != nil {
				requestID = reqID.(string)
			}
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			newContext := context.WithValue(r.Context(), middleware.LogEntryCtxKey, sl)
			next.ServeHTTP(ww, r.WithContext(newContext))
			latency := time.Since(start)
			fields := logrus.Fields{
				"status":  ww.Status(),
				"took":    latency,
				"remote":  r.RemoteAddr,
				"request": r.RequestURI,
				"method":  r.Method,
			}
			if requestID != "" {
				fields["request_id"] = requestID
			}
			sl.logger.WithFields(fields).Info()

		}
		return http.HandlerFunc(fn)
	}
}

// LogEntrySetField adds new key-value pair to current (request scoped) log entry. This pair will be
// printed along with all other pairs when the request is served.
// This requires log middleware from this package to be installed in the chain
func LogEntrySetField(r *http.Request, key string, value interface{}) {
	if sl, ok := r.Context().Value(middleware.LogEntryCtxKey).(*structuredLogger); ok {
		sl.logger = sl.logger.WithField(key, value)
	}
}
