// Package latency pkg/tpviz/latency/embed.go c4-vis-latency
//
// Positions visors on a unit sphere so that great-circle distance
// approximates measured round-trip time. This is a metric embedding, not
// a force layout: a force simulation with uniform springs would ignore
// the latencies entirely, and cosmos-go's link distance is one global
// scalar, so the positions have to be solved here and handed to it.
//
// The solver is stress majorization (Gansner, Koren and North, "Graph
// Drawing by Stress Majorization", GD 2004) adapted to the sphere: each
// iteration moves a point toward the weighted mean of where its
// neighbors would place it, then renormalizes onto the sphere. On a
// sparse graph this converges in tens of iterations and each one is
// O(edges), so 43k edges over 1.3k visors is a few million operations.
package latency

import (
	"math"
	"sort"
)

// Edge is one measured pair. A and B index into the point slice.
type Edge struct {
	A, B int
	// MS is the round-trip time in milliseconds.
	MS float64
}

// Vec3 is a point on the unit sphere.
type Vec3 struct{ X, Y, Z float64 }

// Params tunes the embedding. The zero value is not useful; use
// DefaultParams.
type Params struct {
	// Iterations of stress majorization.
	Iterations int
	// Seed makes the initial placement deterministic, so the same graph
	// embeds the same way on every visor and across refreshes.
	Seed int64
	// FloorMS clamps implausibly small measurements. A sub-millisecond
	// RTT between two internet hosts is a measurement artifact, and
	// log(0) is worse.
	FloorMS float64
	// CeilMS clamps the tail. Observed maxima run to 30s against a
	// median near 230ms; without a ceiling a handful of broken
	// transports set the scale for everyone.
	CeilMS float64
}

// DefaultParams reflects the live distribution: p25 181ms, median 234ms,
// p75 314ms, p95 1468ms, max ~30s.
func DefaultParams() Params {
	return Params{Iterations: 200, Seed: 1, FloorMS: 1, CeilMS: 2000}
}

// target maps a latency to a great-circle distance in radians.
//
// The mapping is logarithmic because the distribution is heavy-tailed:
// on a linear scale the p95-to-max range would consume most of the
// sphere and the bulk of the network would collapse into a point. Log
// spreads the informative middle. The result spans (0, pi]: identical
// latencies sit together, the clamped maximum is antipodal.
func target(ms float64, p Params) float64 {
	if ms < p.FloorMS {
		ms = p.FloorMS
	}
	if ms > p.CeilMS {
		ms = p.CeilMS
	}
	lo, hi := math.Log(p.FloorMS), math.Log(p.CeilMS)
	if hi <= lo {
		return math.Pi / 2
	}
	return math.Pi * (math.Log(ms) - lo) / (hi - lo)
}

// Embed places n points on the unit sphere from the measured edges.
//
// Points with no edges are left on the initial spiral rather than
// dropped, so indices stay aligned with the caller's visor list.
func Embed(n int, edges []Edge, p Params) []Vec3 {
	if n <= 0 {
		return nil
	}
	pts := fibonacciSphere(n, p.Seed)
	if len(edges) == 0 || p.Iterations <= 0 {
		return pts
	}

	// Precompute each edge's target distance and weight. Stress
	// majorization weights a pair by 1/d^2, which is what makes short
	// distances (the confident, low-latency measurements) dominate over
	// long ones (where the log mapping is least informative anyway).
	type ew struct {
		a, b int
		d, w float64
	}
	ews := make([]ew, 0, len(edges))
	deg := make([]float64, n)
	for _, e := range edges {
		if e.A < 0 || e.B < 0 || e.A >= n || e.B >= n || e.A == e.B {
			continue
		}
		d := target(e.MS, p)
		if d <= 0 {
			d = 1e-6
		}
		w := 1 / (d * d)
		ews = append(ews, ew{a: e.A, b: e.B, d: d, w: w})
		deg[e.A] += w
		deg[e.B] += w
	}
	if len(ews) == 0 {
		return pts
	}

	acc := make([]Vec3, n)
	for it := 0; it < p.Iterations; it++ {
		for i := range acc {
			acc[i] = Vec3{}
		}
		for _, e := range ews {
			pa, pb := pts[e.a], pts[e.b]
			// Where b would like a to sit: rotate from b toward a by the
			// target angle, along the great circle joining them. Points
			// that coincide have no defined direction; nudge with the
			// deterministic tangent so they separate instead of sticking.
			ta := slerpToward(pb, pa, e.d)
			tb := slerpToward(pa, pb, e.d)
			acc[e.a] = addScaled(acc[e.a], ta, e.w)
			acc[e.b] = addScaled(acc[e.b], tb, e.w)
		}
		for i := range pts {
			if deg[i] == 0 {
				continue
			}
			v := Vec3{acc[i].X / deg[i], acc[i].Y / deg[i], acc[i].Z / deg[i]}
			if nv, ok := normalize(v); ok {
				pts[i] = nv
			}
		}
	}
	return pts
}

