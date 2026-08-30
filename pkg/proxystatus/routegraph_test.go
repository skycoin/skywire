package proxystatus

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// sample route PKs (full, never truncated).
const (
	rgSrc  = "02aa11223344556677889900aabbccddeeff00112233445566778899aabbccddee"
	rgExit = "0311223344556677889900aabbccddeeff00112233445566778899aabbccddeeff"
	rgHopA = "03bb223344556677889900aabbccddeeff00112233445566778899aabbccddeeff"
)

func hop(id, from, to, typ string, rtt float64) Hop {
	return Hop{TpID: id, From: from, To: to, TpType: typ, LatencyMS: rtt}
}

// twoStreamSnap is a multi-stream snapshot: two tunnels, each with a direct leg
// and a multihop leg through a SHARED intermediate (rgHopA) to a SHARED exit —
// so the graph must dedup rgHopA and rgExit to one node each. One leg is standby.
func twoStreamSnap() Snapshot {
	directLeg := func(idx int, standby bool) Leg {
		return Leg{Index: idx, RemotePK: rgExit, Direct: true, Alive: true, Standby: standby,
			LatencyMS: 42, RouteLatencyMS: 42, SentBytes: 2048, RecvBytes: 1024,
			GoodputUpBps: 800, GoodputDownBps: 400,
			Hops: []Hop{hop("tp-direct-"+strconv.Itoa(idx), rgSrc, rgExit, "stcpr", 42)}}
	}
	multiLeg := func(idx int, standby bool) Leg {
		return Leg{Index: idx, RemotePK: rgExit, Direct: false, Alive: true, Standby: standby,
			LatencyMS: 30, RouteLatencyMS: 300, SentBytes: 512, RecvBytes: 256,
			GoodputUpBps: 100, GoodputDownBps: 50,
			Hops: []Hop{
				hop("tp-a-"+strconv.Itoa(idx), rgSrc, rgHopA, "sudph", 30),
				hop("tp-b-"+strconv.Itoa(idx), rgHopA, rgExit, "stcpr", 270),
			}}
	}
	return Snapshot{
		Surface: SurfaceSkysocks, App: "skysocks-client", Running: true, MuxEnabled: true,
		Tunnels: []Tunnel{
			{Index: 0, ExitPK: rgExit, MuxEnabled: true, Legs: []Leg{directLeg(0, false), multiLeg(1, false)}},
			{Index: 1, ExitPK: rgExit, MuxEnabled: true, Legs: []Leg{directLeg(2, true), multiLeg(3, false)}},
		},
		Legs: []Leg{directLeg(0, false), multiLeg(1, false)}, // back-compat mirror of Tunnels[0]
	}
}

// TestBuildRouteGraph_HoplessLegsSynthesize verifies legs WITHOUT a recorded
// forward path (auto/standby legs the router did not record hops for) still render
// from their own transport: a direct hop-less leg draws src→exit, and a relayed
// hop-less leg draws src→intermediate→exit — converging on the TRUE exit (t.ExitPK)
// rather than mislabeling the leg's first-hop intermediate as the exit.
func TestBuildRouteGraph_HoplessLegsSynthesize(t *testing.T) {
	snap := Snapshot{
		Surface: SurfaceSkysocks, App: "skysocks-client", Running: true, MuxEnabled: true, SelfPK: rgSrc,
		Tunnels: []Tunnel{{Index: 0, ExitPK: rgExit, MuxEnabled: true, Legs: []Leg{
			{Index: 0, RemotePK: rgExit, Direct: true, Alive: true},  // direct, no hops
			{Index: 1, RemotePK: rgHopA, Direct: false, Alive: true}, // relayed, no hops
		}}},
	}
	g := buildRouteGraph(snap)
	roleOf := map[string]string{}
	for _, n := range g.Nodes {
		roleOf[n.ID] = n.Role
	}
	if roleOf[rgExit] != "exit" {
		t.Fatalf("rgExit role = %q, want exit", roleOf[rgExit])
	}
	if roleOf[rgHopA] == "exit" {
		t.Fatalf("rgHopA (an intermediate) mislabeled as exit")
	}
	if roleOf[rgSrc] != "root" {
		t.Fatalf("rgSrc role = %q, want root (treeSrc should fall back to SelfPK)", roleOf[rgSrc])
	}
	has := func(s, d string) bool {
		for _, l := range g.Links {
			if l.Source == s && l.Target == d {
				return true
			}
		}
		return false
	}
	if !has(rgSrc, rgExit) {
		t.Error("missing direct src→exit link")
	}
	if !has(rgSrc, rgHopA) {
		t.Error("missing relayed src→intermediate link")
	}
	if !has(rgHopA, rgExit) {
		t.Error("missing intermediate→exit convergence link (relayed leg must reach the true exit)")
	}
}

