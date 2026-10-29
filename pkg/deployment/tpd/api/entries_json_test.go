package api

import (
	"encoding/json"
	"math"
	"math/rand"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

func randEntries(r *rand.Rand, n int) []*transport.Entry {
	floats := []float64{0, math.Copysign(0, -1), 1, 0.5, 12.345678, 1e-7, 3e-9, 1e21, 9.99e20, 1e300, 5e-324, -2.5, 123456789.125, float64(r.Uint32()) / 7}
	strs := []string{"", "stcpr", "dmsg", "automatic", "user", "a<b", "x&y", "q\"t", "back\\slash", "tab\t", "ünï", " ", "\xff"}
	out := make([]*transport.Entry, n)
	for i := range out {
		if r.Intn(20) == 0 {
			continue // nil element
		}
		a, _ := cipher.GenerateKeyPair()
		bk, _ := cipher.GenerateKeyPair()
		out[i] = &transport.Entry{
			ID:            uuid.New(),
			Edges:         [2]cipher.PubKey{a, bk},
			Type:          types.Type(strs[r.Intn(len(strs))]),
			Label:         transport.Label(strs[r.Intn(len(strs))]),
			Latency:       floats[r.Intn(len(floats))] * r.Float64(),
			Bandwidth:     r.Uint64() >> uint(r.Intn(64)),
			ThroughputBps: floats[r.Intn(len(floats))],
		}
		if r.Intn(10) == 0 {
			out[i].Edges[1] = cipher.PubKey{}
		}
	}
	return out
}

// marshalEntries must match json.Marshal byte for byte.
func TestMarshalEntriesMatchesJSON(t *testing.T) {
	r := rand.New(rand.NewSource(1)) //nolint:gosec
	for _, es := range [][]*transport.Entry{nil, {}, {nil}} {
		want, err := json.Marshal(es)
		require.NoError(t, err)
		got, err := marshalEntries(es)
		require.NoError(t, err)
		require.Equal(t, string(want), string(got))
	}
	for i := 0; i < 300; i++ {
		es := randEntries(r, r.Intn(20))
		want, err := json.Marshal(es)
		require.NoError(t, err)
		got, err := marshalEntries(es)
		require.NoError(t, err)
		require.Equal(t, string(want), string(got))
	}
	for _, f := range []float64{math.NaN(), math.Inf(1)} {
		es := []*transport.Entry{{Latency: f}}
		_, werr := json.Marshal(es)
		_, gerr := marshalEntries(es)
		require.Error(t, werr)
		require.Error(t, gerr)
	}
}

func BenchmarkMarshalEdgeEntries(b *testing.B) {
	r := rand.New(rand.NewSource(2)) //nolint:gosec
	es := make([]*transport.Entry, 400)
	for i := range es {
		a, _ := cipher.GenerateKeyPair()
		bk, _ := cipher.GenerateKeyPair()
		es[i] = &transport.Entry{ID: uuid.New(), Edges: [2]cipher.PubKey{a, bk}, Type: "stcpr", Label: "automatic",
			Latency: r.Float64() * 300, Bandwidth: r.Uint64() >> 30, ThroughputBps: float64(r.Intn(1 << 24))}
	}
	b.Run("json", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = json.Marshal(es) //nolint:errcheck
		}
	})
	b.Run("append", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = marshalEntries(es) //nolint:errcheck
		}
	})
}
