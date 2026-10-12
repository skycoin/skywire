//go:build tinygo && js

// Package dmsghttp pkg/dmsg/dmsghttp/debug_pprof_tinygo.go c1-net-dmsg
package dmsghttp

import "net/http"

// registerPprof mounts nothing in a TinyGo browser build, which has nothing
// to profile.
func registerPprof(*http.ServeMux) {}
