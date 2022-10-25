// Package setupmetrics pkg/setup/setupmetrics/victoria_metrics.go
package setupmetrics

import (
	"time"

	"github.com/VictoriaMetrics/metrics"

	"github.com/skycoin/skywire-utilities/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/routing"
)

// Metrics collects metrics in prometheus format.
type Metrics interface {
	RecordRequest() func(*routing.EdgeRules, *error)
	RecordRoute() func(*error)
}

// VictoriaMetrics implements `Metrics` using Victoria Metrics.
type VictoriaMetrics struct {
	activeRequests        *metricsutil.VictoriaMetricsIntGaugeWrapper
	reqDurationsFailed    *metrics.Histogram
	reqDurationsSuccesses *metrics.Histogram
	routesSetup           *metricsutil.VictoriaMetricsIntGaugeWrapper
	routesSetupFailed     *metricsutil.VictoriaMetricsIntGaugeWrapper
	routesSetupDuration   *metrics.Histogram
}

// NewVictoriaMetrics returns the Victoria Metrics implementation of Metrics.
func NewVictoriaMetrics() *VictoriaMetrics {
	return &VictoriaMetrics{
		activeRequests:        metricsutil.NewVictoriaMetricsIntGauge("setup_node_active_request_count"),
		reqDurationsFailed:    metrics.GetOrCreateHistogram("setup_node_request_durations{success=\"false\"}"),
		reqDurationsSuccesses: metrics.GetOrCreateHistogram("setup_node_request_durations{success=\"true\"}"),
		routesSetup:           metricsutil.NewVictoriaMetricsIntGauge("setup_node_no_of_route_setups"),
		routesSetupFailed:     metricsutil.NewVictoriaMetricsIntGauge("setup_node_no_of_failed_route_setups"),
		routesSetupDuration:   metrics.GetOrCreateHistogram("setup_node_route_setup_duration{success=\"true\"}"),
	}
}

// RecordRequest implements `Metrics`.
func (m *VictoriaMetrics) RecordRequest() func(rules *routing.EdgeRules, err *error) {
	start := time.Now()
	m.activeRequests.Inc()

	return func(rules *routing.EdgeRules, err *error) {
		if *err == nil {
			m.reqDurationsSuccesses.UpdateDuration(start)
		} else {
			m.reqDurationsFailed.UpdateDuration(start)
		}

		m.activeRequests.Dec()
	}
}

// RecordRoute implements `Metrics`.
func (m *VictoriaMetrics) RecordRoute() func(err *error) {
	start := time.Now()
	m.routesSetup.Inc()

	return func(err *error) {
		m.routesSetupDuration.UpdateDuration(start)
		if *err != nil {
			m.routesSetupFailed.Inc()
		}
	}
}
