package visor

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// A fully-populated TransportSummary must serialize exactly as it did
// before MarshalJSON existed — the detail endpoints depend on it.
func TestTransportSummaryMarshalJSONKeepsSetIdentifiers(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	remote, _ := cipher.GenerateKeyPair()
	id := uuid.New()

	b, err := json.Marshal(TransportSummary{
		ID:     id,
		Local:  pk,
		Remote: remote,
		Type:   types.Type("stcpr"),
	})
	require.NoError(t, err)

	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(b, &got))
	require.Equal(t, id.String(), got["id"])
	require.Equal(t, pk.Hex(), got["local_pk"])
	require.Equal(t, remote.Hex(), got["remote_pk"])
	require.Equal(t, "stcpr", got["type"])
}

// A compacted entry must not pay for the identifiers it doesn't carry.
// omitempty cannot do this on its own (uuid.UUID and cipher.PubKey are
// arrays), so a regression here silently costs ~170 bytes per transport
// on every node-list poll.
func TestTransportSummaryMarshalJSONOmitsZeroIdentifiers(t *testing.T) {
	b, err := json.Marshal(TransportSummary{Type: types.Type("dmsg"), Initiator: true})
	require.NoError(t, err)

	s := string(b)
	require.NotContains(t, s, `"id"`)
	require.NotContains(t, s, `"local_pk"`)
	require.NotContains(t, s, `"remote_pk"`)
	require.Contains(t, s, `"type":"dmsg"`)
	require.Contains(t, s, `"initiator":true`)
	require.Less(t, len(b), 80, "compacted entry should stay tiny: %s", s)
}

func TestCompactTransportSummariesKeepsOnlyTypeAndDirection(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	in := []*TransportSummary{{
		ID:            uuid.New(),
		Local:         pk,
		Remote:        pk,
		Type:          types.Type("sudph"),
		Initiator:     true,
		LatencyMS:     42,
		ThroughputBps: 1234,
		RemoteIP:      "1.2.3.4",
	}, nil}

	out := compactTransportSummaries(in)
	require.Len(t, out, 2)
	require.Nil(t, out[1])
	require.Equal(t, types.Type("sudph"), out[0].Type)
	require.True(t, out[0].Initiator)
	require.Zero(t, out[0].ID)
	require.True(t, out[0].Local.Null())
	require.True(t, out[0].Remote.Null())
	require.Zero(t, out[0].LatencyMS)
	require.Nil(t, out[0].Endpoint)

	// The input — which may be the hypervisor's cached Overview — is
	// left alone.
	require.Equal(t, types.Type("sudph"), in[0].Type)
	require.False(t, in[0].Local.Null())
	require.Equal(t, float64(42), in[0].LatencyMS)
}

// compactSummaryTransports must copy the Overview it rewrites:
// hv.summaryCache hands the same *Overview to the per-visor detail
// endpoints, which do need the full transport records.
func TestCompactSummaryTransportsDoesNotMutateSharedOverview(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	shared := &Overview{
		PubKey:     pk,
		Transports: []*TransportSummary{{ID: uuid.New(), Local: pk, Remote: pk, Type: types.Type("dmsg")}},
	}

	out := compactSummaryTransports([]Summary{{Overview: shared}})
	require.Len(t, out, 1)
	require.NotSame(t, shared, out[0].Overview)
	require.True(t, out[0].Overview.Transports[0].Local.Null())
	require.False(t, shared.Transports[0].Local.Null())
}
