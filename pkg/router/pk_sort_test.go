// pkg/router/pk_sort_test.go — pkLess must order public keys identically to the
// pk.String() comparator it replaces on the route-BFS sort hot paths, at a
// fraction of the allocation.

package router

import (
	"math/rand"
	"sort"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

func randPubKeysSeeded(rng *rand.Rand, n int) []cipher.PubKey {
	pks := make([]cipher.PubKey, n)
	for i := range pks {
		rng.Read(pks[i][:])
	}
	return pks
}

// TestPkLessMatchesStringOrder: for every pair, pkLess agrees with the
// pk.String() (Hex) comparison it replaces — hex preserves byte order, so a
// byte-sorted slice equals a String-sorted slice.
func TestPkLessMatchesStringOrder(t *testing.T) {
	rng := rand.New(rand.NewSource(0xBEEF)) //nolint:gosec // reproducible fixture
	pks := randPubKeysSeeded(rng, 500)
	for i := range pks {
		for j := range pks {
			if got, want := pkLess(pks[i], pks[j]), pks[i].String() < pks[j].String(); got != want {
				t.Fatalf("pkLess disagrees with String() order for %s vs %s: got %v want %v",
					pks[i], pks[j], got, want)
			}
		}
	}

	// A full sort by each key must yield the identical sequence.
	byBytes := append([]cipher.PubKey(nil), pks...)
	byString := append([]cipher.PubKey(nil), pks...)
	sort.SliceStable(byBytes, func(i, j int) bool { return pkLess(byBytes[i], byBytes[j]) })
	sort.SliceStable(byString, func(i, j int) bool { return byString[i].String() < byString[j].String() })
	for i := range byBytes {
		if byBytes[i] != byString[i] {
			t.Fatalf("sorted order diverges at %d: bytes=%s string=%s", i, byBytes[i], byString[i])
		}
	}
}

func benchSortPKs(b *testing.B, less func(a, b cipher.PubKey) bool) {
	rng := rand.New(rand.NewSource(1)) //nolint:gosec
	base := randPubKeysSeeded(rng, 64) // a per-expansion children set size
	scratch := make([]cipher.PubKey, len(base))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copy(scratch, base)
		sort.SliceStable(scratch, func(x, y int) bool { return less(scratch[x], scratch[y]) })
	}
}

// BenchmarkPKSortString is the previous comparator; BenchmarkPKSortBytes is
// pkLess. The allocs/op delta is the per-sort churn removed from the BFS.
func BenchmarkPKSortString(b *testing.B) {
	benchSortPKs(b, func(a, c cipher.PubKey) bool { return a.String() < c.String() })
}

func BenchmarkPKSortBytes(b *testing.B) {
	benchSortPKs(b, pkLess)
}
