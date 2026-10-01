package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestComputeVisorBW(t *testing.T) {
	const day = "2026-10-01"
	eb := func(sent, recv uint64) *EdgeBandwidth { return &EdgeBandwidth{Sent: sent, Recv: recv} }
	records := []TransportMetric{
		// Both edges reported: each is paid what it says it sent.
		{Type: "stcpr", Edges: []string{"a", "b"}, Daily: []DailyEdgeBandwidth{{Date: day, A: eb(100, 7), B: eb(40, 90)}}},
		// Only A reported: A's recv stands for what B sent.
		{Type: "sudph", Edges: []string{"a", "c"}, Daily: []DailyEdgeBandwidth{{Date: day, A: eb(10, 20)}}},
		// Another day's bytes are not this day's.
		{Type: "stcpr", Edges: []string{"b", "c"}, Daily: []DailyEdgeBandwidth{{Date: "2026-09-30", A: eb(999, 999)}}},
		// Marked same-network that day: left out entirely.
		{Type: "stcpr", Edges: []string{"a", "d"}, Daily: []DailyEdgeBandwidth{{Date: day, A: eb(500, 500), SameNetwork: true}}},
		// Marked on ANOTHER day only: this day counts.
		{Type: "dmsg", Edges: []string{"e", "d"}, Daily: []DailyEdgeBandwidth{
			{Date: "2026-09-30", B: eb(1, 1), SameNetwork: true},
			{Date: day, B: eb(3, 4)},
		}},
	}

	got := ComputeVisorBW(records, day)
	require.Equal(t, VisorBWVersion, got.Version)
	require.Equal(t, 4, got.Transports)
	require.Equal(t, 1, got.SameNetworkExcluded)
	require.Equal(t, map[string]map[string]uint64{
		"a": {"stcpr": 100, "sudph": 10},
		"b": {"stcpr": 40},
		"c": {"sudph": 20},
		"e": {"dmsg": 4},
		"d": {"dmsg": 3},
	}, got.Visors)
}
