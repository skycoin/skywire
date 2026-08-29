package proxystatus

import (
	"encoding/json"
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
			Hops: []Hop{hop("tp-direct-"+string(rune('0'+idx)), rgSrc, rgExit, "stcpr", 42)}}
	}
	multiLeg := func(idx int, standby bool) Leg {
		return Leg{Index: idx, RemotePK: rgExit, Direct: false, Alive: true, Standby: standby,
			LatencyMS: 30, RouteLatencyMS: 300, SentBytes: 512, RecvBytes: 256,
			GoodputUpBps: 100, GoodputDownBps: 50,
			Hops: []Hop{
				hop("tp-a-"+string(rune('0'+idx)), rgSrc, rgHopA, "sudph", 30),
				hop("tp-b-"+string(rune('0'+idx)), rgHopA, rgExit, "stcpr", 270),
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

// TestRenderGraphView asserts the page wires the GPU graph view: the toggle, the
// static canvas + tooltip, the #rgdata JSON payload, and the driver that boots
// the wasm-visor netview blob and drives cosmos-go (tpvizGL) — all self-contained
// and same-origin.
func TestRenderGraphView(t *testing.T) {
	page := string(Render(twoStreamSnap()))
	for _, want := range []string{
		// toggle + view containers
		`class="vtoggle"`, `onclick="setRouteView('tree')"`, `onclick="setRouteView('graph')"`,
		`class="muxtree"`, `id="rgraphsec"`, `id="rgcanvas"`, `id="rgtip"`,
		// route subgraph payload embedded as inert JSON in the live region
		`<script type="application/json" id="rgdata">`, `"nodes":`, `"links":`, `"sig":`,
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
	// Non-skysocks surfaces get no graph (no wasm route to serve it): no driver,
	// no canvas, no payload.
	dmsg := string(Render(Snapshot{Surface: SurfaceDmsg, App: "dmsgweb", Legs: twoStreamSnap().Legs}))
	for _, gone := range []string{`id="rgdata"`, `id="rgcanvas"`, "tpvizGL", "setRouteView"} {
		if strings.Contains(dmsg, gone) {
			t.Errorf("non-skysocks page must not contain graph markup %q", gone)
		}
	}
}
