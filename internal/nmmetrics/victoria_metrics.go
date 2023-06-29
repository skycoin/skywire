package nmmetrics

import "github.com/skycoin/skywire/pkg/metricsutil"

// VictoriaMetrics implements `Metrics` using `VictoriaMetrics`.
type VictoriaMetrics struct {
	totalVpnServerCount *metricsutil.VictoriaMetricsIntGaugeWrapper
	totalVisorCount     *metricsutil.VictoriaMetricsIntGaugeWrapper
	totalStcprTpCount   *metricsutil.VictoriaMetricsIntGaugeWrapper
	totalSudphTpCount   *metricsutil.VictoriaMetricsIntGaugeWrapper
}

// NewVictoriaMetrics returns the Victoria Metrics implementation of `Metrics`.
func NewVictoriaMetrics() *VictoriaMetrics {
	return &VictoriaMetrics{
		totalVpnServerCount: metricsutil.NewVictoriaMetricsIntGauge("network_monitor_total_vpn_server_count"),
		totalVisorCount:     metricsutil.NewVictoriaMetricsIntGauge("network_monitor_total_visor_count"),
		totalStcprTpCount:   metricsutil.NewVictoriaMetricsIntGauge("network_monitor_total_visors_with_stcpr_transport_count"),
		totalSudphTpCount:   metricsutil.NewVictoriaMetricsIntGauge("network_monitor_total_visors_with_sudph_transport_count"),
	}
}

// SetTotalVpnServerCount implements `Metrics`.
func (m *VictoriaMetrics) SetTotalVpnServerCount(val int64) {
	m.totalVpnServerCount.Set(val)
}

// SetTotalVisorCount implements `Metrics`.
func (m *VictoriaMetrics) SetTotalVisorCount(val int64) {
	m.totalVisorCount.Set(val)
}

// SetTpCount implements `Metrics`.
func (m *VictoriaMetrics) SetTpCount(stcprCount int64, sudphCount int64) {
	m.totalStcprTpCount.Set(stcprCount)
	m.totalSudphTpCount.Set(sudphCount)
}
