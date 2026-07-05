// Package metrics pkg/dmsg/disc/metrics/metrics_test.go: covers both
// Metrics implementations — the no-op Empty and the VictoriaMetrics
// gauge wrapper. Both are pure (the gauge wrapper just holds in-memory
// VictoriaMetrics counters), so no external metrics backend is needed.
package metrics

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Both implementations must satisfy the Metrics interface.
var (
	_ Metrics = Empty{}
	_ Metrics = (*VictoriaMetrics)(nil)
)

func TestEmpty(t *testing.T) {
	m := NewEmpty()
	// No-ops: must not panic for any value.
	require.NotPanics(t, func() {
		m.SetClientsCount(0)
		m.SetClientsCount(42)
		m.SetServersCount(-1)
		m.SetServersCount(100)
	})
}

func TestVictoriaMetrics(t *testing.T) {
	m := NewVictoriaMetrics()
	require.NotNil(t, m)
	require.NotNil(t, m.clientsCount)
	require.NotNil(t, m.serversCount)

	// Set both gauges; the wrapper records the value with no backend.
	require.NotPanics(t, func() {
		m.SetClientsCount(7)
		m.SetServersCount(3)
		// Overwrite to exercise the Set path again.
		m.SetClientsCount(0)
		m.SetServersCount(0)
	})
}
