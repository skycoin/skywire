// Package metricsutil pkg/metricsutil/victoria_metrics_int_gauge_wrapper.go c0-com-util
package metricsutil

import (
	"sync/atomic"

	"github.com/0magnet/metrics"
)

// VictoriaMetricsIntGaugeWrapper wraps Victoria Metrics int gauge encapsulating all the
// needed logic to control the value.
type VictoriaMetricsIntGaugeWrapper struct {
	val   atomic.Int64
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
	w.val.Add(1)
}

// Dec decrements gauge value.
func (w *VictoriaMetricsIntGaugeWrapper) Dec() {
	w.val.Add(-1)
}

// Set sets gauge value.
func (w *VictoriaMetricsIntGaugeWrapper) Set(val int64) {
	w.val.Store(val)
}

// Val gets gauge value.
func (w *VictoriaMetricsIntGaugeWrapper) Val() int64 {
	return w.val.Load()
}
