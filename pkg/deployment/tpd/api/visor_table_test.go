// Package api pkg/deployment/tpd/api/visor_table_test.go c4-net-discovery
package api

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// Every visor gets a row with its transports by type, busiest first; an
// online visor without transports is listed; a transport counts for both its
// edges.
func TestVisorTable(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	c, _ := cipher.GenerateKeyPair()
	idle, _ := cipher.GenerateKeyPair()
	tp := func(x, y cipher.PubKey, typ string) *transport.Entry {
		return &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(x, y), Type: tptypes.Type(typ)}
	}
	api := &API{
		transportsCache: []*transport.Entry{tp(a, b, "stcpr"), tp(a, c, "sudph"), tp(a, c, "stcpr"), tp(a, a, "stcpr")},
		uptimesCache:    []store.VisorSummary{{PK: a, Online: true}, {PK: idle, Online: true}},
	}
	tbl := api.visorTable(context.Background())
	require.Equal(t, []string{"Public key", "Role", "Online", "Total", "stcpr", "sudph"}, tbl.Head)
	require.Len(t, tbl.Rows, 4)
	require.Equal(t, []string{a.Hex(), "", "yes", "3", "2", "1"}, tbl.Rows[0], "the busiest visor first; its self transport is left out")
	require.Equal(t, []string{idle.Hex(), "", "yes", "0", "", ""}, tbl.Rows[3], "an online visor without transports comes last")
	require.Contains(t, tbl.Note, "4 visors with 3 transports, 2 of them online")
}
