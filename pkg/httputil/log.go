// Package httputil pkg/httputil/log.go c0-com-http
package httputil

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"time"

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
			requestID := GetReqID(r.Context())
			ww := &StatusWriter{ResponseWriter: w}
			newContext := context.WithValue(r.Context(), logEntryKey{}, sl)
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
	if sl, ok := r.Context().Value(logEntryKey{}).(*structuredLogger); ok {
		sl.logger = sl.logger.WithField(key, value)
	}
}

type logEntryKey struct{}

// StatusWriter records the status code and body size a handler wrote. It
// passes Flush and Hijack through to the writer it wraps.
type StatusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

// WriteHeader records code and sends it.
func (s *StatusWriter) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

// Write records that a body was sent, with an implicit 200.
func (s *StatusWriter) Write(p []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(p)
	s.bytes += n
	return n, err
}

// Status is the code sent, 200 when the handler wrote nothing.
func (s *StatusWriter) Status() int {
	if s.status == 0 {
		return http.StatusOK
	}
	return s.status
}

// BytesWritten is the body size sent.
func (s *StatusWriter) BytesWritten() int { return s.bytes }

// Flush sends buffered data to the client.
func (s *StatusWriter) Flush() { http.NewResponseController(s.ResponseWriter).Flush() } //nolint:errcheck,gosec

// Hijack lets the handler take over the connection.
func (s *StatusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(s.ResponseWriter).Hijack()
}

// Unwrap returns the wrapped writer, for http.ResponseController.
func (s *StatusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }
