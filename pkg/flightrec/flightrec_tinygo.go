//go:build tinygo

// Package flightrec pkg/flightrec/flightrec_tinygo.go c1-util-debug
//
// TinyGo has no runtime/trace flight recorder, so every entry point here is a
// no-op and the handler answers 404 the way the native one does when stopped.
package flightrec

import "net/http"

// Start is a no-op under TinyGo.
func Start(_ string, _ func(string, ...any)) error { return nil }

// Stop is a no-op under TinyGo.
func Stop() {}

// Running is always false under TinyGo.
func Running() bool { return false }

// Snapshot is a no-op under TinyGo.
func Snapshot(_ string) {}

// Snapshots is always empty under TinyGo.
func Snapshots() []string { return nil }

// Handler answers 404, as the native handler does while not running.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "the flight recorder is not running", http.StatusNotFound)
	})
}
