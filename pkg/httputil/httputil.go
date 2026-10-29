// Package httputil pkg/httputil/httputil.go c0-com-http
package httputil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/logging"
)

var log = logging.MustGetLogger("httputil")

// WriteJSON writes a json object on a http.ResponseWriter with the given code.
// A value that cannot be encoded answers 500. Write errors are only logged.
func WriteJSON(w http.ResponseWriter, r *http.Request, code int, v interface{}) {
	pretty, err := BoolFromQuery(r, "pretty", false)
	if err != nil {
		log.WithError(err).Warn("Failed to get bool from query")
	}
	if err, ok := v.(error); ok {
		v = map[string]interface{}{"error": err.Error()}
	}
	// Marshal before writing the header, so a value that cannot be encoded
	// answers 500 instead of a status with an empty or truncated body.
	var body []byte
	if pretty {
		body, err = json.MarshalIndent(v, "", "  ")
	} else {
		body, err = json.Marshal(v)
	}
	if err != nil {
		log.WithError(err).Error("Failed to encode JSON response")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if _, err := w.Write(append(body, '\n')); err != nil {
		if isIOError(err) {
			log.WithError(err).Debug("Failed to write JSON response (client likely disconnected)")
			return
		}
		log.WithError(err).Warn("Failed to write JSON response")
	}
}

// isIOError checks if an error is related to I/O (network issues, timeouts, etc.)
// — anything that means "the response can't be written", not "the data is wrong".
func isIOError(err error) bool {
	if err == nil {
		return false
	}
	// Sentinel errors with stable identities. Covers:
	//   - net.ErrClosed:           "use of closed network connection"
	//   - io.ErrClosedPipe:        "io: read/write on closed pipe"
	//   - io.ErrShortWrite:        "short write" — wrapped by http.timeoutWriter
	//                              when a write deadline expires mid-response
	//   - os.ErrDeadlineExceeded:  any deadline-exceeded error (write timeouts,
	//                              context deadline propagated through)
	if errors.Is(err, net.ErrClosed) ||
		errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, io.ErrShortWrite) ||
		errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	// net.Error timeout — covers any net package timeout type that doesn't
	// satisfy errors.Is(_, os.ErrDeadlineExceeded).
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	// String-match fallback for errors that have no clean Go sentinel,
	// or whose value differs from a sentinel that shares its message.
	//
	// "short write" specifically: http.timeoutWriter (from net/http's
	// per-write deadline machinery) returns its own error when the
	// response deadline expires mid-write — same text as io.ErrShortWrite,
	// different identity, so errors.Is misses it. Production TPD was
	// panicking every ~30s on large /all-transports responses to slow
	// clients before this case was added.
	errStr := err.Error()
	if strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "connection reset") ||
		strings.Contains(errStr, "short write") ||
		strings.Contains(errStr, "i/o timeout") ||
		strings.Contains(errStr, "deadline exceeded") {
		return true
	}
	return false
}

// ReadJSON reads the request body to a json object.
func ReadJSON(r *http.Request, v interface{}) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// BoolFromQuery obtains a boolean from a query entry.
func BoolFromQuery(r *http.Request, key string, defaultVal bool) (bool, error) {
	if r == nil || r.URL == nil {
		return defaultVal, nil
	}
	switch q := r.URL.Query().Get(key); q {
	case "true", "on", "1":
		return true, nil
	case "false", "off", "0":
		return false, nil
	case "":
		return defaultVal, nil
	default:
		return false, fmt.Errorf("invalid '%s' query value of '%s'", key, q)
	}
}

type ctxKeyLogger int

// LoggerKey defines logger HTTP context key.
const LoggerKey ctxKeyLogger = -1

// GetLogger returns the logger carried by the HTTP request context, or the
// package logger when the context has none.
//
// The miss is the common case, not an edge case: SetLoggerMiddleware only
// puts a logger in the context when a request ID is present. This used to
// return logging.NewMasterLogger(), which built a whole logrus.Logger,
// TextFormatter and hook map per call, and whose entries then bypassed the
// process hooks and configured level because it was its own master. The
// package logger is created once and routes through the master logger, so
// hooks and level apply.
func GetLogger(r *http.Request) logrus.FieldLogger {
	if ctxLog, ok := r.Context().Value(LoggerKey).(logrus.FieldLogger); ok && ctxLog != nil {
		return ctxLog
	}

	return log
}

// todo: investigate if it's used throughout the services (didn't work properly for UT)
// remove and use structured logging

// SetLoggerMiddleware sets logger to context of HTTP requests.
func SetLoggerMiddleware(log logrus.FieldLogger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			if reqID := GetReqID(ctx); reqID != "" && log != nil {
				ctx = context.WithValue(ctx, LoggerKey, log.WithField("RequestID", reqID))
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		}
		return http.HandlerFunc(fn)
	}
}