func TestBuildRouteGraph_DedupRolesColors(t *testing.T) {
	g := buildRouteGraph(twoStreamSnap())

	// Dedup: src + exit + one shared intermediate = exactly 3 nodes.
	if len(g.Nodes) != 3 {
		ids := make([]string, len(g.Nodes))
		for i, n := range g.Nodes {
			ids[i] = n.ID
		}
		t.Fatalf("want 3 deduped nodes, got %d: %v", len(g.Nodes), ids)
	}

	byID := map[string]rgNode{}
	for _, n := range g.Nodes {
		byID[n.ID] = n
	}
	if n := byID[rgSrc]; n.Role != "root" || n.Color != rgRootColor {
		t.Errorf("root node = %+v", n)
	}
	if n := byID[rgExit]; n.Role != "exit" || n.Color != rgExitColor {
		t.Errorf("exit node = %+v", n)
	}
	if n := byID[rgHopA]; n.Role != "hop" || n.Color != rgHopColors[0] {
		t.Errorf("hop node = %+v (want hop / %s)", n, rgHopColors[0])
	}

	// Every node tooltip carries the FULL (untruncated) PK.
	for _, n := range g.Nodes {
		if !strings.Contains(n.Tip, n.ID) {
			t.Errorf("node %s tip must contain the full PK", n.ID)
		}
	}

	// The exit node is reached by legs from BOTH streams, so its tip lists s0 and s1.
	exitTip := byID[rgExit].Tip
	if !strings.Contains(exitTip, "s0 ") || !strings.Contains(exitTip, "s1 ") {
		t.Errorf("exit tip should mention both streams:\n%s", exitTip)
	}
}

func TestBuildRouteGraph_StreamColorsAndStandby(t *testing.T) {
	g := buildRouteGraph(twoStreamSnap())

	var haveS0, haveS1, haveDim, haveActiveWide bool
	for _, l := range g.Links {
		switch l.Stream {
		case 0:
			if l.Color == rgStreamColors[0] {
				haveS0 = true
			}
		case 1:
			// stream1 active edges keep the accent; the standby direct leg is dimmed.
			if l.Color == rgStreamColors[1] {
				haveS1 = true
			}
		}
		if !l.Active {
			haveDim = true
			if !strings.HasPrefix(l.Color, "rgba(") {
				t.Errorf("standby link color should be dimmed rgba(), got %q", l.Color)
			}
			if l.Width > 0.7 {
				t.Errorf("standby link width should be thin, got %v", l.Width)
			}
		}
		if l.Active && l.Width > 1.0 {
			haveActiveWide = true // rate-scaled width above the 1.0 floor
		}
	}
	if !haveS0 || !haveS1 {
		t.Errorf("expected per-stream accent colors on edges (s0=%v s1=%v)", haveS0, haveS1)
	}
	if !haveDim {
		t.Error("expected at least one standby (dimmed) edge")
	}
	if !haveActiveWide {
		t.Error("expected the busiest active edge to be widened by its rate")
	}
}

