// Package httputil pkg/httputil/limit.go c0-com-util
package httputil

import (
	"errors"
	"net/http"
)

// LimitBody caps request bodies at n bytes. A declared Content-Length over n
// is answered 413 at once, and any other body fails its read past n.
func LimitBody(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > n {
				WriteJSON(w, r, http.StatusRequestEntityTooLarge, Error{Error: http.StatusText(http.StatusRequestEntityTooLarge)})
				return
			}
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, n)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// BodyTooLarge reports whether err came from a body read that passed the
// LimitBody cap.
func BodyTooLarge(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}
