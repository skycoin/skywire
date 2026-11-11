package ui2

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlanBulk(t *testing.T) {
	ns := []Node{
		{PK: "a", Online: true, AppNames: []string{"skychat", "vpn-client"}},
		{PK: "b", Online: false, AppNames: []string{"skychat"}},
		{PK: "c", Online: true, Children: []Node{{PK: "d", Online: true, ViaSub: true}}},
	}
	pks := []string{"a", "b", "c", "d", "x"}

	b := PlanBulk(Bulk{Action: BulkStart, App: "skychat"}, ns, pks, nil)
	skips := map[string]string{}
	for _, r := range b.Rows {
		skips[r.PK] = r.Skip
	}
	require.Equal(t, map[string]string{
		"a": "", "b": "nodes.bulk-apps.offline", "c": "nodes.bulk-apps.not-configured",
		"d": "nodes.bulk-apps.via-sub", "x": "nodes.bulk-apps.offline",
	}, skips)
	require.Equal(t, 1, b.Todo())
	require.Equal(t, []string{"skychat", "vpn-client"}, b.Apps, "start offers the apps of the selected visors")

	b = PlanBulk(Bulk{Action: BulkAdd, App: "skychat"}, ns, pks, []string{"skysocks"})
	require.Equal(t, "nodes.bulk-apps.already-added", b.Rows[0].Skip)
	require.True(t, b.Rows[2].AddFirst)
	require.Contains(t, b.Apps, "skysocks", "add offers the catalog too")

	b = PlanBulk(Bulk{Action: BulkAdd, App: "skychat", StartAfter: true}, ns, pks, nil)
	require.Empty(t, b.Rows[0].Skip, "an app already added is still started")
	require.False(t, b.Rows[0].AddFirst)

	b.Rows[0].Done, b.Rows[2].Err = true, "boom"
	ok, failed, skipped := b.Counts()
	require.Equal(t, [3]int{1, 1, 3}, [3]int{ok, failed, skipped})
}