func TestBuildRouteGraph_PrunesDeadAndSig(t *testing.T) {
	snap := twoStreamSnap()
	// Kill one leg; it must not contribute nodes/edges.
	snap.Tunnels[0].Legs[1].Alive = false
	g := buildRouteGraph(snap)
	for _, l := range g.Links {
		if l.Stream == 0 && l.Target == rgHopA {
			t.Error("dead leg's hop edge should be pruned")
		}
	}

	// topoSig is stable under a pure metric change (bytes/rate) but changes when a
	// node/edge is added or removed.
	base := buildRouteGraph(twoStreamSnap()).Sig
	snap2 := twoStreamSnap()
	snap2.Tunnels[0].Legs[0].SentBytes = 999999
	snap2.Tunnels[0].Legs[0].GoodputUpBps = 12345
	if buildRouteGraph(snap2).Sig != base {
		t.Error("topoSig must be invariant to metric-only changes")
	}
	snap3 := twoStreamSnap()
	snap3.Tunnels[0].Legs[1].Alive = false
	if buildRouteGraph(snap3).Sig == base {
		t.Error("topoSig must change when the topology changes")
	}
}

func TestRouteGraphJSON_Valid(t *testing.T) {
	// Empty snapshot yields a valid, parseable, empty document.
	var empty rgGraph
	if err := json.Unmarshal(RouteGraphJSON(Snapshot{Surface: SurfaceSkysocks}), &empty); err != nil {
		t.Fatalf("empty graph JSON invalid: %v", err)
	}
	if len(empty.Nodes) != 0 || len(empty.Links) != 0 {
		t.Errorf("empty snapshot should yield no nodes/links, got %+v", empty)
	}

	var g rgGraph
	if err := json.Unmarshal(RouteGraphJSON(twoStreamSnap()), &g); err != nil {
		t.Fatalf("graph JSON invalid: %v", err)
	}
	if len(g.Nodes) != 3 || len(g.Links) == 0 || g.Sig == "" {
		t.Errorf("round-tripped graph looks wrong: nodes=%d links=%d sig=%q", len(g.Nodes), len(g.Links), g.Sig)
	}
}

// TestRenderGraphView asserts the page wires the GPU graph view unified with the
// tree and log: the per-section collapse toggles, the static canvas + tooltip,
// the #rgdata JSON payload (now carrying seed positions), and the driver that
// boots the wasm-visor netview blob and drives cosmos-go (tpvizGL) — all
// self-contained and same-origin.
func TestRenderGraphView(t *testing.T) {
	page := string(Render(twoStreamSnap()))
	for _, want := range []string{
		// independent per-section SHOW/HIDE toggles (log, tree, graph), all
		// visible by default, replacing the old exclusive tree/graph switch
		`class="sectoggle"`, `onclick="secToggle('tree')"`, `onclick="secToggle('graph')"`,
		`onclick="secToggle('log')"`, "window.secToggle",
		`class="muxtree"`, `id="rgraphsec"`, `class="rgbody"`, `id="rgcanvas"`, `id="rgtip"`,
		// interactive zoom/fit controls, wired to the cosmos-go zoom/fit API
		`class="rgctl"`, `onclick="rgZoom(1.3)"`, `onclick="rgZoom(1/1.3)"`, `onclick="rgFit()"`,
		"window.rgZoom", "window.rgFit", "g.zoomBy",
		// route subgraph payload embedded as inert JSON in the live region, now
		// including the deterministic seed positions per node (x,y)
		`<script type="application/json" id="rgdata">`, `"nodes":`, `"links":`, `"sig":`, `"x":`, `"y":`,
		// driver hands the seed positions to setData and re-fits after a settle
		`nd.x`, `nd.y`, "positions:pp",
		// driver reuses the network visualizer's engine with no new wasm:
		// netview role → tpvizGL (cosmos-go), served same-origin
		`__SKYWIRE_WASM_ROLE__`, `"netview"`, "/main.wasm", "/wasm_exec.js",
		"tpvizGL", "instantiateStreaming", "MutationObserver", "setData",
		// per-stream + exit colors reach the payload
		rgExitColor, rgStreamColors[0], rgStreamColors[1],
	} {
		if !strings.Contains(page, want) {
			t.Errorf("graph-view page missing %q", want)
		}
	}
	// The old exclusive toggle must be gone.
	for _, gone := range []string{`class="vtoggle"`, "setRouteView", "gv-graph"} {
		if strings.Contains(page, gone) {
			t.Errorf("page still contains removed exclusive-toggle markup %q", gone)
		}
	}
	// The graph section must NOT be display:none-gated (it is visible by default).
	if strings.Contains(page, `#rgraphsec{display:none`) {
		t.Error("graph section must not be hidden by default")
	}
	// Non-skysocks surfaces get no graph (no wasm route to serve it): no driver,
	// no canvas, no payload, no toggles.
	dmsg := string(Render(Snapshot{Surface: SurfaceDmsg, App: "dmsgweb", Legs: twoStreamSnap().Legs}))
	for _, gone := range []string{`id="rgdata"`, `id="rgcanvas"`, "tpvizGL", "secToggle", `class="sectoggle"`} {
		if strings.Contains(dmsg, gone) {
			t.Errorf("non-skysocks page must not contain graph markup %q", gone)
		}
	}
}

