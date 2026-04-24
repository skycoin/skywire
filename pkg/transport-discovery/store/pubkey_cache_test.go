package store

import (
	"testing"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

func TestPubKeyCache_ParseHitsReturnEqualValue(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	hex := pk.Hex()

	c := newPubKeyCache(16)

	first, err := c.Parse(hex)
	if err != nil {
		t.Fatalf("first parse: %v", err)
	}
	if first != pk {
		t.Fatalf("first parse mismatch: got %s want %s", first.Hex(), hex)
	}

	// Confirm hit by checking the map was populated.
	c.mu.RLock()
	_, cached := c.items[hex]
	c.mu.RUnlock()
	if !cached {
		t.Fatalf("expected pubkey to be cached after first Parse")
	}

	second, err := c.Parse(hex)
	if err != nil {
		t.Fatalf("second parse: %v", err)
	}
	if second != first {
		t.Fatalf("second parse returned different value")
	}
}

func TestPubKeyCache_MalformedHexNotCached(t *testing.T) {
	c := newPubKeyCache(16)
	bad := "not-a-hex-pubkey"

	if _, err := c.Parse(bad); err == nil {
		t.Fatalf("expected error for malformed hex")
	}

	c.mu.RLock()
	_, cached := c.items[bad]
	c.mu.RUnlock()
	if cached {
		t.Fatalf("malformed hex should not be cached")
	}

	// A second malformed Parse must still fail (not silently succeed).
	if _, err := c.Parse(bad); err == nil {
		t.Fatalf("expected error on repeat malformed parse")
	}
}

func TestPubKeyCache_EvictsWhenFull(t *testing.T) {
	c := newPubKeyCache(2)

	for i := 0; i < 3; i++ {
		pk, _ := cipher.GenerateKeyPair()
		if _, err := c.Parse(pk.Hex()); err != nil {
			t.Fatalf("parse %d: %v", i, err)
		}
	}

	c.mu.RLock()
	size := len(c.items)
	c.mu.RUnlock()
	if size > 2 {
		t.Fatalf("cache exceeded cap: size=%d cap=2", size)
	}
}

func BenchmarkPubKeyCache_Parse(b *testing.B) {
	pk, _ := cipher.GenerateKeyPair()
	hex := pk.Hex()
	c := newPubKeyCache(16)
	// Warm.
	if _, err := c.Parse(hex); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.Parse(hex); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPubKeyCache_Uncached(b *testing.B) {
	pk, _ := cipher.GenerateKeyPair()
	hex := []byte(pk.Hex())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var out cipher.PubKey
		if err := out.UnmarshalText(hex); err != nil {
			b.Fatal(err)
		}
	}
}
