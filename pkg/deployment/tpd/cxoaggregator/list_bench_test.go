package cxoaggregator

import (
	"encoding/json"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
)

func benchLeaf(n int) []byte {
	l := transportListLeaf{Version: "v"}
	for i := 0; i < n; i++ {
		pk, _ := cipher.GenerateKeyPair()
		l.Compact = append(l.Compact, transport.CompactEntry{Remote: pk, Type: "stcpr"})
	}
	b, err := json.Marshal(l)
	if err != nil {
		panic(err)
	}
	return b
}

func BenchmarkListDecodePlain(b *testing.B) {
	leaf := benchLeaf(3000)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var l transportListLeaf
		if err := json.Unmarshal(leaf, &l); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListDecodePresized(b *testing.B) {
	leaf := benchLeaf(3000)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var l transportListLeaf
		if err := decodeTransportList(leaf, &l); err != nil {
			b.Fatal(err)
		}
	}
}

func TestDecodeTransportListCount(t *testing.T) {
	for _, n := range []int{0, 1, 2, 50} {
		var l transportListLeaf
		if err := decodeTransportList(benchLeaf(n), &l); err != nil || len(l.Compact) != n {
			t.Fatalf("n=%d got %d err %v", n, len(l.Compact), err)
		}
	}
}
