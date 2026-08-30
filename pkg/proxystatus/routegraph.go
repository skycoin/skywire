// Package proxystatus pkg/proxystatus/routegraph.go c4-app-web
//
// RouteGraph projects a surface Snapshot's live route-group state into the small
// node/edge subgraph the status.skysocks page hands to the GPU force-directed
// view (github.com/0magnet/cosmos-go, reused from pkg/tpviz/wasmgl via the one
// wasm-visor "netview" blob). It is the graph counterpart of routetree.go's
// ASCII tree: the SAME Snapshot data, shaped as a graph rather than a tree.
//
// Scope is deliberately SMALL — only the nodes and edges ON the proxy's actual
// routes: this visor (root), each intermediate hop, and the exit, plus the
// per-hop transport each leg traverses. It is NOT the whole mesh; an
// intermediate's other transports are not fetched. A hop PK shared by several
// legs is ONE node (deduplicated). Edges are colored by their STREAM (tunnel)
// with the same per-stream accent palette the tree uses, styled active vs
// standby, and widthed by the leg's live byte-rate.
//
// The engine renders points (no on-canvas text), so a node's FULL public key is
// never truncated on screen — it rides untruncated in the node's hover tooltip
// (Tip), with a short hex only as the tooltip's heading. The driver JS
// (render.go graphGLScript) maps this JSON to cosmos-go's typed-array setData
// payload (positions seeded, colors as CSS strings, links as index pairs).
package proxystatus

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Route-graph palette. These are the DARK-theme hex values of the page's CSS
// accent variables (--accent, --hop-exit, --hop1..4, --stream0..4). The GL view
// is an inherently dark canvas (cosmos BackgroundColor is fixed dark in
// wasmgl), like tpviz, so the graph uses the dark palette regardless of the
// page's light/dark theme — matching the network visualizer it is modeled on.
const (
	rgRootColor = "#7c83ff" // --accent: this visor (root)
	rgExitColor = "#ff5c5c" // --hop-exit: the exit (destination)
)

// rgHopColors cycles the intermediate-hop hues by DEPTH (--hop1..4), the same
// levels routetree.go colors hop PKs with, so a hop at a given depth reads the
// same hue in both the tree and the graph.
var rgHopColors = []string{"#4fc3f7", "#c48cff", "#ffb74d", "#4dd0a0"}

// rgStreamColors cycles the per-STREAM accents (--stream0..4) by tunnel index,
// so each --tunnels stream's edges read as one colored fan root→exit.
var rgStreamColors = []string{"#7c9cff", "#ff9e64", "#e879c9", "#4dd0e1", "#c3a6ff"}

// Node display sizes (cosmos point pixel radii). Root and exit are emphasized
// over the intermediate hops so the two endpoints of every route stand out.
const (
	rgRootSize = 15.0
	rgExitSize = 14.0
	rgHopSize  = 9.0
)

