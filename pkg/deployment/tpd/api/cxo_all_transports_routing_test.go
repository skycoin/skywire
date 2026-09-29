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

func TestRoutingSnapshotCarriesMetricsNotBandwidth(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	e := &transport.Entry{Edges: transport.SortEdges(a, b), Type: tptypes.STCPR, Latency: 12.3456, ThroughputBps: 123456.7, Bandwidth: 999}
	p := &AllTransportsCXOPublisher{api: &API{store: &qosStore{entries: []*transport.Entry{e}}}}

	entries, err := p.routingSnapshot(context.Background())
	require.NoError(t, err)
	body, err := json.Marshal(toWireEntries(entries))
	require.NoError(t, err)

	var got []map[string]any
	require.NoError(t, json.Unmarshal(body, &got))
	require.Len(t, got, 1)
	require.Equal(t, 12.3, got[0]["latency_ms"])
	require.Equal(t, 123000.0, got[0]["throughput_bps"])
	require.NotContains(t, got[0], "bandwidth", "registration bandwidth is not routing data")
}

func TestRoundSig3(t *testing.T) {
	require.Equal(t, 0.0, roundSig3(0))
	require.Equal(t, 123000.0, roundSig3(123456.7))
	require.Equal(t, 0.00123, roundSig3(0.0012345))
	require.Equal(t, 9.99, roundSig3(9.994))
}
