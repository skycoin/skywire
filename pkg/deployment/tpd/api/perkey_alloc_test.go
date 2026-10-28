package api

import (
	"reflect"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/types"
)

func perKeyCountsHexPerEdge(entries []*transport.Entry) map[string]map[string]int {
	result := make(map[string]map[string]int)
	for _, entry := range entries {
		for _, edge := range entry.Edges {
			pkHex := edge.Hex()
			if result[pkHex] == nil {
				result[pkHex] = make(map[string]int)
			}
			result[pkHex][string(entry.Type)]++
			result[pkHex]["total"]++
		}
	}
	return result
}

func perKeyBenchEntries(keys, n int) []*transport.Entry {
	pks := make([]cipher.PubKey, keys)
	for i := range pks {
		pks[i], _ = cipher.GenerateKeyPair()
	}
	out := make([]*transport.Entry, n)
	for i := range out {
		t := tptypes.Type("stcpr")
		if i%3 == 0 {
			t = "sudph"
		}
		e := transport.MakeEntry(pks[i%keys], pks[(i*7+1)%keys], t, transport.LabelAutomatic)
		out[i] = &e
	}
	return out
}

func TestPerKeyCountsMatchesHexPerEdge(t *testing.T) {
	entries := perKeyBenchEntries(40, 500)
	if !reflect.DeepEqual(perKeyCounts(entries), perKeyCountsHexPerEdge(entries)) {
		t.Fatal("perKeyCounts differs from the per-edge reference")
	}
}

func BenchmarkPerKeyCounts(b *testing.B) {
	entries := perKeyBenchEntries(1500, 20000)
	b.Run("hex-per-edge", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			perKeyCountsHexPerEdge(entries)
		}
	})
	b.Run("hex-per-key", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			perKeyCounts(entries)
		}
	})
}
