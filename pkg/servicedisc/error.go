// Package servicedisc pkg/servicedisc/error.go
package servicedisc

import (
	"fmt"
	"net/http"

	"github.com/sirupsen/logrus"
)

// HTTPError represents an HTTP error.
type HTTPError struct {
	HTTPStatus int    `json:"code,omitempty"` // HTTP Status.
	Err        string `json:"error,omitempty"`
}

// Error implements error.
func (err *HTTPError) Error() string {
	return fmt.Sprintf("%s: %s", http.StatusText(err.HTTPStatus), err.Err)
}

// Log prints a log message for the HTTP error.
func (err *HTTPError) Log(log logrus.FieldLogger) {
	log.WithError(err).
		WithField("msg", err.Err).
		WithField("http_status", http.StatusText(err.HTTPStatus)).
		Warn()
}

// Timeout implements net.Error.
func (err *HTTPError) Timeout() bool {
	switch err.HTTPStatus {
	case http.StatusGatewayTimeout, http.StatusRequestTimeout:
		return true
	default:
		return false
	}
}

// Temporary implements net.Error.
func (err *HTTPError) Temporary() bool {
	if err.Timeout() {
		return true
	}
	switch err.HTTPStatus {
	case http.StatusServiceUnavailable, http.StatusTooManyRequests:
		return true
	default:
		return false
	}
}
