package routingfeed

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

func gz(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return cxoutils.Gzip(b)
}

// Deltas apply in sequence order whatever order the leaves come in, a
// removal drops a transport, the t_id is restored, and a leaf that does not
// decode is skipped.
func TestReader(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	c, _ := cipher.GenerateKeyPair()
	ab, ac := transport.SortEdges(a, b), transport.SortEdges(a, c)
	shard := []map[string]any{
		{"edges": ab, "type": tptypes.SUDPH, "latency_ms": 12},
		{"edges": ac, "type": tptypes.STCPR, "latency_ms": 40},
	}
	r := NewReader()
	r.Add(DeltaPath(2), gz(t, map[string]any{"c": []map[string]any{{"edges": ab, "type": tptypes.SUDPH, "latency_ms": 30}}}))
	r.Add(Prefix+a.Hex(), gz(t, shard))
	r.Add(DeltaPath(1), gz(t, map[string]any{
		"c": []map[string]any{{"edges": ab, "type": tptypes.SUDPH, "latency_ms": 20}},
		"r": []map[string]any{{"edges": [2]cipher.PubKey{ac[1], ac[0]}, "type": tptypes.STCPR}},
	}))
	r.Add(Prefix+"junk", []byte("not json"))
	r.Add(DeltaPrefix+"x", gz(t, map[string]any{}))

	got := r.Entries()
	require.Len(t, got, 1)
	require.Equal(t, 30.0, got[0].Latency, "delta 2 applies after delta 1")
	require.Equal(t, transport.MakeTransportID(ab[0], ab[1], tptypes.SUDPH), got[0].ID)
}
