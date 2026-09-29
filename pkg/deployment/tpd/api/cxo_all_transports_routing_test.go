package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// qosStore answers only the QoS read; the metric-free GetAllTransports
// panics on the nil embedded Store, which asserts it is not used.
type qosStore struct {
	store.Store
	entries []*transport.Entry
}

func (s *qosStore) GetAllTransportsWithLatency(_ context.Context, self bool) ([]*transport.Entry, error) {
	if self {
		panic("routing snapshot asked for self-loops")
	}
	return s.entries, nil
}

// The routing read is the QoS one; the all-transports feed carries topology
// only, as it always did — metrics are the routing feed's.
func TestRoutingEntriesCarryMetricsLegacyDoesNot(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	e := &transport.Entry{Edges: transport.SortEdges(a, b), Type: tptypes.STCPR, Latency: 12.3456, ThroughputBps: 123456.7, Bandwidth: 999}

	entries, err := routingEntries(context.Background(), &qosStore{entries: []*transport.Entry{e}})
	require.NoError(t, err)
	require.Equal(t, 12.3456, entries[0].Latency)

	body, err := json.Marshal(toWireEntries(entries))
	require.NoError(t, err)
	var got []map[string]any
	require.NoError(t, json.Unmarshal(body, &got))
	require.Len(t, got, 1)
	for _, k := range []string{"latency_ms", "throughput_bps", "bandwidth"} {
		require.NotContains(t, got[0], k)
	}
}
