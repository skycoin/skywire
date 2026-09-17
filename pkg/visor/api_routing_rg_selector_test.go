// Package visor pkg/visor/api_routing_rg_selector_test.go
package visor

import (
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
)

// TestSelectRouteDescByPort: every route group a skysocks-client instance
// dials to one exit carries the SAME src_port (the app's port, 3) — only
// dst_port is per-group. The --rg selector must therefore name a group by
// the port 'mux info' shows for it (desc.dst_port), or `proxy start
// --tunnels N` has no way to address one tunnel.
func TestSelectRouteDescByPort(t *testing.T) {
	srcPK, _ := cipher.GenerateKeyPair()
	dstPK, _ := cipher.GenerateKeyPair()
	const appPort = 3
	infos := []router.MuxInfo{
		{Desc: routing.NewRouteDescriptor(srcPK, dstPK, appPort, 49166)},
		{Desc: routing.NewRouteDescriptor(srcPK, dstPK, appPort, 49168)},
	}

	for _, want := range []routing.Port{49166, 49168} {
		got, err := selectRouteDesc(infos, "skysocks-client", uint16(want))
		if err != nil {
			t.Fatalf("selectRouteDesc(dst_port=%d): %v", want, err)
		}
		if got.DstPort() != want {
			t.Errorf("selectRouteDesc(%d) picked dst_port=%d, want %d", want, got.DstPort(), want)
		}
	}

	// 0 is still ambiguous with >1 group, and the error must list the
	// dst_ports the caller is meant to pass back as --rg.
	_, err := selectRouteDesc(infos, "skysocks-client", 0)
	if err == nil {
		t.Fatal("selectRouteDesc(0) with 2 groups: want an ambiguity error, got nil")
	}
	for _, frag := range []string{"2 active rg's", "dst_port=49166", "dst_port=49168"} {
		if !strings.Contains(err.Error(), frag) {
			t.Errorf("ambiguity error %q missing %q", err, frag)
		}
	}

	// src_port still selects when the caller has only the one group (the
	// pre-existing behaviour), and 0 auto-picks it.
	one := infos[:1]
	if got, err := selectRouteDesc(one, "skysocks-client", appPort); err != nil || got.DstPort() != 49166 {
		t.Errorf("selectRouteDesc(src_port=%d) = (%v, %v), want the only group", appPort, got.DstPort(), err)
	}
	if got, err := selectRouteDesc(one, "skysocks-client", 0); err != nil || got.DstPort() != 49166 {
		t.Errorf("selectRouteDesc(0) with 1 group = (%v, %v), want the only group", got.DstPort(), err)
	}

	// An unknown port matches neither end.
	if _, err := selectRouteDesc(infos, "skysocks-client", 1234); err == nil {
		t.Error("selectRouteDesc(1234): want an error, got nil")
	}
}