// slerpToward returns the point at angle theta from `from`, along the
// great circle heading toward `to`.
func slerpToward(from, to Vec3, theta float64) Vec3 {
	dot := clamp(from.X*to.X+from.Y*to.Y+from.Z*to.Z, -1, 1)
	// Tangent direction at `from` pointing at `to`, Gram-Schmidt.
	t := Vec3{to.X - dot*from.X, to.Y - dot*from.Y, to.Z - dot*from.Z}
	nt, ok := normalize(t)
	if !ok {
		// Coincident or antipodal: pick any tangent deterministically so
		// the pair still separates.
		nt, ok = normalize(orthogonal(from))
		if !ok {
			return from
		}
	}
	s, c := math.Sin(theta), math.Cos(theta)
	return Vec3{from.X*c + nt.X*s, from.Y*c + nt.Y*s, from.Z*c + nt.Z*s}
}

// fibonacciSphere is a deterministic near-uniform initial placement. A
// random start makes the result differ between visors looking at the
// same network, which for a shared picture is a defect.
func fibonacciSphere(n int, seed int64) []Vec3 {
	pts := make([]Vec3, n)
	golden := math.Pi * (3 - math.Sqrt(5))
	// The seed only rotates the spiral, so it never changes the shape.
	off := float64(seed%360) * math.Pi / 180
	for i := 0; i < n; i++ {
		y := 1.0
		if n > 1 {
			y = 1 - 2*float64(i)/float64(n-1)
		}
		r := math.Sqrt(math.Max(0, 1-y*y))
		th := golden*float64(i) + off
		pts[i] = Vec3{math.Cos(th) * r, y, math.Sin(th) * r}
	}
	return pts
}

func addScaled(a, b Vec3, s float64) Vec3 {
	return Vec3{a.X + b.X*s, a.Y + b.Y*s, a.Z + b.Z*s}
}

func normalize(v Vec3) (Vec3, bool) {
	l := math.Sqrt(v.X*v.X + v.Y*v.Y + v.Z*v.Z)
	if l < 1e-12 || math.IsNaN(l) || math.IsInf(l, 0) {
		return Vec3{}, false
	}
	return Vec3{v.X / l, v.Y / l, v.Z / l}, true
}

func orthogonal(v Vec3) Vec3 {
	if math.Abs(v.X) < 0.9 {
		return Vec3{1 - v.X*v.X, -v.X * v.Y, -v.X * v.Z}
	}
	return Vec3{-v.Y * v.X, 1 - v.Y*v.Y, -v.Y * v.Z}
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Angle returns the great-circle distance between two unit vectors.
func Angle(a, b Vec3) float64 {
	return math.Acos(clamp(a.X*b.X+a.Y*b.Y+a.Z*b.Z, -1, 1))
}

// Stress reports the residual, normalized so it can be compared across
// graphs: sum(w * (actual - target)^2) / sum(w * target^2). Zero is a
// perfect embedding; 1 is no better than placing everything at the mean
// distance. Exported so the view can show whether the picture it is
// drawing actually fits the data.
func Stress(pts []Vec3, edges []Edge, p Params) float64 {
	var num, den float64
	for _, e := range edges {
		if e.A < 0 || e.B < 0 || e.A >= len(pts) || e.B >= len(pts) {
			continue
		}
		d := target(e.MS, p)
		if d <= 0 {
			continue
		}
		w := 1 / (d * d)
		diff := Angle(pts[e.A], pts[e.B]) - d
		num += w * diff * diff
		den += w * d * d
	}
	if den == 0 {
		return 0
	}
	return num / den
}

// SortedByDegree returns point indices ordered by how many edges touch
// them, most first. The view uses it to label only the hubs.
func SortedByDegree(n int, edges []Edge) []int {
	deg := make([]int, n)
	for _, e := range edges {
		if e.A >= 0 && e.A < n {
			deg[e.A]++
		}
		if e.B >= 0 && e.B < n {
			deg[e.B]++
		}
	}
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool { return deg[idx[i]] > deg[idx[j]] })
	return idx
}
