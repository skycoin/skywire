package latency

import (
	"math"
	"testing"
)

// TestEmbedRecoversKnownGeometry: four points whose pairwise latencies
// describe a regular tetrahedron must come back as a tetrahedron. The
// tetrahedral angle is 109.47 degrees, so every pair should land there.
func TestEmbedRecoversKnownGeometry(t *testing.T) {
	// Pick a latency whose target() is the tetrahedral angle.
	p := DefaultParams()
	p.Iterations = 500
	want := math.Acos(-1.0 / 3.0)
	// Invert target(): ms = exp(lo + want/pi*(hi-lo)).
	lo, hi := math.Log(p.FloorMS), math.Log(p.CeilMS)
	ms := math.Exp(lo + want/math.Pi*(hi-lo))

	var edges []Edge
	for a := 0; a < 4; a++ {
		for b := a + 1; b < 4; b++ {
			edges = append(edges, Edge{A: a, B: b, MS: ms})
		}
	}
	pts := Embed(4, edges, p)
	for _, e := range edges {
		got := Angle(pts[e.A], pts[e.B])
		if math.Abs(got-want) > 0.05 {
			t.Errorf("pair %d-%d: angle %.4f rad, want %.4f", e.A, e.B, got, want)
		}
	}
	if s := Stress(pts, edges, p); s > 0.01 {
		t.Errorf("stress %.4f, want < 0.01 on an exactly-realizable graph", s)
	}
}

// TestEmbedIsDeterministic: two visors looking at the same network must
// draw the same picture.
func TestEmbedIsDeterministic(t *testing.T) {
	edges := []Edge{{0, 1, 50}, {1, 2, 200}, {2, 3, 400}, {3, 0, 120}, {0, 2, 900}}
	a := Embed(4, edges, DefaultParams())
	b := Embed(4, edges, DefaultParams())
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("point %d differs between runs: %v vs %v", i, a[i], b[i])
		}
	}
}

// TestEmbedOrdersByLatency: the whole point of the view is that a lower
// latency means a shorter arc. Verify the ordering is monotone.
func TestEmbedOrdersByLatency(t *testing.T) {
	p := DefaultParams()
	p.Iterations = 400
	// A hub with spokes at increasing latency.
	lat := []float64{20, 80, 300, 1200}
	var edges []Edge
	for i, ms := range lat {
		edges = append(edges, Edge{A: 0, B: i + 1, MS: ms})
	}
	pts := Embed(len(lat)+1, edges, p)
	prev := -1.0
	for i := range lat {
		d := Angle(pts[0], pts[i+1])
		if d < prev {
			t.Errorf("spoke %d (%.0fms) at %.3f rad is closer than the previous %.3f", i, lat[i], d, prev)
		}
		prev = d
	}
}

// TestEmbedKeepsIndicesForIsolatedPoints: a visor with no measured
// transports must still get a position, or the caller's indices shift.
func TestEmbedKeepsIndicesForIsolatedPoints(t *testing.T) {
	pts := Embed(5, []Edge{{0, 1, 100}}, DefaultParams())
	if len(pts) != 5 {
		t.Fatalf("got %d points, want 5", len(pts))
	}
	for i, v := range pts {
		l := math.Sqrt(v.X*v.X + v.Y*v.Y + v.Z*v.Z)
		if math.Abs(l-1) > 1e-9 {
			t.Errorf("point %d is not on the unit sphere: |v|=%.9f", i, l)
		}
	}
}

// TestTargetIsMonotone guards the latency-to-angle mapping.
func TestTargetIsMonotone(t *testing.T) {
	p := DefaultParams()
	prev := -1.0
	for _, ms := range []float64{0.1, 1, 10, 100, 234, 1000, 2000, 30000} {
		d := target(ms, p)
		if d < prev {
			t.Errorf("target(%v)=%.4f is below the previous %.4f", ms, d, prev)
		}
		if d < 0 || d > math.Pi {
			t.Errorf("target(%v)=%.4f is outside (0, pi]", ms, d)
		}
		prev = d
	}
}
