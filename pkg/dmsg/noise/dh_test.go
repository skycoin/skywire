package noise

import (
	"bytes"
	"testing"

	"github.com/skycoin/skycoin/src/cipher"
	secp256k1 "github.com/skycoin/skycoin/src/cipher/secp256k1-go"
)

// staticDH is the DH a handshake between these two static keys builds, so
// DH(sk, pk) on it is the cached static-static step.
func staticDH(sk, pk []byte) Secp256k1 { return Secp256k1{staticSK: sk, peerStatic: pk} }

// mustDH runs the static-static DH and fails the test/benchmark on error. Every call site
// here uses valid secp256k1 inputs, so an error is a real bug, not an expected
// path. Takes testing.TB so tests and benchmarks share it.
func mustDH(tb testing.TB, sk, pk []byte) []byte {
	tb.Helper()
	out, err := staticDH(sk, pk).DH(sk, pk)
	if err != nil {
		tb.Fatalf("DH: %v", err)
	}
	return out
}

// TestDHCacheConsistency verifies the cache returns the same bytes
// as a fresh compute for the same (sk, pk) inputs, and that
// different (sk, pk) pairs produce different outputs. Existing
// noise-handshake tests cover the integration; this is a unit
// check on the cache layer alone.
func TestDHCacheConsistency(t *testing.T) {
	resetDHCache()

	pk1, sk1 := secp256k1.GenerateKeyPair()
	pk2, sk2 := secp256k1.GenerateKeyPair()

	first := mustDH(t, sk1, pk2)  // miss → compute + cache
	second := mustDH(t, sk1, pk2) // hit
	if !bytes.Equal(first, second) {
		t.Fatalf("cache hit returned different bytes: %x vs %x", first, second)
	}

	// DH(sk1, pk2) == DH(sk2, pk1) by the commutative property of
	// Diffie-Hellman, so we use a *third* unrelated keypair to
	// generate a cache-lookup key with a guaranteed-different DH
	// output.
	pk3, _ := secp256k1.GenerateKeyPair()
	other := mustDH(t, sk1, pk3)
	if bytes.Equal(first, other) {
		t.Fatalf("unrelated (sk,pk) pairs produced the same DH output: %x", first)
	}

	// Confirm a new compute with the same inputs but the cache
	// cleared still matches the cached value (cache is a no-op
	// optimization, not a divergence source).
	resetDHCache()
	fresh := mustDH(t, sk1, pk2)
	if !bytes.Equal(first, fresh) {
		t.Fatalf("post-reset compute disagrees with cached value: %x vs %x", fresh, first)
	}

	// Sanity check: DH is commutative (each party should derive the
	// same shared secret from their own SK + the peer's PK).
	commutative := mustDH(t, sk2, pk1)
	if !bytes.Equal(first, commutative) {
		t.Fatalf("DH not commutative: DH(sk1, pk2)=%x DH(sk2, pk1)=%x", first, commutative)
	}
}

