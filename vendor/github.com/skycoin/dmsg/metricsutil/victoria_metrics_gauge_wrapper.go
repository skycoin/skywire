package metricsutil

import (
	"sync/atomic"

	"github.com/VictoriaMetrics/metrics"
)

// VictoriaMetricsGaugeWrapper wraps Victoria Metrics gauge encapsulating all the
// needed logic to control the value.
type VictoriaMetricsGaugeWrapper struct {
	val   int64
	gauge *metrics.Gauge
}

// NewVictoriaMetricsGauge constructs new wrapper for Victoria Metric gauge with
// the name `name`.
func NewVictoriaMetricsGauge(name string) *VictoriaMetricsGaugeWrapper {
	var w VictoriaMetricsGaugeWrapper
	w.gauge = metrics.GetOrCreateGauge(name, func() float64 {
		return float64(w.Val())
	})

	return &w
}

// Inc increments gauge value.
func (w *VictoriaMetricsGaugeWrapper) Inc() {
	atomic.AddInt64(&w.val, 1)
}

// Dec decrements gauge value.
func (w *VictoriaMetricsGaugeWrapper) Dec() {
	atomic.AddInt64(&w.val, -1)
}

// Val gets gauge value.
func (w *VictoriaMetricsGaugeWrapper) Val() int64 {
	return atomic.LoadInt64(&w.val)
}
