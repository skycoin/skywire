//go:build js && wasm

// Package wasmgl pkg/tpviz/wasmgl/latencyview.go c4-vis-latency
// The latency-space view: visors positioned by measured round-trip time
// rather than by geography, with a spherical Voronoi cell per visor.
//
// The sphere here is NOT the Earth. Position is a function of latency
// only, so two visors sit close together when the network puts them
// close together, whichever continents they are on. Geography is
// available as a colour, which is what makes the interesting question
// askable: where does network distance agree with physical distance, and
// where does it not.
//
// Division of labour follows the rest of this package. The embedding,
// the tessellation and the projection are Go, because they are numeric
// work over the whole point set; cosmos-go draws the points and links
// from the projected positions with its simulation OFF, because the
// positions are solved, not simulated. Its link distance is a single
// global scalar, so a force layout could not honour per-edge latency
// even in principle.
package wasmgl

import (
	"math"
	"syscall/js"

	latency "github.com/0magnet/spheregraph"
)

// latencyState is the computed view, kept so a camera rotation can
// reproject without re-solving.
type latencyState struct {
	// sites are the embedded positions, before coincident separation.
	// The Voronoi is built from these.
	sites []latency.Vec3
	// render are the positions handed to cosmos-go, in which coincident
	// visors are spread onto a tiny ring so each stays selectable.
	render []latency.Vec3
	cells  []latency.Cell
	edges  []latency.Edge
	stress float64

	// yaw and pitch orient the sphere for the orthographic projection.
	yaw, pitch float64
}

var latView latencyState

// jsSetLatencyGraph solves the embedding and the tessellation.
//
// Argument is {visors: string[], edges: [{a,b,ms}]} where a and b index
// into visors. Returns {points, stress, cells, hidden} — see project().
func jsSetLatencyGraph(_ js.Value, args []js.Value) interface{} {
	cfg := arg(args, 0)
	if !cfg.Truthy() {
		return js.ValueOf(map[string]interface{}{"error": "no graph"})
	}
	nVisors := cfg.Get("visors").Length()
	rawEdges := cfg.Get("edges")
	edges := make([]latency.Edge, 0, rawEdges.Length())
	for i := 0; i < rawEdges.Length(); i++ {
		e := rawEdges.Index(i)
		edges = append(edges, latency.Edge{
			A:  e.Get("a").Int(),
			B:  e.Get("b").Int(),
			MS: e.Get("ms").Float(),
		})
	}

	p := latency.DefaultParams()
	latView.sites = latency.Embed(nVisors, edges, p)
	latView.render = latency.SeparateCoincident(latView.sites)
	latView.cells = latency.Voronoi(latView.sites)
	latView.edges = edges
	latView.stress = latency.Stress(latView.sites, edges, p)
	return latView.project()
}

// jsRotateLatency turns the sphere. Reprojects without re-solving, which
// is what keeps a drag interactive on a 43k-edge graph.
func jsRotateLatency(_ js.Value, args []js.Value) interface{} {
	latView.yaw += arg(args, 0).Float()
	latView.pitch += arg(args, 1).Float()
	// Stop just short of the poles so the tangent frame stays defined.
	const lim = math.Pi/2 - 1e-3
	latView.pitch = math.Max(-lim, math.Min(lim, latView.pitch))
	return latView.project()
}

// project maps the sphere to the plane for cosmos-go.
//
// Orthographic, so the sphere reads as a sphere and a cell's area on
// screen falls off toward the limb the way a globe's does. Points on the
// far side are reported in `hidden` rather than dropped, because
// dropping them would renumber every index the caller holds.
func (s *latencyState) project() interface{} {
	n := len(s.render)
	pts := make([]interface{}, 0, n*2)
	hidden := make([]interface{}, 0, n)
	cy, sy := math.Cos(s.yaw), math.Sin(s.yaw)
	cp, sp := math.Cos(s.pitch), math.Sin(s.pitch)

	rot := func(v latency.Vec3) latency.Vec3 {
		x := v.X*cy + v.Z*sy
		z := -v.X*sy + v.Z*cy
		y := v.Y*cp - z*sp
		z = v.Y*sp + z*cp
		return latency.Vec3{X: x, Y: y, Z: z}
	}
	for i, v := range s.render {
		r := rot(v)
		pts = append(pts, r.X, r.Y)
		if r.Z < 0 {
			hidden = append(hidden, i)
		}
	}
	// Cells as flat vertex rings, front-facing only: a cell whose every
	// vertex is on the far side is not drawn at all, and one that
	// straddles the limb is drawn clipped by the renderer.
	cells := make([]interface{}, 0, len(s.cells))
	for _, c := range s.cells {
		if len(c.Polygon) < 3 {
			continue
		}
		ring := make([]interface{}, 0, len(c.Polygon)*2)
		front := false
		for _, v := range c.Polygon {
			r := rot(v)
			if r.Z >= 0 {
				front = true
			}
			ring = append(ring, r.X, r.Y)
		}
		if !front {
			continue
		}
		cells = append(cells, map[string]interface{}{
			"site": c.Site,
			"ring": ring,
		})
	}
	return js.ValueOf(map[string]interface{}{
		"points": pts,
		"hidden": hidden,
		"cells":  cells,
		"stress": s.stress,
		"yaw":    s.yaw,
		"pitch":  s.pitch,
	})
}

// jsLatencyStats reports how well the drawing fits the measurements, so
// the view can say so rather than implying a precision it does not have.
func jsLatencyStats(_ js.Value, _ []js.Value) interface{} {
	return js.ValueOf(map[string]interface{}{
		"visors": len(latView.sites),
		"edges":  len(latView.edges),
		"stress": latView.stress,
		"cells":  len(latView.cells),
	})
}
