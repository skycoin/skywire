package store

import (
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/transport"
)

func TestAllTransportsCache_HitMissExpiry(t *testing.T) {
	c := newAllTransportsCache(50 * time.Millisecond)

	if _, ok := c.Get(true); ok {
		t.Fatal("expected miss on empty cache (selfTransports=true)")
	}
	if _, ok := c.Get(false); ok {
		t.Fatal("expected miss on empty cache (selfTransports=false)")
	}

	withSelf := []*transport.Entry{{}, {}}
	withoutSelf := []*transport.Entry{{}}

	c.Put(true, withSelf)
	c.Put(false, withoutSelf)

	if got, ok := c.Get(true); !ok || len(got) != 2 {
		t.Fatalf("expected hit with 2 entries for selfTransports=true, got ok=%v len=%d", ok, len(got))
	}
	if got, ok := c.Get(false); !ok || len(got) != 1 {
		t.Fatalf("expected hit with 1 entry for selfTransports=false, got ok=%v len=%d", ok, len(got))
	}

	time.Sleep(60 * time.Millisecond)
	if _, ok := c.Get(true); ok {
		t.Fatal("expected miss after TTL expiry (selfTransports=true)")
	}
	if _, ok := c.Get(false); ok {
		t.Fatal("expected miss after TTL expiry (selfTransports=false)")
	}
}

func TestAllTransportsCache_SlotsIndependent(t *testing.T) {
	c := newAllTransportsCache(time.Hour)
	c.Put(true, []*transport.Entry{{}})

	if _, ok := c.Get(false); ok {
		t.Fatal("Put(true) must not populate Get(false) slot")
	}
	if _, ok := c.Get(true); !ok {
		t.Fatal("Put(true) must populate Get(true) slot")
	}
}
