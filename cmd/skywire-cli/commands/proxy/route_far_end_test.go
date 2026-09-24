// Package skysocksc cmd/skywire-cli/commands/proxy/route_far_end_test.go c4-vis-cli
//
// `proxy status` must name the EXIT, never this visor. A dialed route group's
// descriptor is in the initiator orientation — the setup node hands each edge
// the descriptor pointing AT that edge, so Dst is the LOCAL visor — and the
// renderer used to print Desc.DstPK, i.e. the local key, as the route's
// destination.
package skysocksc

import (
	"strings"
	"testing"
)

const (
	localHexPK = "0323272a4a8d1b2f0f2b4c68d1f9a7b3c5e7091b2d4f6a8c0e2f4a6b8d0c1e3f55"
	exitHexPK  = "02deadbeef1b2f0f2b4c68d1f9a7b3c5e7091b2d4f6a8c0e2f4a6b8d0c1e3f5507"
	nullHexPK  = "000000000000000000000000000000000000000000000000000000000000000000"
)

// dialedRG is one route group as a DIALING client sees it: the local visor is
// the descriptor's Dst, the exit its Src, and dst_port is the group's own port
// handle (what `proxy mux set --rg` takes).
func dialedRG(farEnd string) muxRouteGroupInfo {
	rg := muxRouteGroupInfo{MuxEnabled: true}
	rg.Desc.SrcPK = exitHexPK
	rg.Desc.SrcPort = 3
	rg.Desc.DstPK = localHexPK
	rg.Desc.DstPort = 49153
	rg.FarEndPK = farEnd
	rg.Legs = []muxLegInfo{{Index: 0, TpType: "stcpr", RemotePK: exitHexPK}}
	return rg
}

func TestRenderProxyRouteNamesTheExitNotTheLocalVisor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		farEnd string
	}{
		{"far_end_pk from the visor", exitHexPK},
		{"visor sent no far_end_pk", ""},
		{"visor sent a null far_end_pk", nullHexPK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := renderProxyRoute([]muxRouteGroupInfo{dialedRG(tc.farEnd)}, nil)
			if !strings.Contains(out, shortPK(exitHexPK)) {
				t.Fatalf("the exit %s is missing from the route line: %q", shortPK(exitHexPK), out)
			}
			if strings.Contains(out, shortPK(localHexPK)) {
				t.Fatalf("the LOCAL visor %s was printed as the route's far end: %q", shortPK(localHexPK), out)
			}
			// The group's own port handle is the one `proxy mux set --rg` takes,
			// so it must still be printed.
			if !strings.Contains(out, "49153") {
				t.Fatalf("the rg port handle 49153 is missing: %q", out)
			}
		})
	}
}

// The status tree's tunnel carries the same exit.
func TestSnapshotFromGroupsExitIsTheFarEnd(t *testing.T) {
	rg := treeRouteGroup{MuxEnabled: true}
	rg.Desc.SrcPK = exitHexPK
	rg.Desc.DstPK = localHexPK
	rg.FarEndPK = exitHexPK
	snap := snapshotFromGroups([]treeRouteGroup{rg})
	if len(snap.Tunnels) != 1 {
		t.Fatalf("want 1 tunnel, got %d", len(snap.Tunnels))
	}
	if got := snap.Tunnels[0].ExitPK; got != exitHexPK {
		t.Fatalf("exit_pk = %s, want the far end %s", got, exitHexPK)
	}
}
