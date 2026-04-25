package store

import (
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
)

func TestEdgeEntriesCache_HitMissExpiry(t *testing.T) {
	c := newEdgeEntriesCache(8, 50*time.Millisecond)

	pk, _ := cipher.GenerateKeyPair()
	entries := []*transport.Entry{{}}

	if _, ok := c.Get(pk); ok {
		t.Fatal("expected miss on empty cache")
	}

	c.Put(pk, entries)
	got, ok := c.Get(pk)
	if !ok {
		t.Fatal("expected hit immediately after Put")
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(got))
	}

	time.Sleep(60 * time.Millisecond)
	if _, ok := c.Get(pk); ok {
		t.Fatal("expected miss after TTL expiry")
	}
}

func TestEdgeEntriesCache_Invalidate(t *testing.T) {
	c := newEdgeEntriesCache(8, time.Hour)

	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	entries := []*transport.Entry{{}}

	c.Put(pk1, entries)
	c.Put(pk2, entries)

	c.Invalidate(pk1)
	if _, ok := c.Get(pk1); ok {
		t.Fatal("expected miss after Invalidate")
	}
	if _, ok := c.Get(pk2); !ok {
		t.Fatal("Invalidate of pk1 should not affect pk2")
	}

	// variadic form invalidates all listed
	c.Invalidate(pk1, pk2)
	if _, ok := c.Get(pk2); ok {
		t.Fatal("expected miss after variadic Invalidate")
	}
}

func TestEdgeEntriesCache_CapEviction(t *testing.T) {
	c := newEdgeEntriesCache(2, time.Hour)

	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	pk3, _ := cipher.GenerateKeyPair()
	entries := []*transport.Entry{{}}

	c.Put(pk1, entries)
	c.Put(pk2, entries)
	c.Put(pk3, entries) // forces random eviction of one of pk1/pk2

	if len(c.items) > 2 {
		t.Fatalf("cache exceeded capacity: %d", len(c.items))
	}
}