// rgNode is one deduplicated node — this visor, an intermediate hop, or the
// exit. ID is the FULL public key (the dedup key and the driver's index key);
// PK repeats it for the tooltip's copy affordance; Label is a short hex heading
// only. Color/Size drive the point; Tip is the multi-line hover text.
//
// X and Y are the node's DETERMINISTIC seed position, already in cosmos space
// ([0,rgSpaceSize], centered at rgSpaceSize/2) so the driver hands them straight
// to tpvizGL.setData's positions array (the way tpviz's grouped layout feeds
// fixed positions). cosmos-go renders every point at the origin unless seeded, so
// without these the small route subgraph collapses; with them the graph opens as
// a recognizable root-left → hops → exit-right fan and the force sim only refines
// it. See seedRouteLayout.
type rgNode struct {
	ID    string  `json:"id"`
	Label string  `json:"label"`
	Role  string  `json:"role"` // "root" | "hop" | "exit"
	Color string  `json:"color"`
	Size  float64 `json:"size"`
	Tip   string  `json:"tip"`
	PK    string  `json:"pk"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
}

// rgSpaceSize is cosmos's coordinate space; it MUST match the engine's
// Config.SpaceSize (pkg/tpviz/wasmgl/overlay.go spaceSize). Seed positions are
// emitted in this space so setData uses them verbatim (the driver skips the
// engine's own center-scatter fallback when a position array is present).
const rgSpaceSize = 8192.0

// rgLink is one hop segment of a leg's forward route (from → to). Color is the
// leg's STREAM accent; Width reflects the leg's live byte-rate; Active is false
// for a standby leg (the driver dims it). Stream is the tunnel index; Tip is the
// segment's transport/leg detail (also folded into each endpoint node's tip,
// since the engine hovers points, not links).
type rgLink struct {
	Source string  `json:"source"`
	Target string  `json:"target"`
	Color  string  `json:"color"`
	Width  float64 `json:"width"`
	Active bool    `json:"active"`
	Stream int     `json:"stream"`
	Tip    string  `json:"tip"`
}

// rgGraph is the whole route subgraph. Sig is a topology signature (node ids +
// link endpoints, order-independent) the driver uses to decide when a live push
// actually changed the SHAPE — so it only re-runs the force layout on a
// topology change, not on every ~1s metric update (which would jerk the graph).
type rgGraph struct {
	Nodes []rgNode `json:"nodes"`
	Links []rgLink `json:"links"`
	Sig   string   `json:"sig"`
}

// effectiveTunnels yields the stream-level route groups to graph: the snapshot's
// Tunnels when present, else a single synthetic tunnel wrapping the flat Legs
// list (a caller that projected only one route group). Empty when there is
// nothing routed.
func effectiveTunnels(snap Snapshot) []Tunnel {
	if len(snap.Tunnels) > 0 {
		return snap.Tunnels
	}
	if len(snap.Legs) == 0 {
		return nil
	}
	exit := ""
	for _, l := range snap.Legs {
		if l.RemotePK != "" {
			exit = l.RemotePK
			break
		}
	}
	return []Tunnel{{Index: 0, ExitPK: exit, Legs: snap.Legs}}
}

// hasRouteGraph reports whether snap has any alive route to graph.
func hasRouteGraph(snap Snapshot) bool {
	for _, t := range effectiveTunnels(snap) {
		for _, l := range t.Legs {
			if l.Alive {
				return true
			}
		}
	}
	return false
}

// nodeAcc accumulates a node's role, hop depth (for color) and incident-leg tip
// lines as the route walk visits it from multiple legs.
type nodeAcc struct {
	node      *rgNode
	lines     []string
	level     int // shallowest intermediate hop depth (1-based); 0 for root/exit
	streamOrd int // ordinal of the first stream that visited this hop; -1 = root/exit (centered)
	isRoot    bool
	isExit    bool
}

// RouteGraphJSON builds the route subgraph as the JSON the status page embeds in
// its <script type="application/json" id="rgdata"> and the cosmos-go driver
// consumes. Dead legs are pruned (matching the tree). Returns a valid, possibly
// empty ({"nodes":[],"links":[],"sig":""}) document — never nil — so the driver
// can always parse it.
func RouteGraphJSON(snap Snapshot) []byte {
	g := buildRouteGraph(snap)
	b, err := json.Marshal(g) // default HTML-escaping neutralizes any </script>
	if err != nil || b == nil {
		return []byte(`{"nodes":[],"links":[],"sig":""}`)
	}
	return b
}

// buildRouteGraph walks the Snapshot's tunnels/legs/hops into the dedup'd
// node/link model. Nodes are kept in first-encounter order (deterministic:
// root, then per stream in order, per leg in tree order, per hop). Each hop
// segment becomes a link and its detail line is folded into BOTH endpoint
// nodes' tooltips.
func buildRouteGraph(snap Snapshot) rgGraph {
	if !hasRouteGraph(snap) {
		return rgGraph{Nodes: []rgNode{}, Links: []rgLink{}}
	}
	tunnels := effectiveTunnels(snap)
	src := treeSrc(snap)
	nStreams := len(tunnels)

	accByPK := map[string]*nodeAcc{}
	var order []*nodeAcc
	get := func(pk string) *nodeAcc {
		if a, ok := accByPK[pk]; ok {
			return a
		}
		a := &nodeAcc{node: &rgNode{ID: pk, PK: pk, Label: shortPK(pk), Role: "hop"}, streamOrd: -1}
		accByPK[pk] = a
		order = append(order, a)
		return a
	}

	root := get(src)
	root.isRoot = true

	var links []rgLink
	maxRate := 0.0

	for ord, t := range tunnels {
		streamIdx := t.Index
		for _, l := range t.Legs {
			if !l.Alive {
				continue
			}
			rate := l.GoodputUpBps + l.GoodputDownBps
			if rate > maxRate {
				maxRate = rate
			}
			hops := l.Hops
			if len(hops) == 0 {
				// No recorded path: a single edge src → remote(exit).
				exitPK := l.RemotePK
				if exitPK == "" {
					exitPK = t.ExitPK
				}
				if exitPK == "" {
					continue
				}
				ex := get(exitPK)
				ex.isExit = true
				line := legLine(streamIdx, l, "", "", l.TpType, l.LatencyMS)
				root.lines = append(root.lines, line)
				ex.lines = append(ex.lines, line)
				links = append(links, rgLink{
					Source: src, Target: exitPK, Stream: streamIdx,
					Color: streamColor(streamIdx), Active: !l.Standby, Width: rate, Tip: line,
				})
				continue
			}
			n := len(hops)
			for i, h := range hops {
				isExit := i == n-1
				to := get(h.To)
				if isExit {
					to.isExit = true
				} else {
					if lvl := i + 1; to.level == 0 || lvl < to.level {
						to.level = lvl
					}
					if to.streamOrd < 0 {
						to.streamOrd = ord // first stream to traverse this hop sets its lane
					}
				}
				from := h.From
				if from == "" {
					from = src
				}
				line := legLine(streamIdx, l, h.TpID, h.To, h.TpType, h.LatencyMS)
				get(from).lines = append(get(from).lines, line)
				to.lines = append(to.lines, line)
				links = append(links, rgLink{
					Source: from, Target: h.To, Stream: streamIdx,
					Color: streamColor(streamIdx), Active: !l.Standby, Width: rate, Tip: line,
				})
			}
		}
	}

	// Finalize node visuals (role/color/size drive both the point and the seed
	// layout below, which reads Role).
	for _, a := range order {
		switch {
		case a.isRoot:
			a.node.Role, a.node.Color, a.node.Size, a.node.Label = "root", rgRootColor, rgRootSize, "source"
		case a.isExit:
			a.node.Role, a.node.Color, a.node.Size = "exit", rgExitColor, rgExitSize
		default:
			a.node.Role, a.node.Color, a.node.Size = "hop", hopColorFor(a.level), rgHopSize
		}
	}

	// Assign the deterministic seed layout (root left, exit right, hops between by
	// depth, streams fanned vertically) before copying the nodes out, so the graph
	// opens recognizable instead of collapsed at the origin.
	seedRouteLayout(order, nStreams)

	nodes := make([]rgNode, 0, len(order))
	for _, a := range order {
		a.node.Tip = nodeTip(a, nStreams)
		nodes = append(nodes, *a.node)
	}

	// Normalize link widths from the byte-rates into a readable pixel range;
	// standby legs are drawn dim + thin regardless of rate.
	for i := range links {
		links[i].Width = linkWidth(links[i].Width, maxRate, links[i].Active)
		if !links[i].Active {
			links[i].Color = dimColor(links[i].Color)
		}
	}

	return rgGraph{Nodes: nodes, Links: links, Sig: topoSig(nodes, links)}
}

// seedRouteLayout assigns each node a DETERMINISTIC seed position in cosmos space
// ([0,rgSpaceSize], centered), mirroring how tpviz's grouped layout hands fixed
// positions to the same engine. cosmos-go renders every point at the origin
// unless seeded (its force sim then has coincident points with no direction to
// separate), so a route subgraph without this collapses into a blob; with it the
// graph opens as a readable left→right fan and the sim only refines it.
//
// Geometry: the horizontal axis is HOP DEPTH — this visor (root) pinned left, the
// exit pinned right, intermediate hops spread between by their depth. The vertical
// axis FANS the streams: with more than one --tunnels stream each stream's hops
// ride their own lane, so the tunnels read as parallel fans converging on the
// shared root and exit (both centered). A small deterministic per-node jitter
// breaks the symmetry of parallel same-depth mux legs so the sim can separate
// them.
func seedRouteLayout(order []*nodeAcc, nStreams int) {
	maxLevel := 0
	for _, a := range order {
		if a.node.Role == "hop" && a.level > maxLevel {
			maxLevel = a.level
		}
	}
	maxCol := maxLevel + 1 // the exit sits one column past the deepest hop
	if maxCol < 1 {
		maxCol = 1
	}
	const (
		center = rgSpaceSize / 2
		xLeft  = rgSpaceSize * 0.18
		xRight = rgSpaceSize * 0.82
		vSpan  = rgSpaceSize * 0.55 // total vertical spread of the stream fan
	)
	lane := vSpan
	if nStreams > 1 {
		lane = vSpan / float64(nStreams)
	}
	colX := func(col int) float64 {
		return xLeft + float64(col)/float64(maxCol)*(xRight-xLeft)
	}
	laneY := func(ord int) float64 {
		if nStreams <= 1 || ord < 0 {
			return center
		}
		return center + (float64(ord)-float64(nStreams-1)/2)*lane
	}
	for i, a := range order {
		var col int
		y := center
		hop := false
		switch a.node.Role {
		case "root":
			col = 0
		case "exit":
			col = maxCol
		default: // hop
			hop = true
			col = a.level
			if col < 1 {
				col = 1
			}
			y = laneY(a.streamOrd)
		}
		x := colX(col)
		// Root and exit stay clean anchors; hops get a small deterministic jitter
		// (a hash of their order index in ±0.5) so parallel same-depth same-lane
		// legs don't stack exactly on top of each other.
		if hop {
			j := float64((i*2654435761)&0xffff)/65535.0 - 0.5
			x += j * (rgSpaceSize * 0.02)
			y += j * (rgSpaceSize * 0.05)
		}
		a.node.X = x
		a.node.Y = y
	}
}

// nodeTip renders a node's hover text: a role heading (with a short PK), the
// FULL public key on its own line (never truncated), and the routes/transports
// incident to this node.
func nodeTip(a *nodeAcc, nStreams int) string {
	var b strings.Builder
	role := a.node.Role
	fmt.Fprintf(&b, "%s · %s\n%s", strings.ToUpper(role), shortPK(a.node.ID), a.node.ID)
	if len(a.lines) > 0 {
		b.WriteString("\nroutes here:")
		for _, ln := range a.lines {
			b.WriteString("\n  ")
			b.WriteString(ln)
		}
	}
	_ = nStreams
	return b.String()
}

// legLine is one incident-leg detail line: stream, R[idx], state, direction,
// transport (type + id), hop rtt, route rtt, and the leg's cumulative ↑/↓ bytes
// with live rate. Shown in the endpoint nodes' tooltips (the engine hovers
// points, not links) and carried on the link's own Tip.
func legLine(streamIdx int, l Leg, tpID, to, tpType string, hopRTT float64) string {
	state := "active"
	if l.Standby {
		state = "standby"
	}
	dir := "multihop"
	if l.Direct {
		dir = "direct"
	}
	tp := "[" + orDash(tpType) + "]"
	if tpID != "" {
		tp += " " + tpID
	}
	var b strings.Builder
	fmt.Fprintf(&b, "s%d R[%d] %s %s · %s · hop %s · route %s · ↑%s(%s) ↓%s(%s)",
		streamIdx, l.Index, state, dir, tp,
		tpRTT(hopRTT), routeRTTCompact(l.RouteLatencyMS),
		compactBytes(l.SentBytes), compactRate(l.GoodputUpBps),
		compactBytes(l.RecvBytes), compactRate(l.GoodputDownBps))
	_ = to
	return b.String()
}

// streamColor is the per-stream accent for a tunnel index (cycling the palette).
func streamColor(idx int) string {
	i := ((idx % len(rgStreamColors)) + len(rgStreamColors)) % len(rgStreamColors)
	return rgStreamColors[i]
}

// hopColorFor is the intermediate-hop color at a given depth (cycling the hop
// palette), matching routetree.go's per-depth hop coloring.
func hopColorFor(level int) string {
	if level < 1 {
		level = 1
	}
	return rgHopColors[(level-1)%len(rgHopColors)]
}

// linkWidth maps a leg's byte-rate to a cosmos link width. Active legs scale
// 1.0..3.5 with their share of the busiest leg's rate (so the fattest edge is
// the fastest); standby legs are a thin constant.
func linkWidth(rate, maxRate float64, active bool) float64 {
	if !active {
		return 0.6
	}
	if maxRate <= 0 || rate <= 0 {
		return 1.0
	}
	return 1.0 + rate/maxRate*2.5
}

// dimColor turns a "#rrggbb" accent into a translucent rgba() so a standby
// leg's edge recedes behind the active ones. cosmos-go's GetRgbaColor parses
// rgba() strings. A non-hex input is returned unchanged.
func dimColor(hex string) string {
	r, g, bl, ok := parseHexRGB(hex)
	if !ok {
		return hex
	}
	return fmt.Sprintf("rgba(%d,%d,%d,0.4)", r, g, bl)
}

// parseHexRGB parses "#rrggbb" into 0..255 components.
func parseHexRGB(s string) (r, g, b int, ok bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) != 6 {
		return 0, 0, 0, false
	}
	v := func(i int) (int, bool) {
		var n int
		for _, c := range s[i : i+2] {
			switch {
			case c >= '0' && c <= '9':
				n = n*16 + int(c-'0')
			case c >= 'a' && c <= 'f':
				n = n*16 + int(c-'a') + 10
			case c >= 'A' && c <= 'F':
				n = n*16 + int(c-'A') + 10
			default:
				return 0, false
			}
		}
		return n, true
	}
	r, ok1 := v(0)
	g, ok2 := v(2)
	b, ok3 := v(4)
	return r, g, b, ok1 && ok2 && ok3
}

// shortPK is a short hex heading for a node's tooltip — a prefix only, never
// shown on the canvas and always paired with the full PK on the next line, so no
// truncated key is ever presented as complete. "this visor" (the src fallback)
// passes through unchanged.
func shortPK(pk string) string {
	pk = strings.TrimSpace(pk)
	if len(pk) <= 8 || strings.Contains(pk, " ") {
		return pk
	}
	return pk[:8]
}

// topoSig is an order-independent signature of the graph's SHAPE (node ids +
// link endpoints), so the live driver re-runs the force layout only when the
// topology actually changes, not on every metric refresh.
func topoSig(nodes []rgNode, links []rgLink) string {
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		ids = append(ids, n.ID)
	}
	pairs := make([]string, 0, len(links))
	for _, l := range links {
		// Order-independent per edge so a reordering of equal edges is not a change.
		if l.Source <= l.Target {
			pairs = append(pairs, l.Source+">"+l.Target)
		} else {
			pairs = append(pairs, l.Target+">"+l.Source)
		}
	}
	sortStrings(ids)
	sortStrings(pairs)
	return strings.Join(ids, ",") + "|" + strings.Join(pairs, ",")
}

// sortStrings is a tiny in-place insertion sort (avoids importing sort here for
// two short slices).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
