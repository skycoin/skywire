package utmetrics

import "github.com/skycoin/skywire-utilities/pkg/metricsutil"

// VictoriaMetrics implements `Metrics` using `VictoriaMetrics`.
type VictoriaMetrics struct {
	entriesCount *metricsutil.VictoriaMetricsIntGaugeWrapper
}

// NewVictoriaMetrics returns the Victoria Metrics implementation of `Metrics`.
func NewVictoriaMetrics() *VictoriaMetrics {
	return &VictoriaMetrics{
		entriesCount: metricsutil.NewVictoriaMetricsIntGauge("uptime_tracker_entries_count"),
	}
}

// SetEntriesCount implements `Metrics`.
func (m *VictoriaMetrics) SetEntriesCount(val int64) {
	m.entriesCount.Set(val)
}
