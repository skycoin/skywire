// Package metricsutil pkg/metricsutil/http.go
package metricsutil

import (
	"net/http"

	"github.com/VictoriaMetrics/metrics"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/sirupsen/logrus"
)

// AddMetricsHandler adds a prometheus-format Handle at '/metrics' to the provided serve mux.
func AddMetricsHandler(mux *chi.Mux) {
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		metrics.WritePrometheus(w, true)
	})
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
		log.Fatal(http.ListenAndServe(addr, r)) //nolint
	}()
}
