package sdmetrics

import (
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/metricsutil"
)

// VictoriaMetrics implements `Metrics` using `VictoriaMetrics`.
type VictoriaMetrics struct {
	servicesRegByTypeCount   *metricsutil.VictoriaMetricsUintGaugeWrapper
	serviceTypesCount        *metricsutil.VictoriaMetricsUintGaugeWrapper
	serviceTypeVPNCount      *metricsutil.VictoriaMetricsUintGaugeWrapper
	serviceTypeVisorCount    *metricsutil.VictoriaMetricsUintGaugeWrapper
	serviceTypeSkysocksCount *metricsutil.VictoriaMetricsUintGaugeWrapper
}

// NewVictoriaMetrics returns the Victoria Metrics implementation of Metrics.
func NewVictoriaMetrics() *VictoriaMetrics {
	return &VictoriaMetrics{
		servicesRegByTypeCount:   metricsutil.NewVictoriaMetricsUintGauge("service_discovery_services_registered_by_type_count"),
		serviceTypesCount:        metricsutil.NewVictoriaMetricsUintGauge("service_discovery_service_types_count"),
		serviceTypeVPNCount:      metricsutil.NewVictoriaMetricsUintGauge("service_discovery_service_type_vpn_count"),
		serviceTypeVisorCount:    metricsutil.NewVictoriaMetricsUintGauge("service_discovery_service_type_visor_count"),
		serviceTypeSkysocksCount: metricsutil.NewVictoriaMetricsUintGauge("service_discovery_service_type_skysocks_count"),
	}
}

// SetServiceTypesCount implements `Metrics`.
func (m *VictoriaMetrics) SetServiceTypesCount(val uint64) {
	m.serviceTypesCount.Set(val)
}

// SetServicesRegByTypeCount implements `Metrics`.
func (m *VictoriaMetrics) SetServicesRegByTypeCount(val uint64) {
	m.servicesRegByTypeCount.Set(val)
}

// SetServiceTypeVPNCount implements `Metrics`.
func (m *VictoriaMetrics) SetServiceTypeVPNCount(val uint64) {
	m.serviceTypeVPNCount.Set(val)
}

// SetServiceTypeVisorCount implements `Metrics`.
func (m *VictoriaMetrics) SetServiceTypeVisorCount(val uint64) {
	m.serviceTypeVisorCount.Set(val)
}

// SetServiceTypeSkysocksCount implements `Metrics`.
func (m *VictoriaMetrics) SetServiceTypeSkysocksCount(val uint64) {
	m.serviceTypeSkysocksCount.Set(val)
}
