//go:build !mobile

// Package setupmetrics pkg/router/setupmetrics/victoria_metrics.go c2-net-routing
//
// The VictoriaMetrics implementation is desktop-only: 0magnet/metrics has no
// process-metrics source for GOOS=ios, and only the route setup node service
// (pkg/services/sn) constructs it. The mobile build keeps the Metrics
// interface (metrics.go), Empty and the stats Collector, none of which import
// it.
package setupmetrics

import (
	"time"

	"github.com/0magnet/metrics"

	"github.com/skycoin/skywire/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/routing"
)

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

	return func(_ *routing.EdgeRules, err *error) {
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
