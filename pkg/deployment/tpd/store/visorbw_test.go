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
		// a and d share an IP: left out entirely.
		{Type: "dmsg", Edges: []string{"a", "d"}, Daily: []DailyEdgeBandwidth{{Date: day, A: eb(500, 500)}}},
		// e has no class (no survey): never matched as same-IP.
		{Type: "dmsg", Edges: []string{"e", "d"}, Daily: []DailyEdgeBandwidth{{Date: day, B: eb(3, 4)}}},
	}
	classes := &IPClasses{Classes: map[string]string{
		"a": IPClass([]byte("k"), "1.2.3.4"),
		"d": IPClass([]byte("k"), "1.2.3.4"),
		"b": IPClass([]byte("k"), "5.6.7.8"),
		"c": IPClass([]byte("k"), "9.9.9.9"),
	}}

	got := ComputeVisorBW(records, day, classes)
	require.Equal(t, 4, got.Transports)
	require.Equal(t, 1, got.SameIPExcluded)
	require.Equal(t, map[string]map[string]uint64{
		"a": {"stcpr": 100, "sudph": 10},
		"b": {"stcpr": 40},
		"c": {"sudph": 20},
		"e": {"dmsg": 4},
		"d": {"dmsg": 3},
	}, got.Visors)

	// Without classes nothing is left out.
	none := ComputeVisorBW(records, day, nil)
	require.Equal(t, 0, none.SameIPExcluded)
	require.Equal(t, uint64(500), none.Visors["a"]["dmsg"])
}

func TestIPClass(t *testing.T) {
	require.Equal(t, IPClass([]byte("k"), "1.2.3.4"), IPClass([]byte("k"), "1.2.3.4"))
	require.NotEqual(t, IPClass([]byte("k"), "1.2.3.4"), IPClass([]byte("k"), "1.2.3.5"))
	require.NotEqual(t, IPClass([]byte("k"), "1.2.3.4"), IPClass([]byte("other"), "1.2.3.4"), "the class depends on the key")
}
