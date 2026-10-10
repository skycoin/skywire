// Package httputil pkg/httputil/recover.go
package httputil

import (
	"errors"
	"net/http"
	"runtime/debug"
)

// Recoverer turns a handler panic into a 500 and logs it with the stack, so one
// bad request does not take the server down. http.ErrAbortHandler passes through.
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(rec)
			}
			log.Errorf("panic serving %s %s: %v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
			w.WriteHeader(http.StatusInternalServerError)
		}()
		next.ServeHTTP(w, r)
	})
}
