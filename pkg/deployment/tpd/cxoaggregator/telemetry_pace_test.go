package cxoaggregator

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestTelemetryPacerHoldsWithinWindow(t *testing.T) {
	var p telemetryPacer
	pk, _ := cipher.GenerateKeyPair()
	k := telKey{uuid.New(), pk}
	t0 := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

	s, ok := p.offer(k, telSnap{sent: 10, throughput: 5, latMin: 1, latMax: 3, latAvg: 2}, t0)
	require.True(t, ok, "first report applies")
	require.EqualValues(t, 10, s.sent)

	_, ok = p.offer(k, telSnap{sent: 20, throughput: 50}, t0.Add(45*time.Second))
	require.False(t, ok)
	_, ok = p.offer(k, telSnap{sent: 30, throughput: 7}, t0.Add(90*time.Second))
	require.False(t, ok)
	require.Empty(t, p.due(t0.Add(100*time.Second)), "window not passed")

	// The window passes with no newer report: the held one is flushed with
	// the latest counters and the peak throughput.
	held := p.due(t0.Add(telemetryApplyEvery + time.Second))
	require.Len(t, held, 1)
	require.EqualValues(t, 30, held[0].s.sent)
	require.EqualValues(t, 50, held[0].s.throughput)

	// Latency survives a newer report that has none.
	_, ok = p.offer(k, telSnap{sent: 40, latMin: 4, latMax: 6, latAvg: 5}, t0.Add(telemetryApplyEvery+10*time.Second))
	require.False(t, ok)
	s, ok = p.offer(k, telSnap{sent: 50}, t0.Add(2*telemetryApplyEvery+2*time.Second))
	require.True(t, ok)
	require.EqualValues(t, 50, s.sent)
	require.EqualValues(t, 5, s.latAvg)
}

func TestTelemetryPacerAppliesAtOnceOnANewDay(t *testing.T) {
	var p telemetryPacer
	pk, _ := cipher.GenerateKeyPair()
	k := telKey{uuid.New(), pk}
	night := time.Date(2026, 9, 30, 23, 59, 30, 0, time.UTC)
	_, ok := p.offer(k, telSnap{sent: 1}, night)
	require.True(t, ok)
	_, ok = p.offer(k, telSnap{sent: 2}, night.Add(45*time.Second))
	require.True(t, ok, "first report of the new UTC day is not held")
}

func TestTelemetryPacerForgetsIdleEntries(t *testing.T) {
	var p telemetryPacer
	pk, _ := cipher.GenerateKeyPair()
	k := telKey{uuid.New(), pk}
	t0 := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	p.offer(k, telSnap{sent: 1}, t0)
	p.due(t0.Add(telemetryStateSweepAfter + time.Minute))
	require.Empty(t, p.m)
}
