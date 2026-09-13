// Package proxystatus pkg/proxystatus/routetree_direct_test.go c3-vis-core
package proxystatus

import (
	"strings"
	"testing"

	"github.com/0magnet/bitree"

	"github.com/stretchr/testify/require"
)

const (
	directSelfPK = "0323272a60895f56aad82cb767fb5c413807adcf7c9fb0578b1b1c5807c7f29d4c"
	directExitPK = "0281a102c82820e811368c8d028cf11b1a985043b726b1bcdb8fce89b27384b2cb"
)

// directSnapshot is what the visor builds for a proxy whose dial took the
// AppDirectMux shortcut: one leg, no Hops, the remote IS the exit.
func directSnapshot(alive bool) Snapshot {
	leg := Leg{
		Index:       0,
		TransportID: "c63e3d2b-6041-0518-86d6-d321bf57fa57",
		TpType:      "stcpr",
		RemotePK:    directExitPK,
		Direct:      true,
		Alive:       alive,
		SentBytes:   54892,
		RecvBytes:   109380275,
	}
	return Snapshot{
		Surface: SurfaceSkysocks,
		App:     "skysocks-client",
		Running: true,
		SelfPK:  directSelfPK,
		Legs:    []Leg{leg},
		Tunnels: []Tunnel{{Index: 0, ExitPK: directExitPK, Legs: []Leg{leg}}},
	}
}

// TestRouteTree_DirectLegRendersTheExit: a 0-hop direct session must draw the
// exit, not just the local visor.
//
// hopClassMap skips every leg that is not Alive, and the visor's direct-stream
// legs were built without it — so status.skysocks drew a tree with the local PK
// at the top and NOTHING under it, on a proxy that was carrying 104 MB. The
// len(Hops)==0 branch below it was already correct; it was simply never reached.
func TestRouteTree_DirectLegRendersTheExit(t *testing.T) {
	tree := RouteTree(directSnapshot(true))
	require.NotNil(t, tree)

	rendered := renderTree(tree)
	require.Contains(t, rendered, directExitPK, "the exit PK must appear in the tree")

	roles := hopClassMap(directSnapshot(true))
	require.Equal(t, hopClass(true, 0), roles[directExitPK],
		"the remote of a hop-less leg IS the exit")
}

// The guard itself is still right: a dead leg must not be drawn as a live path.
func TestRouteTree_DeadDirectLegIsNotAnExit(t *testing.T) {
	roles := hopClassMap(directSnapshot(false))
	require.NotContains(t, roles, directExitPK, "a leg that is not alive must not mark an exit")
}

// renderTree flattens the node tree to text for substring assertions.
func renderTree(n *bitree.Node) string {
	if n == nil {
		return ""
	}
	var sb strings.Builder
	var walk func(*bitree.Node)
	walk = func(cur *bitree.Node) {
		if cur == nil {
			return
		}
		sb.WriteString(cur.Label)
		sb.WriteByte('\n')
		for _, c := range cur.Left {
			walk(c)
		}
		for _, c := range cur.Right {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

// TestRouteTree_DirectLegShowsTypeIDAndRTT: naming the exit is not enough —
// the tree must also say WHICH transport carries it and how fast.
//
// hopToNode renders Cols{[type], tpID, rtt}, but a leg with no Hops never
// reaches it: routeToNode returns a bare public-key node instead. So a direct
// session drew the exit and nothing about the transport under it. The fix is
// to describe the direct path as the one hop it actually is, rather than to
// special-case the renderer.
func TestRouteTree_DirectLegShowsTypeIDAndRTT(t *testing.T) {
	snap := directSnapshot(true)
	snap.Legs[0].LatencyMS = 12.5
	snap.Legs[0].Hops = []Hop{{
		From:      directSelfPK,
		To:        directExitPK,
		TpID:      "c63e3d2b-6041-0518-86d6-d321bf57fa57",
		TpType:    "stcpr",
		LatencyMS: 12.5,
	}}
	snap.Tunnels[0].Legs = snap.Legs

	cols := collectCols(RouteTree(snap))
	joined := strings.Join(cols, " ")
	require.Contains(t, joined, "stcpr", "the transport type must be rendered")
	require.Contains(t, joined, "c63e3d2b-6041-0518-86d6-d321bf57fa57", "the transport id must be rendered")
	require.Regexp(t, `12(\.5)?`, joined, "the transport RTT must be rendered; got %q", joined)

	// The hop's destination is still the exit, so the earlier guarantee holds.
	require.Equal(t, hopClass(true, 0), hopClassMap(snap)[directExitPK])
}

// collectCols gathers every node's Cols across the tree.
func collectCols(n *bitree.Node) []string {
	var out []string
	var walk func(*bitree.Node)
	walk = func(cur *bitree.Node) {
		if cur == nil {
			return
		}
		out = append(out, cur.Cols...)
		for _, c := range cur.Left {
			walk(c)
		}
		for _, c := range cur.Right {
			walk(c)
		}
	}
	walk(n)
	return out
}
