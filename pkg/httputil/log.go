// Package httputil pkg/httputil/log.go c0-com-http
package httputil

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"
)

type structuredLogger struct {
	logger logrus.FieldLogger
}

// RequestLogSlow is the latency above which a request that otherwise
// succeeded is still logged at Info. A var, not a const, so tests can lower it.
var RequestLogSlow = 2 * time.Second

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
			entry := sl.logger.WithFields(fields)
			// Level by outcome. A deployment service answers thousands of
			// requests a minute; logging each one at Info made the request log
			// the bulk of the service's output and told an operator nothing a
			// counter would not. What is worth a line is a request that failed
			// on our side, or one that took long enough to be a symptom.
			switch {
			case ww.Status() >= http.StatusInternalServerError:
				entry.Warn("Served request.")
			case latency >= RequestLogSlow:
				entry.Info("Served slow request.")
			default:
				// 4xx included: a malformed or out-of-sequence request is the
				// client's mistake, and a noisy client must not be able to fill
				// the service's log at Info.
				entry.Debug("Served request.")
			}
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
