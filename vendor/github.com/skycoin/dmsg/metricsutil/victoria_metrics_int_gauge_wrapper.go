package metricsutil

import (
	"sync/atomic"

	"github.com/VictoriaMetrics/metrics"
)

// VictoriaMetricsIntGaugeWrapper wraps Victoria Metrics int gauge encapsulating all the
// needed logic to control the value.
type VictoriaMetricsIntGaugeWrapper struct {
	val   int64
	gauge *metrics.Gauge
}

// NewVictoriaMetricsIntGauge constructs new wrapper for Victoria Metric int gauge with
// the name `name`.
func NewVictoriaMetricsIntGauge(name string) *VictoriaMetricsIntGaugeWrapper {
	var w VictoriaMetricsIntGaugeWrapper
	w.gauge = metrics.GetOrCreateGauge(name, func() float64 {
		return float64(w.Val())
	})

	return &w
}

// Inc increments gauge value.
func (w *VictoriaMetricsIntGaugeWrapper) Inc() {
	atomic.AddInt64(&w.val, 1)
}

// Dec decrements gauge value.
func (w *VictoriaMetricsIntGaugeWrapper) Dec() {
	atomic.AddInt64(&w.val, -1)
}

// Set sets gauge value.
func (w *VictoriaMetricsIntGaugeWrapper) Set(val int64) {
	atomic.StoreInt64(&w.val, val)
}

// Val gets gauge value.
func (w *VictoriaMetricsIntGaugeWrapper) Val() int64 {
	return atomic.LoadInt64(&w.val)
}
