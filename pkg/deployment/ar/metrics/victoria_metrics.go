// Package armetrics pkg/deployment/ar/metrics/victoria_metrics.go c4-net-discovery
package armetrics

import (
	"github.com/skycoin/skywire/pkg/metricsutil"
)

// VictoriaMetrics implements `Metrics` using `VictoriaMetrics`.
type VictoriaMetrics struct {
	clientsCount *metricsutil.VictoriaMetricsIntGaugeWrapper
}

// NewVictoriaMetrics returns the Victoria Metrics implementation of `Metrics`.
func NewVictoriaMetrics() *VictoriaMetrics {
	return &VictoriaMetrics{
		clientsCount: metricsutil.NewVictoriaMetricsIntGauge("address_resolver_clients_count"),
	}
}

// SetClientsCount implements `Metrics`.
func (m *VictoriaMetrics) SetClientsCount(val int64) {
	m.clientsCount.Set(val)
}
