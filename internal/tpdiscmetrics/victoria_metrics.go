package tpdiscmetrics

import (
	"sync"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/metricsutil"
	"github.com/skycoin/skywire/pkg/transport/types"
)

// VictoriaMetrics implements `Metrics` using `VictoriaMetrics`.
type VictoriaMetrics struct {
	tpCountsMx  sync.Mutex
	stcpCounts  *metricsutil.VictoriaMetricsIntGaugeWrapper
	stcprCounts *metricsutil.VictoriaMetricsIntGaugeWrapper
	sudphCounts *metricsutil.VictoriaMetricsIntGaugeWrapper
	dmsgCounts  *metricsutil.VictoriaMetricsIntGaugeWrapper
}

// NewVictoriaMetrics returns the Victoria Metrics implementation of `Metrics`.
func NewVictoriaMetrics() *VictoriaMetrics {
	return &VictoriaMetrics{
		stcpCounts:  metricsutil.NewVictoriaMetricsIntGauge("transport_discovery_stcp_count"),
		stcprCounts: metricsutil.NewVictoriaMetricsIntGauge("transport_discovery_stcpr_count"),
		sudphCounts: metricsutil.NewVictoriaMetricsIntGauge("transport_discovery_sudph_count"),
		dmsgCounts:  metricsutil.NewVictoriaMetricsIntGauge("transport_discovery_dmsg_count"),
	}
}

// SetTPCounts implements `Metrics`.
func (m *VictoriaMetrics) SetTPCounts(tpCounts map[types.Type]int) {
	m.tpCountsMx.Lock()
	defer m.tpCountsMx.Unlock()

	for tpType, count := range tpCounts {
		switch tpType {
		case types.STCP:
			m.stcpCounts.Set(int64(count))
		case types.STCPR:
			m.stcprCounts.Set(int64(count))
		case types.SUDPH:
			m.sudphCounts.Set(int64(count))
		case types.DMSG:
			m.dmsgCounts.Set(int64(count))
		}
	}
}
