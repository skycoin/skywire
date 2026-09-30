//go:build tinygo

// Package dmsghttp pkg/dmsg/dmsghttp/debug_pprof_tinygo.go c1-net-dmsg
package dmsghttp

import "net/http"

// registerPprof mounts nothing in a TinyGo build: its net/http/pprof does not
// build against current Go, and a TinyGo wasm page has nothing to profile.
func registerPprof(*http.ServeMux) {}
