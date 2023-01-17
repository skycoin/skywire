// Package httputil pkg/httputil/error.go
package httputil

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
)

// HTTPError represents an http error associated with a server response.
type HTTPError struct {
	Status int
	Body   string
}

// Error is the object returned to the client when there's an error.
type Error struct {
	Error string `json:"error"`
}

// ErrorFromResp creates an HTTPError from a given server response.
func ErrorFromResp(resp *http.Response) error {
	status := resp.StatusCode
	if status >= 200 && status < 300 {
		return nil
	}
	msg, err := io.ReadAll(resp.Body)
	if err != nil && len(msg) == 0 {
		msg = []byte(fmt.Sprintf("failed to read HTTP response body: %v", err))
	}
	return &HTTPError{Status: status, Body: string(bytes.TrimSpace(msg))}
}

// Error returns the error message.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("%d %s: %v", e.Status, http.StatusText(e.Status), e.Body)
}

// Timeout implements net.Error
func (e *HTTPError) Timeout() bool {
	switch e.Status {
	case http.StatusGatewayTimeout, http.StatusRequestTimeout:
		return true
	default:
		return false
	}
}

// Temporary implements net.Error
func (e *HTTPError) Temporary() bool {
	if e.Timeout() {
		return true
	}
	switch e.Status {
	case http.StatusServiceUnavailable, http.StatusTooManyRequests:
		return true
	default:
		return false
	}
}
