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