// TestSeedRouteLayout asserts the deterministic seed layout: this visor (root)
// pinned left, the exit pinned right, intermediate hops between them, every
// position inside cosmos space, and — with multiple streams — the per-stream hop
// lanes fanned to distinct vertical bands.
func TestSeedRouteLayout(t *testing.T) {
	g := buildRouteGraph(twoStreamSnap())
	byID := map[string]rgNode{}
	for _, n := range g.Nodes {
		byID[n.ID] = n
		if n.X < 0 || n.X > rgSpaceSize || n.Y < 0 || n.Y > rgSpaceSize {
			t.Errorf("node %s seed pos out of space: (%v,%v)", n.ID, n.X, n.Y)
		}
	}
	root, hop, exit := byID[rgSrc], byID[rgHopA], byID[rgExit]
	if !(root.X < hop.X && hop.X < exit.X) {
		t.Errorf("want root.X < hop.X < exit.X, got %v %v %v", root.X, hop.X, exit.X)
	}

	// A two-stream snapshot with a DISTINCT intermediate per stream must place the
	// two hops on different vertical lanes (the streams fan).
	hopS0 := "03c0223344556677889900aabbccddeeff00112233445566778899aabbccddeeff"
	hopS1 := "03d1223344556677889900aabbccddeeff00112233445566778899aabbccddeeff"
	leg := func(idx int, via string) Leg {
		return Leg{Index: idx, RemotePK: rgExit, Alive: true, GoodputUpBps: 100,
			Hops: []Hop{hop2(rgSrc, via, "sudph"), hop2(via, rgExit, "stcpr")}}
	}
	fan := Snapshot{Surface: SurfaceSkysocks,
		Tunnels: []Tunnel{
			{Index: 0, ExitPK: rgExit, Legs: []Leg{leg(0, hopS0)}},
			{Index: 1, ExitPK: rgExit, Legs: []Leg{leg(1, hopS1)}},
		},
		Legs: []Leg{leg(0, hopS0)},
	}
	fg := buildRouteGraph(fan)
	fb := map[string]rgNode{}
	for _, n := range fg.Nodes {
		fb[n.ID] = n
	}
	if fb[hopS0].Y == fb[hopS1].Y {
		t.Errorf("per-stream hops should fan to distinct lanes, both at Y=%v", fb[hopS0].Y)
	}
}

func hop2(from, to, typ string) Hop { return Hop{From: from, To: to, TpType: typ} }
