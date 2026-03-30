// Package metricsutil pkg/metricsutil/http.go

package metricsutil

import (
	"fmt"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/VictoriaMetrics/metrics"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"
)

// AddMetricsHandler adds a prometheus-format Handle at '/metrics' to the provided serve mux.
func AddMetricsHandler(mux *chi.Mux) {
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		metrics.WritePrometheus(w, true)
	})
}

// ServePProf starts a pprof debug server on the given address.
// The serviceName is exposed at /debug/service so operators can identify
// which service a pprof endpoint belongs to.
func ServePProf(log logrus.FieldLogger, addr, serviceName string) {
	if addr == "" {
		return
	}

	mux := http.NewServeMux()

	// Service identifier
	mux.HandleFunc("/debug/service", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, serviceName)
	})

	// Standard pprof handlers
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	for _, profile := range []string{"heap", "goroutine", "threadcreate", "block", "mutex", "allocs"} {
		mux.Handle("/debug/pprof/"+profile, pprof.Handler(profile))
	}

	go func() {
		log.WithField("addr", addr).WithField("service", serviceName).Info("Starting pprof server")
		server := &http.Server{
			Addr:              addr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Error("pprof server failed")
		}
	}()
}

// ServeHTTPMetrics starts serving metrics on a given `addr`.
func ServeHTTPMetrics(log logrus.FieldLogger, addr string) {
	if addr == "" {
		return
	}

	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	AddMetricsHandler(r)

	log.WithField("addr", addr).Info("Serving metrics.")
	go func() {
		log.Fatal(http.ListenAndServe(addr, r)) //nolint:gosec
	}()
}
