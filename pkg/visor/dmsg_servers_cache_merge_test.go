// Package visor pkg/visor/dmsg_servers_cache_merge_test.go c3-vis-core
package visor

import (
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
)

func cachedServerEntry(t *testing.T, hexPK, addr string) *dmsgdisc.Entry {
	t.Helper()
	var pk cipher.PubKey
	if err := pk.UnmarshalText([]byte(hexPK)); err != nil {
		t.Fatalf("parse pk %s: %v", hexPK, err)
	}
	return &dmsgdisc.Entry{Static: pk, Server: &dmsgdisc.Server{Address: addr}}
}

// A dmsg server that rotates its IP leaves the address in skywire.json
// stale. dmsgc.New pre-seeds those configured entries straight into the
// client's entry cache and resolveServerEntry answers from that cache
// before consulting discovery, so a stale address is dialed for the whole
// process lifetime — and a restart re-seeds it. MergePreferringCache is
// what keeps the learned address winning; assert that directly.
func TestMergePreferringCacheOverridesStaleConfiguredAddress(t *testing.T) {
	const (
		rotated = "03717576ada5b1744e395c66c2bb11cea73b0e23d0dcd54422139b1a7f12e962c4"
		stable  = "0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"
		unseen  = "02a49bc0aa1b5b78f638e9189be4ed095bac5d6839c828465a8350f80ac07629c0"
	)

	c := NewDmsgServersCache("")
	if err := c.Replace([]*dmsgdisc.Entry{
		cachedServerEntry(t, rotated, "172.105.110.46:30083"),
		cachedServerEntry(t, unseen, "143.42.59.213:30088"),
	}); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	got := c.MergePreferringCache([]*dmsgdisc.Entry{
		cachedServerEntry(t, rotated, "139.162.173.101:30083"), // dead host
		cachedServerEntry(t, stable, "139.162.160.227:30086"),
	})

	addrs := make(map[string]string, len(got))
	for _, e := range got {
		if e == nil || e.Server == nil {
			t.Fatalf("nil entry or server section in merge output")
		}
		addrs[e.Static.String()] = e.Server.Address
	}

	if want := "172.105.110.46:30083"; addrs[rotated] != want {
		t.Errorf("rotated server: got %q, want the cached %q", addrs[rotated], want)
	}
	if want := "139.162.160.227:30086"; addrs[stable] != want {
		t.Errorf("configured-only server: got %q, want %q", addrs[stable], want)
	}
	if want := "143.42.59.213:30088"; addrs[unseen] != want {
		t.Errorf("cache-only server: got %q, want %q", addrs[unseen], want)
	}
	if len(got) != 3 {
		t.Errorf("merge returned %d entries, want 3 (no drops, no duplicates)", len(got))
	}
}
