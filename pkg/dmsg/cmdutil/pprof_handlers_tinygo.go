//go:build tinygo

// Package cmdutil pkg/dmsg/cmdutil/pprof_handlers_tinygo.go c1-net-dmsg
package cmdutil

import "net/http"

// registerPprofHandlers mounts nothing in a TinyGo build: its net/http/pprof
// does not build against current Go.
func registerPprofHandlers(*http.ServeMux, bool) {}
