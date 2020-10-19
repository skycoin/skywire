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
	HTTPStatus int    `json:"code"`    // HTTP Status.
	Msg        string `json:"message"` // Message describing error intended for client.

	// Actual error. This is hidden as it may be purposely obscured by the server.
	Err error `json:"-"`
}

// Error implements error.
func (err *HTTPError) Error() string {
	return fmt.Sprintf("%s: %s", http.StatusText(err.HTTPStatus), err.Msg)
}

// Log prints a log message for the HTTP error.
func (err *HTTPError) Log(log logrus.FieldLogger) {
	log.WithError(err.Err).
		WithField("msg", err.Msg).
		WithField("http_status", http.StatusText(err.HTTPStatus)).
		Warn()
}
