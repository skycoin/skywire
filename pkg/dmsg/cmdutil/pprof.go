// Package cmdutil provides shared command utilities for dmsg services.
package cmdutil

import (
	"encoding/json"
	"net/http"
	"net/http/pprof"
	"os"
	"runtime"
	rpprof "runtime/pprof"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/noise"
	"github.com/skycoin/skywire/pkg/logging"
)

// cacheStatsHandler exposes the in-process cipher signature-verify
// cache and noise DH cache counters as JSON. Mounted at
// /debug/cache by startPProfHTTP. Sits beside the standard
// /debug/pprof/* endpoints so operators can pull hit/miss/eviction
// rates from the same pprof server without a separate listener.
//
// Output shape (hit_rate is misses>0 ? hits/(hits+misses) : 0):
//
//	{
//	  "verify_cache": {
//	    "hits": uint64, "misses": uint64, "evictions": uint64,
//	    "size": int, "capacity": int, "hit_rate": float64
//	  },
//	  "dh_cache": {
//	    "hits": uint64, "misses": uint64, "evictions": uint64,
//	    "size": int, "capacity": int, "hit_rate": float64
//	  }
//	}
func cacheStatsHandler(w http.ResponseWriter, _ *http.Request) {
	vs := cipher.GetVerifyCacheStats()
	ds := noise.GetCacheStats()
	out := map[string]any{
		"verify_cache": map[string]any{
			"hits":      vs.Hits,
			"misses":    vs.Misses,
			"evictions": vs.Evictions,
			"size":      vs.Size,
			"capacity":  vs.Capacity,
			"hit_rate":  hitRate(vs.Hits, vs.Misses),
		},
		"dh_cache": map[string]any{
			"hits":      ds.Hits,
			"misses":    ds.Misses,
			"evictions": ds.Evictions,
			"size":      ds.Size,
			"capacity":  ds.Capacity,
			"hit_rate":  hitRate(ds.Hits, ds.Misses),
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out) //nolint:errcheck
}

func hitRate(hits, misses uint64) float64 {
	total := hits + misses
	if total == 0 {
		return 0
	}
	return float64(hits) / float64(total)
}

// InitPProf starts profiling based on the given mode and address.
// Supported modes: http, cpu, mem, mutex, block, trace.
// If mode is empty, this is a no-op.
// Returns a stop function that should be called on shutdown to finalize profiles.
func InitPProf(log *logging.Logger, mode string, addr string) func() { //nolint:gocyclo
	noop := func() {}

	switch mode {
	case "http":
		startPProfHTTP(log, addr, false)
		time.Sleep(100 * time.Millisecond)
		return noop

	case "cpu":
		f, err := os.Create("cpu.pprof")
		if err != nil {
			log.Errorf("failed to create cpu.pprof: %v", err)
			return noop
		}
		if err := rpprof.StartCPUProfile(f); err != nil {
			log.Errorf("failed to start CPU profile: %v", err)
			if err := f.Close(); err != nil {
				log.Errorf("failed to close cpu.pprof: %v", err)
			}
			return noop
		}
		log.Info("CPU profiling started, will write to cpu.pprof on shutdown")
		return func() {
			rpprof.StopCPUProfile()
			if err := f.Close(); err != nil {
				log.Errorf("failed to close cpu.pprof: %v", err)
			}
			log.Info("CPU profile written to cpu.pprof")
		}

	case "mem":
		log.Info("Memory profiling enabled, will write to mem.pprof on shutdown")
		return func() {
			f, err := os.Create("mem.pprof")
			if err != nil {
				log.Errorf("failed to create mem.pprof: %v", err)
				return
			}
			defer f.Close() //nolint:errcheck,gosec
			runtime.GC()
			if err := rpprof.WriteHeapProfile(f); err != nil {
				log.Errorf("failed to write memory profile: %v", err)
				return
			}
			log.Info("Memory profile written to mem.pprof")
		}

	case "mutex":
		runtime.SetMutexProfileFraction(1)
		log.Info("Mutex profiling enabled, will write to mutex.pprof on shutdown")
		return func() {
			f, err := os.Create("mutex.pprof")
			if err != nil {
				log.Errorf("failed to create mutex.pprof: %v", err)
				return
			}
			defer f.Close() //nolint:errcheck,gosec
			if err := rpprof.Lookup("mutex").WriteTo(f, 0); err != nil {
				log.Errorf("failed to write mutex profile: %v", err)
				return
			}
			log.Info("Mutex profile written to mutex.pprof")
		}

	case "block":
		runtime.SetBlockProfileRate(1)
		log.Info("Block profiling enabled, will write to block.pprof on shutdown")
		return func() {
			f, err := os.Create("block.pprof")
			if err != nil {
				log.Errorf("failed to create block.pprof: %v", err)
				return
			}
			defer f.Close() //nolint:errcheck,gosec
			if err := rpprof.Lookup("block").WriteTo(f, 0); err != nil {
				log.Errorf("failed to write block profile: %v", err)
				return
			}
			log.Info("Block profile written to block.pprof")
		}

	case "trace":
		startPProfHTTP(log, addr, true)
		time.Sleep(100 * time.Millisecond)
		return noop

	case "":
		// no-op

	default:
		log.Errorf("unknown pprof mode %q, supported: [ cpu | mem | mutex | block | trace | http ]", mode)
	}

	return noop
}

// startPProfHTTP starts a pprof HTTP server on a dedicated OS thread.
// Locking the goroutine to its own thread ensures the kernel scheduler
// gives it CPU time even when the Go runtime is saturated with goroutines,
// which is exactly when pprof is needed most.
func startPProfHTTP(log *logging.Logger, addr string, traceOnly bool) {
	// Reserve an extra OS thread for the pprof server so it doesn't
	// compete with application goroutines for GOMAXPROCS slots.
	runtime.GOMAXPROCS(runtime.GOMAXPROCS(0) + 1)

	go func() {
		// Pin this goroutine to a dedicated OS thread so the kernel
		// scheduler guarantees it CPU time independent of Go's
		// cooperative goroutine scheduler.
		runtime.LockOSThread()

		mux := http.NewServeMux()
		if traceOnly {
			mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
			log.Infof("Serving trace endpoint on http://%s/debug/pprof/trace (dedicated thread)", addr)
		} else {
			mux.HandleFunc("/debug/pprof/", pprof.Index)
			mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
			mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
			mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
			mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

			for _, profile := range []string{"heap", "goroutine", "threadcreate", "block", "mutex", "allocs"} {
				mux.Handle("/debug/pprof/"+profile, pprof.Handler(profile))
			}
			mux.HandleFunc("/debug/cache", cacheStatsHandler)
			log.Infof("Serving pprof on http://%s (dedicated thread); cipher+DH cache stats at /debug/cache", addr)
		}

		srv := &http.Server{
			Addr:              addr,
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
			WriteTimeout:      30 * time.Second,
		}
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Errorf("pprof http server failed: %v", err)
		}
	}()
}