// TestDHCacheEviction verifies the cache stays bounded under a
// stream of unique inputs (the ephemeral-DH miss pattern).
func TestDHCacheEviction(t *testing.T) {
	resetDHCache()
	// Push 2× the cap of unique pairs and confirm size never grows
	// past the cap.
	for i := 0; i < dhCacheMax*2; i++ {
		pk, sk := secp256k1.GenerateKeyPair()
		mustDH(t, sk, pk)
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
	dhCacheHits.Store(0)
	dhCacheMisses.Store(0)
	dhCacheEvictions.Store(0)
}

// TestDHCacheStats validates the hits / misses / evictions counters
// exposed via GetCacheStats. Each unique (sk, pk) input is a miss
// on first call and a hit on subsequent calls; overflowing the
// cap triggers an eviction per overflow insertion.
func TestDHCacheStats(t *testing.T) {
	resetDHCache()
	pk1, sk1 := secp256k1.GenerateKeyPair()
	pk2, sk2 := secp256k1.GenerateKeyPair()

	// First call on each pair = miss.
	mustDH(t, sk1, pk2)
	mustDH(t, sk2, pk1)
	// Repeat — both hits.
	mustDH(t, sk1, pk2)
	mustDH(t, sk2, pk1)
	mustDH(t, sk1, pk2)

	s := GetCacheStats()
	if s.Hits != 3 {
		t.Fatalf("expected Hits=3, got %d", s.Hits)
	}
	if s.Misses != 2 {
		t.Fatalf("expected Misses=2, got %d", s.Misses)
	}
	if s.Size != 2 {
		t.Fatalf("expected Size=2, got %d", s.Size)
	}
	if s.Capacity != dhCacheMax {
		t.Fatalf("expected Capacity=%d, got %d", dhCacheMax, s.Capacity)
	}
	if s.Evictions != 0 {
		t.Fatalf("expected Evictions=0 (no overflow yet), got %d", s.Evictions)
	}

	// Push past the cap to trigger evictions.
	for i := 0; i < dhCacheMax*2; i++ {
		pk, sk := secp256k1.GenerateKeyPair()
		mustDH(t, sk, pk)
	}
	s = GetCacheStats()
	if s.Evictions == 0 {
		t.Fatalf("expected Evictions>0 after pushing 2x cap, got 0")
	}
	if s.Size > dhCacheMax {
		t.Fatalf("Size exceeded Capacity: %d > %d", s.Size, dhCacheMax)
	}
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
	pk, sk := secp256k1.GenerateKeyPair()
	dh := staticDH(sk, pk)
	mustDH(b, sk, pk) // prime the cache
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = dh.DH(sk, pk) //nolint:errcheck // hot loop; correctness covered by tests
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
		_, _ = dh.DH(keys[i][0], keys[i][1]) //nolint:errcheck // hot loop; correctness covered by tests
	}
}

// TestDHEphemeralSkipsCache checks that a DH which is not the handshake's
// static-static step is computed but never cached: those keys never repeat.
func TestDHEphemeralSkipsCache(t *testing.T) {
	resetDHCache()
	pkS, skS := secp256k1.GenerateKeyPair()
	pkP, _ := secp256k1.GenerateKeyPair()
	dh := staticDH(skS, pkP)

	pkE, skE := secp256k1.GenerateKeyPair()
	for _, in := range [][2][]byte{{skS, pkE}, {skE, pkP}, {skE, pkS}} {
		if _, err := dh.DH(in[0], in[1]); err != nil {
			t.Fatalf("DH: %v", err)
		}
	}
	if _, err := (Secp256k1{}).DH(skS, pkP); err != nil {
		t.Fatalf("DH: %v", err)
	}
	if s := GetCacheStats(); s.Size != 0 || s.Misses != 0 || s.Hits != 0 {
		t.Fatalf("ephemeral DHs touched the cache: %+v", s)
	}
	if _, err := dh.DH(skS, pkP); err != nil {
		t.Fatalf("DH: %v", err)
	}
	if s := GetCacheStats(); s.Size != 1 || s.Misses != 1 {
		t.Fatalf("static-static DH not cached: %+v", s)
	}
}

// TestDHMatchesCipherECDH pins the output to cipher.ECDH (padded to DHLen):
// peers on older builds derive their keys that way.
func TestDHMatchesCipherECDH(t *testing.T) {
	for i := 0; i < 50; i++ {
		pk1, _ := cipher.GenerateKeyPair()
		_, sk2 := cipher.GenerateKeyPair()
		want, err := cipher.ECDH(pk1, sk2)
		if err != nil {
			t.Fatal(err)
		}
		got, err := (Secp256k1{}).DH(sk2[:], pk1[:])
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 33 || !bytes.Equal(got[:32], want) || got[32] != 0 {
			t.Fatalf("DH %x, cipher.ECDH %x", got, want)
		}
	}
}

// TestDHRejectsInvalidPubKey checks that a point off the curve is refused,
// not multiplied.
func TestDHRejectsInvalidPubKey(t *testing.T) {
	_, sk := secp256k1.GenerateKeyPair()
	// About half of all x values have no point on the curve; find one.
	bad := make([]byte, 33)
	bad[0] = 2
	for bad[32] = 1; secp256k1.VerifyPubkey(bad) == 1; bad[32]++ {
	}
	if _, err := (Secp256k1{}).DH(sk, bad); err == nil {
		t.Fatal("DH accepted an invalid public key")
	}
}
