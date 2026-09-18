// Package skysocksc cmd/skywire-cli/commands/proxy/mux_rg_select_test.go
package skysocksc

import (
	"strings"
	"testing"
)

// mkRG builds a route-group snapshot the way the visor reports one: the app's
// fixed src_port (3 for skysocks-client) plus the group's own ephemeral
// dst_port — the value `mux info` prints as the rg's port.
func mkRG(dstPort int, tp string) muxRouteGroupInfo {
	var rg muxRouteGroupInfo
	rg.Desc.SrcPort = 3
	rg.Desc.DstPort = dstPort
	rg.Legs = []muxLegInfo{{Index: 0, TransportID: tp}}
	return rg
}

// TestSelectAutoRGByDstPort: with `proxy start --tunnels N` the app holds N
// route groups that all share src_port=3, so --rg must select by the group's
// own dst_port. 0 stays ambiguous while more than one group is up.
func TestSelectAutoRGByDstPort(t *testing.T) {
	const (
		tpA = "11111111-1111-1111-1111-111111111111"
		tpB = "22222222-2222-2222-2222-222222222222"
	)
	rgs := []muxRouteGroupInfo{mkRG(49166, tpA), mkRG(49168, tpB)}

	for _, c := range []struct {
		port int
		tp   string
	}{{49166, tpA}, {49168, tpB}} {
		got, err := selectAutoRG(rgs, "skysocks-client", uint16(c.port)) //nolint:gosec
		if err != nil {
			t.Fatalf("selectAutoRG(--rg %d): %v", c.port, err)
		}
		if got.Desc.DstPort != c.port || got.Legs[0].TransportID != c.tp {
			t.Errorf("selectAutoRG(--rg %d) picked dst_port=%d tp=%s, want %d/%s",
				c.port, got.Desc.DstPort, got.Legs[0].TransportID, c.port, c.tp)
		}
	}

	if _, err := selectAutoRG(rgs, "skysocks-client", 0); err == nil {
		t.Error("selectAutoRG(0) with 2 groups: want an ambiguity error, got nil")
	} else if !strings.Contains(err.Error(), "2 active route groups") {
		t.Errorf("ambiguity error %q should say how many groups are up", err)
	}

	// currentLegTpIDs rides the same selector, so `mux set --rg <dst_port>`
	// diffs against the right group's legs.
	ids, err := currentLegTpIDs(rgs, "skysocks-client", 49168)
	if err != nil {
		t.Fatalf("currentLegTpIDs(--rg 49168): %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("currentLegTpIDs returned %d legs, want 1", len(ids))
	}
	for id := range ids {
		if id.String() != tpB {
			t.Errorf("currentLegTpIDs(--rg 49168) = %s, want %s", id, tpB)
		}
	}

	// One group: src_port and 0 both still resolve it (unchanged behavior).
	one := rgs[:1]
	if got, err := selectAutoRG(one, "skysocks-client", 3); err != nil || got.Desc.DstPort != 49166 {
		t.Errorf("selectAutoRG(--rg 3) = (%d, %v), want the only group", got.Desc.DstPort, err)
	}
	if got, err := selectAutoRG(one, "skysocks-client", 0); err != nil || got.Desc.DstPort != 49166 {
		t.Errorf("selectAutoRG(0) with 1 group = (%d, %v), want the only group", got.Desc.DstPort, err)
	}
	if _, err := selectAutoRG(nil, "skysocks-client", 0); err == nil {
		t.Error("selectAutoRG with no groups: want an error, got nil")
	}
}
