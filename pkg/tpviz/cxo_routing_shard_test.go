package tpviz

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// A routing shard decodes to its entries with the dropped t_id restored.
func TestRoutingShardEntries(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	edges := transport.SortEdges(a, b)
	body, err := json.Marshal([]map[string]any{{"edges": edges, "type": tptypes.SUDPH, "latency_ms": 12}})
	require.NoError(t, err)

	got := routingShardEntries(cxoutils.Gzip(body))
	require.Len(t, got, 1)
	require.Equal(t, transport.MakeTransportID(edges[0], edges[1], tptypes.SUDPH), got[0].ID)
	require.Equal(t, 12.0, got[0].Latency)
	require.Nil(t, routingShardEntries([]byte("not json")))
}
