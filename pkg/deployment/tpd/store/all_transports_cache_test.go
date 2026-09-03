package store

import (
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/transport"
)

func TestAllTransportsCache_HitMissExpiry(t *testing.T) {
	c := newAllTransportsCache(50 * time.Millisecond)

	for _, withQoS := range []bool{false, true} {
		for _, selfTps := range []bool{false, true} {
			if _, ok := c.Get(selfTps, withQoS); ok {
				t.Fatalf("expected miss on empty cache (self=%v qos=%v)", selfTps, withQoS)
			}
		}
	}

	withSelf := []*transport.Entry{{}, {}}
	withoutSelf := []*transport.Entry{{}}
	withSelfQoS := []*transport.Entry{{}, {}, {}}
	withoutSelfQoS := []*transport.Entry{{}, {}, {}, {}}

	c.Put(true, false, withSelf)
	c.Put(false, false, withoutSelf)
	c.Put(true, true, withSelfQoS)
	c.Put(false, true, withoutSelfQoS)

	cases := []struct {
		self, qos bool
		want      int
	}{
		{true, false, 2},
		{false, false, 1},
		{true, true, 3},
		{false, true, 4},
	}
	for _, tc := range cases {
		got, ok := c.Get(tc.self, tc.qos)
		if !ok || len(got) != tc.want {
			t.Fatalf("self=%v qos=%v: expected hit with %d entries, got ok=%v len=%d",
				tc.self, tc.qos, tc.want, ok, len(got))
		}
	}

	time.Sleep(60 * time.Millisecond)
	for _, tc := range cases {
		if _, ok := c.Get(tc.self, tc.qos); ok {
			t.Fatalf("self=%v qos=%v: expected miss after TTL expiry", tc.self, tc.qos)
		}
	}
}

func TestAllTransportsCache_SlotsIndependent(t *testing.T) {
	c := newAllTransportsCache(time.Hour)
	c.Put(true, false, []*transport.Entry{{}})

	if _, ok := c.Get(false, false); ok {
		t.Fatal("Put(true,false) must not populate Get(false,false)")
	}
	if _, ok := c.Get(true, true); ok {
		t.Fatal("Put(true,false) must not populate Get(true,true) (different qos)")
	}
	if _, ok := c.Get(true, false); !ok {
		t.Fatal("Put(true,false) must populate Get(true,false)")
	}
}
