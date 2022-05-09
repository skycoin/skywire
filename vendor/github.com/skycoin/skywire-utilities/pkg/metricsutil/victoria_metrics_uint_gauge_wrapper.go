package metricsutil

import (
	"sync/atomic"

	"github.com/VictoriaMetrics/metrics"
)

// VictoriaMetricsUintGaugeWrapper wraps Victoria Metrics int gauge encapsulating all the
// needed logic to control the value.
type VictoriaMetricsUintGaugeWrapper struct {
	val   uint64
	gauge *metrics.Gauge
}

// NewVictoriaMetricsUintGauge constructs new wrapper for Victoria Metric int gauge with
// the name `name`.
func NewVictoriaMetricsUintGauge(name string) *VictoriaMetricsUintGaugeWrapper {
	var w VictoriaMetricsUintGaugeWrapper
	w.gauge = metrics.GetOrCreateGauge(name, func() float64 {
		return float64(w.Val())
	})

	return &w
}

// Inc increments gauge value.
func (w *VictoriaMetricsUintGaugeWrapper) Inc() {
	atomic.AddUint64(&w.val, 1)
}

// Dec decrements gauge value.
func (w *VictoriaMetricsUintGaugeWrapper) Dec() {
	atomic.AddUint64(&w.val, ^uint64(0))
}

// Set sets gauge value.
func (w *VictoriaMetricsUintGaugeWrapper) Set(val uint64) {
	atomic.StoreUint64(&w.val, val)
}

// Val gets gauge value.
func (w *VictoriaMetricsUintGaugeWrapper) Val() uint64 {
	return atomic.LoadUint64(&w.val)
}
