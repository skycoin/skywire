//go:build !(tinygo && js)

// Package dmsghttp pkg/dmsg/dmsghttp/debug_pprof.go c1-net-dmsg
package dmsghttp

import (
	"net/http"
	"net/http/pprof"
)

// registerPprof mounts the standard pprof endpoints on mux.
func registerPprof(mux *http.ServeMux) {
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	for _, p := range []string{"heap", "goroutine", "goroutineleak", "threadcreate", "block", "mutex", "allocs"} {
		mux.Handle("/debug/pprof/"+p, pprof.Handler(p))
	}
}
