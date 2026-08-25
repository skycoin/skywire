package store

import (
	"testing"
	"time"

	types "github.com/skycoin/skywire/pkg/transport/types"
)

func TestGetAllCache_HitMissExpiry(t *testing.T) {
	c := newGetAllCache(50 * time.Millisecond)

	if _, ok := c.Get(types.STCPR); ok {
		t.Fatal("expected miss on empty cache (STCPR)")
	}

	stcprPKs := []string{"a", "b"}
	sudphPKs := []string{"c"}

	c.Put(types.STCPR, stcprPKs)
	c.Put(types.SUDPH, sudphPKs)

	if got, ok := c.Get(types.STCPR); !ok || len(got) != 2 {
		t.Fatalf("expected hit with 2 entries for STCPR, got ok=%v len=%d", ok, len(got))
	}
	if got, ok := c.Get(types.SUDPH); !ok || len(got) != 1 {
		t.Fatalf("expected hit with 1 entry for SUDPH, got ok=%v len=%d", ok, len(got))
	}

	time.Sleep(60 * time.Millisecond)
	if _, ok := c.Get(types.STCPR); ok {
		t.Fatal("expected miss after TTL expiry (STCPR)")
	}
	if _, ok := c.Get(types.SUDPH); ok {
		t.Fatal("expected miss after TTL expiry (SUDPH)")
	}
}

func TestGetAllCache_SlotsIndependent(t *testing.T) {
	c := newGetAllCache(time.Hour)
	c.Put(types.STCPR, []string{"a"})

	if _, ok := c.Get(types.SUDPH); ok {
		t.Fatal("Put(STCPR) must not populate Get(SUDPH) slot")
	}
	if _, ok := c.Get(types.STCPR); !ok {
		t.Fatal("Put(STCPR) must populate Get(STCPR) slot")
	}
}

func TestGetAllCache_UnknownNetType(t *testing.T) {
	c := newGetAllCache(time.Hour)
	if _, ok := c.Get(types.Type("bogus")); ok {
		t.Fatal("unknown netType should always miss")
	}
	c.Put(types.Type("bogus"), []string{"a"}) // no-op
	if _, ok := c.Get(types.Type("bogus")); ok {
		t.Fatal("Put on unknown netType should be a no-op")
	}
}
