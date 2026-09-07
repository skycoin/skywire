// Package cipher pkg/cipher/pubkey_verify_cache_test.go c1-cip-core
package cipher

import (
	"bytes"
	"encoding/gob"
	"testing"
)

// TestUnmarshalBinaryRejectsInvalid guards the fast path added for the gob/net-rpc
// decode: a cache hit must never let a malformed or off-curve key through.
func TestUnmarshalBinaryRejectsInvalid(t *testing.T) {
	pk, _ := GenerateKeyPair()

	// Prime the cache with a genuinely valid key.
	var round PubKey
	if err := round.UnmarshalBinary(pk[:]); err != nil {
		t.Fatalf("valid key rejected: %v", err)
	}
	if round != pk {
		t.Fatalf("round-trip mismatch: got %s want %s", round.Hex(), pk.Hex())
	}

	// A second decode of the same bytes takes the cached path and must agree.
	var cached PubKey
	if err := cached.UnmarshalBinary(pk[:]); err != nil {
		t.Fatalf("cached decode failed: %v", err)
	}
	if cached != pk {
		t.Fatalf("cached decode mismatch: got %s want %s", cached.Hex(), pk.Hex())
	}

	// Wrong length must still fail rather than silently truncating.
	var short PubKey
	if err := short.UnmarshalBinary(pk[:len(pk)-1]); err == nil {
		t.Fatal("short input accepted")
	}

	// Right length, not a curve point: flipping the leading prefix byte to a value
	// that is not 0x02/0x03 must be rejected by secp256k1, cache or no cache.
	bad := make([]byte, len(pk))
	copy(bad, pk[:])
	bad[0] = 0x07
	var offCurve PubKey
	if err := offCurve.UnmarshalBinary(bad); err == nil {
		t.Fatal("non-curve point accepted")
	}
}

// TestGobRoundTripPubKey exercises the actual path the fast path was added for.
func TestGobRoundTripPubKey(t *testing.T) {
	pk, _ := GenerateKeyPair()

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(&pk); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var out PubKey
	if err := gob.NewDecoder(&buf).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out != pk {
		t.Fatalf("gob round-trip mismatch: got %s want %s", out.Hex(), pk.Hex())
	}
}

// BenchmarkUnmarshalBinaryRepeat models the real gob/net-rpc shape: the same small
// set of fleet PKs decoded over and over out of every RPC summary response.
func BenchmarkUnmarshalBinaryRepeat(b *testing.B) {
	const fleet = 64
	keys := make([][]byte, fleet)
	for i := range keys {
		pk, _ := GenerateKeyPair()
		buf := make([]byte, len(pk))
		copy(buf, pk[:])
		keys[i] = buf
	}
	b.ResetTimer()
	var out PubKey
	for i := 0; i < b.N; i++ {
		if err := out.UnmarshalBinary(keys[i%fleet]); err != nil {
			b.Fatal(err)
		}
	}
}
