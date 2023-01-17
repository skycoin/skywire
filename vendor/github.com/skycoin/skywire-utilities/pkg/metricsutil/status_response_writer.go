// Package metricsutil pkg/metricsutil/status_response_writer.go
package metricsutil

import "net/http"

// StatusResponseWriter wraps `http.ResponseWriter` but stores status code
// on call to `WriteHeader`.
type StatusResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

// NewStatusResponseWriter wraps `http.ResponseWriter` constructing `StatusResponseWriter`.
func NewStatusResponseWriter(w http.ResponseWriter) *StatusResponseWriter {
	return &StatusResponseWriter{
		ResponseWriter: w,
	}
}

// WriteHeader implements `http.ResponseWriter` storing the written status code.
func (w *StatusResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

// StatusCode gets status code from the writer.
func (w *StatusResponseWriter) StatusCode() int {
	if w.statusCode == 0 {
		// this is case when `WriteHeader` wasn't called explicitly,
		// so we consider it 200
		return http.StatusOK
	}

	return w.statusCode
}
