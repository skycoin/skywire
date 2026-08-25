// Package sdmetrics metrics_test.go: unit tests for the Empty and
// VictoriaMetrics implementations of the Metrics interface. The setters are
// fire-and-forget; the tests assert both implementations satisfy Metrics,
// construct cleanly, and accept calls without panicking. For VictoriaMetrics
// the wrapped gauge value is read back to confirm Set actually stored it.
package sdmetrics

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Both implementations must satisfy the Metrics interface.
var (
	_ Metrics = Empty{}
	_ Metrics = (*VictoriaMetrics)(nil)
)

// exercise calls every setter on the given Metrics with a sample value so a
// panic in any of them fails the test.
func exercise(m Metrics) {
	m.SetServiceTypesCount(1)
	m.SetServicesRegByTypeCount(2)
	m.SetServiceTypeVPNCount(3)
	m.SetServiceTypeVisorCount(4)
	m.SetServiceTypeSkysocksCount(5)
}

func TestEmpty(t *testing.T) {
	m := NewEmpty()
	require.NotPanics(t, func() { exercise(m) })
}

func TestVictoriaMetrics_Setters(t *testing.T) {
	m := NewVictoriaMetrics()
	require.NotNil(t, m)
	require.NotPanics(t, func() { exercise(m) })

	// Each setter writes through to its own wrapped gauge; confirm the
	// stored values round-trip.
	require.Equal(t, uint64(1), m.serviceTypesCount.Val())
	require.Equal(t, uint64(2), m.servicesRegByTypeCount.Val())
	require.Equal(t, uint64(3), m.serviceTypeVPNCount.Val())
	require.Equal(t, uint64(4), m.serviceTypeVisorCount.Val())
	require.Equal(t, uint64(5), m.serviceTypeSkysocksCount.Val())
}

func TestVictoriaMetrics_Overwrite(t *testing.T) {
	m := NewVictoriaMetrics()
	m.SetServiceTypesCount(10)
	m.SetServiceTypesCount(0)
	require.Equal(t, uint64(0), m.serviceTypesCount.Val())
}
