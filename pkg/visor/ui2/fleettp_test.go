package ui2

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFleetTpsOf(t *testing.T) {
	tps := []FleetTp{
		{ID: "1", Type: "stcpr", A: "a", B: "b", Sent: 1, Live: true},
		{ID: "2", Type: "sudph", A: "a", B: "c", Sent: 10},
	}
	f := FleetTpsOf(FleetTps{}, tps, ListQuery{})
	require.Equal(t, "2", f.Rows[0].ID, "busiest first")
	f = FleetTpsOf(FleetTps{Tree: true}, tps, ListQuery{})
	require.Equal(t, "a", f.Visors[0].PK)
	require.Len(t, f.Visors[0].Tps, 2)
	require.Equal(t, 1, f.Visors[0].Offline)
	require.Equal(t, "a", f.Visors[1].Tps[0].B, "a child names the other end")
	require.Len(t, FleetTpsOf(FleetTps{}, tps, ListQuery{Q: "sudph"}).Rows, 1)
}

func TestFleetTpsRender(t *testing.T) {
	tps := []FleetTp{{ID: "tp-1", Type: "stcpr", A: "aa", B: "bb", Sent: 2048, Live: true, LatAvg: 1500}}
	out := renderEN(t, FleetTpsBody(FleetTpsOf(FleetTps{Days: 7, Total: 1, Returned: 1, Bandwidth: 2048}, tps, ListQuery{})))
	require.Contains(t, out, "1 transports · 2.0 KiB network bandwidth (7 days)")
	require.Contains(t, out, "1.5 ms")
	require.Contains(t, out, ">aa<")
	out = renderEN(t, FleetTpsBody(FleetTpsOf(FleetTps{Days: 7, Tree: true, Total: 1}, tps, ListQuery{})))
	require.Contains(t, out, `data-open-key="aa"`)
	require.NotContains(t, out, "Edge A", "the tree view has no edge columns")
}
