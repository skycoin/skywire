package api

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

func sortedKeys(n int) []cipher.PubKey {
	out := make([]cipher.PubKey, n)
	for i := range out {
		out[i], _ = cipher.GenerateKeyPair()
	}
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j].Hex() < out[i].Hex() {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func tp(a, b cipher.PubKey, lat float64) *transport.Entry {
	return &transport.Entry{Edges: transport.SortEdges(a, b), Type: tptypes.STCPR, Latency: lat}
}

func shard(t *testing.T, shards map[string][]byte, pk cipher.PubKey) []allTransportsWireEntry {
	t.Helper()
	body, ok := shards[RoutingPathPrefix+pk.Hex()]
	if !ok {
		return nil
	}
	var out []allTransportsWireEntry
	require.NoError(t, json.Unmarshal(cxoutils.Gunzip(body), &out))
	return out
}

// Each transport lands once, in its lower-keyed edge's shard; self-loops
// are not transports; figures are rounded; input order does not change the
// bytes; and a change touches only the shard it belongs to.
func TestRoutingShards(t *testing.T) {
	k := sortedKeys(3) // k[0] < k[1] < k[2]
	entries := []*transport.Entry{tp(k[0], k[1], 12.34), tp(k[0], k[2], 5), tp(k[1], k[2], 7), {Edges: [2]cipher.PubKey{k[2], k[2]}}}

	shards, err := routingShards(entries)
	require.NoError(t, err)
	require.Len(t, shards, 2, "k[2] has no lower-keyed transports and self-loops are dropped")
	require.Len(t, shard(t, shards, k[0]), 2)
	require.Len(t, shard(t, shards, k[1]), 1)
	for _, e := range shard(t, shards, k[0]) {
		if e.Edges[1] == k[1] {
			require.Equal(t, 12.0, e.Latency, "12.34 ms rounds to two significant digits")
		}
	}

	reversed := []*transport.Entry{entries[3], entries[2], entries[1], entries[0]}
	again, err := routingShards(reversed)
	require.NoError(t, err)
	for path, body := range shards {
		require.True(t, bytes.Equal(body, again[path]), "shard %s depends on input order", path)
	}

	// A latency jitter below the rounding leaves every shard alone; a real
	// change rewrites only its own shard.
	entries[2].Latency = 7.04
	jitter, err := routingShards(entries)
	require.NoError(t, err)
	require.Equal(t, shards, jitter)
	entries[2].Latency = 30
	moved, err := routingShards(entries)
	require.NoError(t, err)
	require.Equal(t, shards[RoutingPathPrefix+k[0].Hex()], moved[RoutingPathPrefix+k[0].Hex()])
	require.NotEqual(t, shards[RoutingPathPrefix+k[1].Hex()], moved[RoutingPathPrefix+k[1].Hex()])
}

func TestRoundSig2(t *testing.T) {
	require.Equal(t, 0.0, roundSig2(0))
	require.Equal(t, 12.0, roundSig2(12.34))
	require.Equal(t, 150.0, roundSig2(153))
	require.Equal(t, 0.0012, roundSig2(0.00123))
}
