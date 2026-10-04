//go:build !tinygo

// Package cmdutil pkg/dmsg/cmdutil/pprof_handlers.go c1-net-dmsg
package cmdutil

import (
	"net/http"
	"net/http/pprof"
)

// registerPprofHandlers mounts the pprof endpoints on mux: only the trace
// endpoint when traceOnly, else all of them.
func registerPprofHandlers(mux *http.ServeMux, traceOnly bool) {
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	if traceOnly {
		return
	}
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	for _, profile := range []string{"heap", "goroutine", "goroutineleak", "threadcreate", "block", "mutex", "allocs"} {
		mux.Handle("/debug/pprof/"+profile, pprof.Handler(profile))
	}
}
