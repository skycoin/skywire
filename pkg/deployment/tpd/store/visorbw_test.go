package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestComputeVisorBW(t *testing.T) {
	const day = "2026-10-01"
	eb := func(sent, recv uint64) *EdgeBandwidth { return &EdgeBandwidth{Sent: sent, Recv: recv} }
	records := []TransportMetric{
		// Both edges reported: each is paid what it sent as far as the other
		// confirms receiving it.
		{Type: "stcpr", Edges: []string{"a", "b"}, Daily: []DailyEdgeBandwidth{{Date: day, A: eb(100, 7), B: eb(40, 90)}}},
		// Only A reported: nothing confirms it, so neither is paid.
		{Type: "sudph", Edges: []string{"a", "c"}, Daily: []DailyEdgeBandwidth{{Date: day, A: eb(10, 20)}}},
		// Another day's bytes are not this day's.
		{Type: "stcpr", Edges: []string{"b", "c"}, Daily: []DailyEdgeBandwidth{{Date: "2026-09-30", A: eb(999, 999), B: eb(999, 999)}}},
		// Marked same-network that day: left out entirely.
		{Type: "stcpr", Edges: []string{"a", "d"}, Daily: []DailyEdgeBandwidth{{Date: day, A: eb(500, 500), B: eb(500, 500), SameNetwork: true}}},
		// Marked on ANOTHER day only: this day counts.
		{Type: "dmsg", Edges: []string{"e", "d"}, Daily: []DailyEdgeBandwidth{
			{Date: "2026-09-30", A: eb(1, 1), B: eb(1, 1), SameNetwork: true},
			{Date: day, A: eb(5, 2), B: eb(3, 4)},
		}},
	}

	got := ComputeVisorBW(records, day)
	require.Equal(t, VisorBWVersion, got.Version)
	require.Equal(t, 3, got.Transports)
	require.Equal(t, 1, got.SameNetworkExcluded)
	require.Equal(t, 1, got.Unverified)
	require.Equal(t, map[string]map[string]uint64{
		"a": {"stcpr": 90},
		"b": {"stcpr": 7},
		"e": {"dmsg": 4},
		"d": {"dmsg": 2},
	}, got.Visors)
}

func TestSenderBytes(t *testing.T) {
	eb := func(sent, recv uint64) *EdgeBandwidth { return &EdgeBandwidth{Sent: sent, Recv: recv} }
	for name, tc := range map[string]struct {
		d            DailyEdgeBandwidth
		wantA, wantB uint64
	}{
		"peer received less":     {DailyEdgeBandwidth{A: eb(100, 50), B: eb(60, 80)}, 80, 50},
		"peer received more":     {DailyEdgeBandwidth{A: eb(100, 70), B: eb(60, 120)}, 100, 60},
		"only A reported":        {DailyEdgeBandwidth{A: eb(100, 50)}, 0, 0},
		"B record all zero":      {DailyEdgeBandwidth{A: eb(100, 50), B: eb(0, 0)}, 0, 0},
		"neither reported":       {DailyEdgeBandwidth{}, 0, 0},
		"one edge only received": {DailyEdgeBandwidth{A: eb(0, 30), B: eb(30, 0)}, 0, 30},
	} {
		a, b := SenderBytes(tc.d)
		require.Equal(t, tc.wantA, a, name)
		require.Equal(t, tc.wantB, b, name)
	}
}
