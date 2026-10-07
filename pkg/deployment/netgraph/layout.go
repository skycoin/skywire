// Package netgraph lays out the transport graph and serves it as a canvas page.
package netgraph

import (
	"math"
	"sort"
)

// Link is one transport between two visors, by their keys in hex.
type Link struct {
	A, B string
	Type string
}

// Graph is a laid out transport graph. Every pair of visors joined by at
// least one transport is one edge, with a bit set for each type joining them.
type Graph struct {
	Nodes []string
	X, Y  []float64
	// Degree is how many transports each visor has.
	Degree []int
	Types  []string
	Edges  []Edge
	// Transports is how many transports the graph was built from.
	Transports int
}

// Edge joins Nodes[A] and Nodes[B]. Mask has bit i set for Types[i].
type Edge struct {
	A, B uint16
	Mask uint8
}

// maxTypes is how many types fit in an edge's mask. Past that the rarest
// types share the last bit.
const maxTypes = 8

// Build turns links into a graph laid out from prev, the positions of an
// earlier layout by key, so that a refreshed picture stays where it was.
func Build(links []Link, prev map[string][2]float64) *Graph {
	idx := map[string]int{}
	g := &Graph{Transports: len(links)}
	typeCount := map[string]int{}
	for _, l := range links {
		typeCount[l.Type]++
		for _, k := range []string{l.A, l.B} {
			if _, ok := idx[k]; !ok {
				idx[k] = len(g.Nodes)
				g.Nodes = append(g.Nodes, k)
				g.Degree = append(g.Degree, 0)
			}
		}
	}
	if len(g.Nodes) > math.MaxUint16 {
		return &Graph{}
	}
	for t := range typeCount {
		g.Types = append(g.Types, t)
	}
	sort.Slice(g.Types, func(i, j int) bool { return typeCount[g.Types[i]] > typeCount[g.Types[j]] })
	bit := map[string]uint8{}
	for i, t := range g.Types {
		if i >= maxTypes {
			i = maxTypes - 1
		}
		bit[t] = 1 << i
	}
	if len(g.Types) > maxTypes {
		g.Types[maxTypes-1] = "other"
		g.Types = g.Types[:maxTypes]
	}

	pairs := map[[2]uint16]int{}
	for _, l := range links {
		a, b := idx[l.A], idx[l.B]
		g.Degree[a]++
		g.Degree[b]++
		if a == b {
			continue
		}
		if a > b {
			a, b = b, a
		}
		k := [2]uint16{uint16(a), uint16(b)} //nolint:gosec
		i, ok := pairs[k]
		if !ok {
			i = len(g.Edges)
			pairs[k] = i
			g.Edges = append(g.Edges, Edge{A: k[0], B: k[1]})
		}
		g.Edges[i].Mask |= bit[l.Type]
	}
	sort.Slice(g.Edges, func(i, j int) bool {
		if g.Edges[i].A != g.Edges[j].A {
			return g.Edges[i].A < g.Edges[j].A
		}
		return g.Edges[i].B < g.Edges[j].B
	})

	g.X = make([]float64, len(g.Nodes))
	g.Y = make([]float64, len(g.Nodes))
	warm := 0
	for i, k := range g.Nodes {
		if p, ok := prev[k]; ok {
			g.X[i], g.Y[i] = p[0], p[1]
			warm++
			continue
		}
		a := float64(i) * 2.399963
		r := math.Sqrt(float64(i)+1) / math.Sqrt(float64(len(g.Nodes))+1)
		g.X[i], g.Y[i] = r*math.Cos(a), r*math.Sin(a)
	}
	iterations := 220
	if warm > len(g.Nodes)*9/10 {
		iterations = 60
	}
	g.layout(iterations)
	return g
}

// layout is a force-directed layout in a unit square: every pair of visors
// repels, every edge pulls its ends together, and gravity keeps the whole
// graph centered. The step shrinks each round so the picture settles.
func (g *Graph) layout(iterations int) {
	n := len(g.Nodes)
	if n < 2 {
		return
	}
	k := math.Sqrt(1 / float64(n))
	dx := make([]float64, n)
	dy := make([]float64, n)
	// A visor with hundreds of transports would otherwise be pulled into the
	// middle of everything it touches; dividing by degree keeps hubs apart.
	pull := make([]float64, n)
	for i := range pull {
		pull[i] = 1 / math.Sqrt(float64(g.Degree[i])+1)
	}
	for it := 0; it < iterations; it++ {
		step := 0.08 * (1 - float64(it)/float64(iterations))
		for i := range dx {
			dx[i], dy[i] = 0, 0
		}
		for i := 0; i < n; i++ {
			xi, yi := g.X[i], g.Y[i]
			for j := i + 1; j < n; j++ {
				ex, ey := xi-g.X[j], yi-g.Y[j]
				d2 := ex*ex + ey*ey + 1e-9
				f := k * k / d2
				dx[i] += ex * f
				dy[i] += ey * f
				dx[j] -= ex * f
				dy[j] -= ey * f
			}
		}
		for _, e := range g.Edges {
			a, b := int(e.A), int(e.B)
			ex, ey := g.X[a]-g.X[b], g.Y[a]-g.Y[b]
			d := math.Sqrt(ex*ex+ey*ey) + 1e-9
			f := d / k
			dx[a] -= ex * f * pull[a]
			dy[a] -= ey * f * pull[a]
			dx[b] += ex * f * pull[b]
			dy[b] += ey * f * pull[b]
		}
		for i := 0; i < n; i++ {
			dx[i] -= g.X[i] * 4
			dy[i] -= g.Y[i] * 4
			d := math.Sqrt(dx[i]*dx[i]+dy[i]*dy[i]) + 1e-9
			m := math.Min(d, step) / d
			g.X[i] += dx[i] * m
			g.Y[i] += dy[i] * m
		}
	}
}

// Positions returns each visor's place, to lay out the next graph from.
func (g *Graph) Positions() map[string][2]float64 {
	out := make(map[string][2]float64, len(g.Nodes))
	for i, k := range g.Nodes {
		out[k] = [2]float64{g.X[i], g.Y[i]}
	}
	return out
}
