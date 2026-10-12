package transport

import (
	"math/big"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

// SortEdges orders keys exactly as the big.Int comparison it replaced, so
// transport IDs and edge order do not change.
func TestSortEdgesMatchesNumericOrder(t *testing.T) {
	for i := 0; i < 2000; i++ {
		a, _ := cipher.GenerateKeyPair()
		b, _ := cipher.GenerateKeyPair()
		var x, y big.Int
		want := [2]cipher.PubKey{b, a}
		if x.SetBytes(a[:]).Cmp(y.SetBytes(b[:])) < 0 {
			want = [2]cipher.PubKey{a, b}
		}
		if got := SortEdges(a, b); got != want {
			t.Fatalf("SortEdges(%s, %s) = %v, want %v", a, b, got, want)
		}
	}
	a, _ := cipher.GenerateKeyPair()
	if got := SortEdges(a, a); got != [2]cipher.PubKey{a, a} {
		t.Fatal("equal keys")
	}
}

func BenchmarkSortEdges(b *testing.B) {
	x, _ := cipher.GenerateKeyPair()
	y, _ := cipher.GenerateKeyPair()
	b.ReportAllocs()
	for b.Loop() {
		SortEdges(x, y)
	}
}
