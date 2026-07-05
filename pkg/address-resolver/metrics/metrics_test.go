// Package armetrics pkg/address-resolver/metrics/metrics_test.go: unit tests
// for the address-resolver metrics implementations.
package armetrics

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEmpty verifies the no-op implementation satisfies Metrics and its method
// does not panic.
func TestEmpty(t *testing.T) {
	var m Metrics = NewEmpty()
	assert.NotPanics(t, func() { m.SetClientsCount(5) })
}

// TestVictoriaMetricsSetClientsCount verifies the Victoria Metrics
// implementation stores the value it is given.
func TestVictoriaMetricsSetClientsCount(t *testing.T) {
	m := NewVictoriaMetrics()
	require.NotNil(t, m)

	var _ Metrics = m // implements the interface

	m.SetClientsCount(42)
	assert.Equal(t, int64(42), m.clientsCount.Val())

	// Overwriting replaces the previous value.
	m.SetClientsCount(7)
	assert.Equal(t, int64(7), m.clientsCount.Val())
}
