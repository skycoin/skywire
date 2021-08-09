package servicedisc

import (
	"fmt"
	"net/http"

	"github.com/sirupsen/logrus"
)

// HTTPResponse represents the http response struct.
type HTTPResponse struct {
	Error *HTTPError  `json:"error,omitempty"`
	Data  interface{} `json:"data,omitempty"`
}

// HTTPError represents an HTTP error.
type HTTPError struct {
	HTTPStatus int   `json:"code"` // HTTP Status.
	Err        error `json:"error"`
}

// Error implements error.
func (err *HTTPError) Error() string {
	return fmt.Sprintf("%s: %s", http.StatusText(err.HTTPStatus), err.Err)
}

// Log prints a log message for the HTTP error.
func (err *HTTPError) Log(log logrus.FieldLogger) {
	log.WithError(err.Err).
		WithField("msg", err.Err).
		WithField("http_status", http.StatusText(err.HTTPStatus)).
		Warn()
}
