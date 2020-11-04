package promutil

import (
	"github.com/go-chi/chi"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// AddMetricsHandle adds a prometheus Handle at '/metrics' to the provided serve mux.
func AddMetricsHandle(mux *chi.Mux, cs ...prometheus.Collector) {
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	reg.MustRegister(prometheus.NewGoCollector())
	reg.MustRegister(cs...)

	h := promhttp.InstrumentMetricHandler(reg, promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.Handle("/metrics", h)
}
