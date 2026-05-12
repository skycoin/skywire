package noise

import (
	"bytes"
	"testing"

	secp256k1 "github.com/skycoin/skycoin/src/cipher/secp256k1-go"
)

// TestDHCacheConsistency verifies the cache returns the same bytes
// as a fresh compute for the same (sk, pk) inputs, and that
// different (sk, pk) pairs produce different outputs. Existing
// noise-handshake tests cover the integration; this is a unit
// check on the cache layer alone.
func TestDHCacheConsistency(t *testing.T) {
	resetDHCache()

	pk1, sk1 := secp256k1.GenerateKeyPair()
	pk2, sk2 := secp256k1.GenerateKeyPair()

	dh := Secp256k1{}

	first := dh.DH(sk1, pk2)  // miss → compute + cache
	second := dh.DH(sk1, pk2) // hit
	if !bytes.Equal(first, second) {
		t.Fatalf("cache hit returned different bytes: %x vs %x", first, second)
	}

	// DH(sk1, pk2) == DH(sk2, pk1) by the commutative property of
	// Diffie-Hellman, so we use a *third* unrelated keypair to
	// generate a cache-lookup key with a guaranteed-different DH
	// output.
	pk3, _ := secp256k1.GenerateKeyPair()
	other := dh.DH(sk1, pk3)
	if bytes.Equal(first, other) {
		t.Fatalf("unrelated (sk,pk) pairs produced the same DH output: %x", first)
	}

	// Confirm a new compute with the same inputs but the cache
	// cleared still matches the cached value (cache is a no-op
	// optimization, not a divergence source).
	resetDHCache()
	fresh := dh.DH(sk1, pk2)
	if !bytes.Equal(first, fresh) {
		t.Fatalf("post-reset compute disagrees with cached value: %x vs %x", fresh, first)
	}

	// Sanity check: DH is commutative (each party should derive the
	// same shared secret from their own SK + the peer's PK).
	commutative := dh.DH(sk2, pk1)
	if !bytes.Equal(first, commutative) {
		t.Fatalf("DH not commutative: DH(sk1, pk2)=%x DH(sk2, pk1)=%x", first, commutative)
	}
}

// TestDHCacheEviction verifies the cache stays bounded under a
// stream of unique inputs (the ephemeral-DH miss pattern).
func TestDHCacheEviction(t *testing.T) {
	resetDHCache()

	dh := Secp256k1{}
	// Push 2× the cap of unique pairs and confirm size never grows
	// past the cap.
	for i := 0; i < dhCacheMax*2; i++ {
		pk, sk := secp256k1.GenerateKeyPair()
		_ = dh.DH(sk, pk)
		if size := dhCacheSize(); size > dhCacheMax {
			t.Fatalf("cache exceeded cap: %d > %d at iteration %d", size, dhCacheMax, i)
		}
	}
}

// resetDHCache wipes the package-level cache for test isolation.
func resetDHCache() {
	dhCacheMu.Lock()
	dhCache = make(map[dhCacheKey][33]byte, dhCacheMax)
	dhCacheMu.Unlock()
}

// dhCacheSize returns the current entry count under the lock.
func dhCacheSize() int {
	dhCacheMu.RLock()
	defer dhCacheMu.RUnlock()
	return len(dhCache)
}

// BenchmarkDHCacheHit measures the cost of a cached lookup (the
// hot-path expected outcome for the ss DH step on repeat
// handshakes). Compare against BenchmarkDHCacheMiss to see the
// raw secp256k1 ECDH cost we're skipping.
func BenchmarkDHCacheHit(b *testing.B) {
	resetDHCache()
	dh := Secp256k1{}
	pk, sk := secp256k1.GenerateKeyPair()
	_ = dh.DH(sk, pk) // prime the cache
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = dh.DH(sk, pk)
	}
}

// BenchmarkDHCacheMiss measures the cost of an uncached compute
// (fresh keypair on every iter so the cache never helps). This is
// what every DH call cost before the cache, and what every
// ephemeral-DH call still costs.
func BenchmarkDHCacheMiss(b *testing.B) {
	dh := Secp256k1{}
	// Pre-generate so the keypair pool fill doesn't dominate the
	// benchmark.
	keys := make([][2][]byte, b.N)
	for i := range keys {
		pk, sk := secp256k1.GenerateKeyPair()
		keys[i] = [2][]byte{sk, pk}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = dh.DH(keys[i][0], keys[i][1])
	}
}
