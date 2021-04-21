package servermetrics

import (
	"fmt"

	"github.com/VictoriaMetrics/metrics"

	"github.com/skycoin/dmsg/metricsutil"
)

// VictoriaMetrics implements `Metrics` using `VictoriaMetrics`.
type VictoriaMetrics struct {
	packetsPerMinute   *metricsutil.VictoriaMetricsUintGaugeWrapper
	packetsPerSecond   *metricsutil.VictoriaMetricsUintGaugeWrapper
	clientsCount       *metricsutil.VictoriaMetricsIntGaugeWrapper
	activeSessions     *metricsutil.VictoriaMetricsIntGaugeWrapper
	successfulSessions *metrics.Counter
	failedSessions     *metrics.Counter
	activeStreams      *metricsutil.VictoriaMetricsIntGaugeWrapper
	successfulStreams  *metrics.Counter
	failedStreams      *metrics.Counter
}

// NewVictoriaMetrics returns the Victoria Metrics implementation of Metrics.
func NewVictoriaMetrics() *VictoriaMetrics {
	return &VictoriaMetrics{
		packetsPerMinute:   metricsutil.NewVictoriaMetricsUintGauge("packets_per_minute"),
		packetsPerSecond:   metricsutil.NewVictoriaMetricsUintGauge("packets_per_second"),
		clientsCount:       metricsutil.NewVictoriaMetricsIntGauge("clients_count"),
		activeSessions:     metricsutil.NewVictoriaMetricsIntGauge("vm_active_sessions_count"),
		successfulSessions: metrics.GetOrCreateCounter("vm_session_success_total"),
		failedSessions:     metrics.GetOrCreateCounter("vm_session_fail_total"),
		activeStreams:      metricsutil.NewVictoriaMetricsIntGauge("vm_active_streams_count"),
		successfulStreams:  metrics.GetOrCreateCounter("vm_stream_success_total"),
		failedStreams:      metrics.GetOrCreateCounter("vm_stream_fail_total"),
	}
}

// SetPacketsPerMinute implements `Metrics`.
func (m *VictoriaMetrics) SetPacketsPerMinute(val uint64) {
	m.packetsPerMinute.Set(val)
}

// SetPacketsPerSecond implements `Metrics`.
func (m *VictoriaMetrics) SetPacketsPerSecond(val uint64) {
	m.packetsPerSecond.Set(val)
}

// SetClientsCount implements `Metrics`.
func (m *VictoriaMetrics) SetClientsCount(val int64) {
	m.clientsCount.Set(val)
}

// RecordSession implements `Metrics`.
func (m *VictoriaMetrics) RecordSession(delta DeltaType) {
	switch delta {
	case 0:
		m.failedSessions.Inc()
	case 1:
		m.successfulSessions.Inc()
		m.activeSessions.Inc()
	case -1:
		m.activeSessions.Dec()
	default:
		panic(fmt.Errorf("invalid delta: %d", delta))
	}
}

// RecordStream implements Metrics.
func (m *VictoriaMetrics) RecordStream(delta DeltaType) {
	switch delta {
	case 0:
		m.failedStreams.Inc()
	case 1:
		m.successfulStreams.Inc()
		m.activeStreams.Inc()
	case -1:
		m.activeStreams.Dec()
	default:
		panic(fmt.Errorf("invalid delta: %d", delta))
	}
}
