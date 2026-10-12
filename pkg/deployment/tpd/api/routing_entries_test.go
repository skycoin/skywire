package api

import (
	"context"
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

// The routing read is the QoS one, so the routing feed carries metrics.
func TestRoutingEntriesCarryMetrics(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	e := &transport.Entry{Edges: transport.SortEdges(a, b), Type: tptypes.STCPR, Latency: 12.3456, ThroughputBps: 123456.7, Bandwidth: 999}

	entries, err := routingEntries(context.Background(), &qosStore{entries: []*transport.Entry{e}})
	require.NoError(t, err)
	require.Equal(t, 12.3456, entries[0].Latency)
}
