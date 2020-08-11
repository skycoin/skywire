package promutil

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// HTTPMetrics collects metrics for prometheus.
type HTTPMetrics interface {
	Collectors() []prometheus.Collector
	Handle(next http.Handler) http.HandlerFunc
}

// NewHTTPMetrics returns the default implementation of HTTPMetrics.
func NewHTTPMetrics(namespace string) HTTPMetrics {
	reqCount := prometheus.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "request_ongoing_count",
		Help:      "Current number of ongoing requests.",
	})
	reqDurations := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace,
		Name:      "request_duration",
		Help:      "Histogram of request durations.",
	}, []string{"code", "method"})

	return &httpMetrics{
		inFlight:  reqCount,
		durations: reqDurations,
	}
}

type httpMetrics struct {
	inFlight  prometheus.Gauge
	durations prometheus.ObserverVec
}

func (hm *httpMetrics) Collectors() []prometheus.Collector {
	return []prometheus.Collector{
		hm.inFlight,
		hm.durations,
	}
}

func (hm *httpMetrics) Handle(next http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := promhttp.InstrumentHandlerInFlight(hm.inFlight, next)
		promhttp.InstrumentHandlerDuration(hm.durations, h).ServeHTTP(w, r)
	}
}

// NewEmptyHTTPMetrics implements Metrics, but does nothing.
func NewEmptyHTTPMetrics() HTTPMetrics {
	return emptyHTTPMetrics{}
}

type emptyHTTPMetrics struct{}

func (emptyHTTPMetrics) Collectors() []prometheus.Collector        { return nil }
func (emptyHTTPMetrics) Handle(next http.Handler) http.HandlerFunc { return next.ServeHTTP }
