// Package metricsutil pkg/metricsutil/victoria_metrics_uint_gauge_wrapper.go c0-com-util
package metricsutil

import (
	"sync/atomic"

	"github.com/0magnet/metrics"
)

// VictoriaMetricsUintGaugeWrapper wraps Victoria Metrics int gauge encapsulating all the
// needed logic to control the value.
type VictoriaMetricsUintGaugeWrapper struct {
	val   atomic.Uint64
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
	w.val.Add(1)
}

// Dec decrements gauge value.
func (w *VictoriaMetricsUintGaugeWrapper) Dec() {
	w.val.Add(^uint64(0))
}

// Set sets gauge value.
func (w *VictoriaMetricsUintGaugeWrapper) Set(val uint64) {
	w.val.Store(val)
}

// Val gets gauge value.
func (w *VictoriaMetricsUintGaugeWrapper) Val() uint64 {
	return w.val.Load()
}
