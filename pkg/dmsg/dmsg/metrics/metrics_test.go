// Package metrics metrics_test.go: unit tests for the Empty and
// VictoriaMetrics implementations of the Metrics interface. The setters are
// fire-and-forget, so the tests assert interface conformance, clean
// construction, and that every code path (including each RecordSession /
// RecordStream delta branch and the invalid-delta default) runs without
// panicking. For VictoriaMetrics the wrapped gauges are read back to confirm
// the active-session/stream counts move as expected.
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

// exercise calls every setter/recorder on the given Metrics so a panic in any
// of them fails the test. Covers all three documented delta types plus an
// out-of-range value that hits the default branch.
func exercise(m Metrics) {
	m.SetClientsCount(7)
	m.SetPacketsPerSecond(100)
	m.SetPacketsPerMinute(6000)
	for _, d := range []DeltaType{DeltaConnect, DeltaDisconnect, DeltaFailed, DeltaType(42)} {
		m.RecordSession(d)
		m.RecordStream(d)
	}
}

func TestEmpty(t *testing.T) {
	m := NewEmpty()
	require.NotPanics(t, func() { exercise(m) })
}

func TestVictoriaMetrics_Exercise(t *testing.T) {
	m := NewVictoriaMetrics()
	require.NotNil(t, m)
	require.NotPanics(t, func() { exercise(m) })
}

func TestVictoriaMetrics_Setters(t *testing.T) {
	m := NewVictoriaMetrics()
	m.SetClientsCount(42)
	m.SetPacketsPerSecond(13)
	m.SetPacketsPerMinute(99)

	require.Equal(t, int64(42), m.clientsCount.Val())
	require.Equal(t, uint64(13), m.packetsPerSecond.Val())
	require.Equal(t, uint64(99), m.packetsPerMinute.Val())
}

func TestVictoriaMetrics_RecordSessionActiveCount(t *testing.T) {
	m := NewVictoriaMetrics()

	// Two connects, then one disconnect -> net +1 active session.
	m.RecordSession(DeltaConnect)
	m.RecordSession(DeltaConnect)
	m.RecordSession(DeltaDisconnect)
	require.Equal(t, int64(1), m.activeSessions.Val())

	// DeltaFailed / invalid delta don't touch the active gauge.
	m.RecordSession(DeltaFailed)
	m.RecordSession(DeltaType(99))
	require.Equal(t, int64(1), m.activeSessions.Val())
}

func TestVictoriaMetrics_RecordStreamActiveCount(t *testing.T) {
	m := NewVictoriaMetrics()

	m.RecordStream(DeltaConnect)
	m.RecordStream(DeltaConnect)
	m.RecordStream(DeltaConnect)
	m.RecordStream(DeltaDisconnect)
	require.Equal(t, int64(2), m.activeStreams.Val())

	m.RecordStream(DeltaFailed)
	m.RecordStream(DeltaType(-7))
	require.Equal(t, int64(2), m.activeStreams.Val())
}
