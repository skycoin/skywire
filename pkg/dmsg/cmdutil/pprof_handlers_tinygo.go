//go:build tinygo && js

// Package cmdutil pkg/dmsg/cmdutil/pprof_handlers_tinygo.go c1-net-dmsg
package cmdutil

import "net/http"

// registerPprofHandlers mounts nothing in a TinyGo browser build, which has
// nothing to profile.
func registerPprofHandlers(*http.ServeMux, bool) {}
