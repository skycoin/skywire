package ui2

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/visor/netview"
)

func TestNetViewOf(t *testing.T) {
	es := []netview.Entry{
		{PK: "a", Country: "DE", Version: "v1", Total: 10, UTStatus: "online"},
		{PK: "b", Country: "US", Version: "v1", Total: 1, UTStatus: "online"},
		{PK: "c", Country: "DE", UTStatus: "offline"},
		{PK: "d"},
	}
	v := NetViewOf(es, ListQuery{}, "", "", 0, false)
	require.Len(t, v.Rows, 2, "online only by default")
	require.Equal(t, [3]int{2, 1, 1}, [3]int{v.Online, v.Offline, v.NotInUT})
	require.Len(t, NetViewOf(es, ListQuery{}, "de", "", 0, true).Rows, 2)
	require.Len(t, NetViewOf(es, ListQuery{}, "", "", 5, false).Rows, 1)
	require.Len(t, NetViewOf(es, ListQuery{Q: "us"}, "", "", 0, true).Rows, 1)
}
