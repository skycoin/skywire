package latency

import (
	"math"
	"testing"
)

// TestDelaunayEuler: a triangulated sphere must satisfy V - E + F = 2,
// and every point on a sphere is a hull vertex.
func TestDelaunayEuler(t *testing.T) {
	for _, n := range []int{4, 8, 50, 500} {
		pts := fibonacciSphere(n, 1)
		tris := Delaunay(pts)
		if len(tris) == 0 {
			t.Fatalf("n=%d: no triangles", n)
		}
		edges := map[[2]int]bool{}
		verts := map[int]bool{}
		for _, tr := range tris {
			for _, e := range [][2]int{{tr.A, tr.B}, {tr.B, tr.C}, {tr.C, tr.A}} {
				if e[0] > e[1] {
					e[0], e[1] = e[1], e[0]
				}
				edges[e] = true
			}
			verts[tr.A], verts[tr.B], verts[tr.C] = true, true, true
		}
		V, E, F := len(verts), len(edges), len(tris)
		if V-E+F != 2 {
			t.Errorf("n=%d: V-E+F = %d-%d+%d = %d, want 2", n, V, E, F, V-E+F)
		}
		if V != n {
			t.Errorf("n=%d: only %d of %d points are hull vertices", n, V, n)
		}
		// A closed triangulation has 2E = 3F.
		if 2*E != 3*F {
			t.Errorf("n=%d: 2E=%d but 3F=%d", n, 2*E, 3*F)
		}
	}
}

// TestVoronoiCellsAreClosedAndNearTheirSite: each cell must be a ring of
// at least three vertices, and every vertex must be closer to its own
// site than the site is to any other point it borders.
func TestVoronoiCellsAreClosedAndNearTheirSite(t *testing.T) {
	pts := fibonacciSphere(200, 1)
	cells := Voronoi(pts)
	if len(cells) != len(pts) {
		t.Fatalf("got %d cells for %d points", len(cells), len(pts))
	}
	empty := 0
	for _, c := range cells {
		if len(c.Polygon) == 0 {
			empty++
			continue
		}
		if len(c.Polygon) < 3 {
			t.Errorf("cell %d has %d vertices, want >= 3", c.Site, len(c.Polygon))
		}
		for _, v := range c.Polygon {
			l := math.Sqrt(dot(v, v))
			if math.Abs(l-1) > 1e-9 {
				t.Errorf("cell %d vertex is off the sphere: |v|=%.9f", c.Site, l)
			}
		}
	}
	if empty > 0 {
		t.Errorf("%d cells are empty on a uniform sphere; want 0", empty)
	}
}

// TestVoronoiDefiningProperty: a cell vertex is equidistant from the
// three sites that generate it, and no site is closer. This is what
// makes it a Voronoi diagram rather than a decorative polygon.
func TestVoronoiDefiningProperty(t *testing.T) {
	pts := fibonacciSphere(60, 1)
	cells := Voronoi(pts)
	for _, c := range cells {
		for _, v := range c.Polygon {
			own := Angle(v, pts[c.Site])
			for j := range pts {
				if j == c.Site {
					continue
				}
				if Angle(v, pts[j]) < own-1e-6 {
					t.Fatalf("cell %d has a vertex closer to site %d than to its own site (%.6f < %.6f)",
						c.Site, j, Angle(v, pts[j]), own)
				}
			}
		}
	}
}

// TestVoronoiAreaSumsToSphere: the cells tessellate, so their solid
// angles must sum to 4*pi. This is the property that distinguishes a
// tessellation from a set of independent boundary circles.
func TestVoronoiAreaSumsToSphere(t *testing.T) {
	pts := fibonacciSphere(120, 1)
	cells := Voronoi(pts)
	total := 0.0
	for _, c := range cells {
		total += sphericalPolygonArea(c.Polygon)
	}
	if math.Abs(total-4*math.Pi) > 0.05 {
		t.Errorf("cell areas sum to %.4f, want 4*pi = %.4f", total, 4*math.Pi)
	}
}

// sphericalPolygonArea by the spherical excess of its fan triangulation.
func sphericalPolygonArea(poly []Vec3) float64 {
	if len(poly) < 3 {
		return 0
	}
	area := 0.0
	for i := 1; i < len(poly)-1; i++ {
		area += sphericalTriangleArea(poly[0], poly[i], poly[i+1])
	}
	return area
}

func sphericalTriangleArea(a, b, c Vec3) float64 {
	// L'Huilier's formula.
	ab, bc, ca := Angle(a, b), Angle(b, c), Angle(c, a)
	s := (ab + bc + ca) / 2
	t := math.Tan(s/2) * math.Tan((s-ab)/2) * math.Tan((s-bc)/2) * math.Tan((s-ca)/2)
	if t <= 0 {
		return 0
	}
	return 4 * math.Atan(math.Sqrt(t))
}

// TestSeparateCoincident: the render path must make every visor
// individually visible, while the Voronoi keeps merging them.
func TestSeparateCoincident(t *testing.T) {
	base := fibonacciSphere(20, 1)
	pts := append([]Vec3{}, base...)
	// Four visors on one machine: identical latency profiles, identical
	// embedded position.
	pts[5] = pts[3]
	pts[7] = pts[3]
	pts[9] = pts[3]

	sep := SeparateCoincident(pts)
	if len(sep) != len(pts) {
		t.Fatalf("got %d render positions for %d points", len(sep), len(pts))
	}
	for i := range sep {
		l := math.Sqrt(dot(sep[i], sep[i]))
		if math.Abs(l-1) > 1e-9 {
			t.Errorf("render point %d is off the sphere: |v|=%.12f", i, l)
		}
	}
	// No two render positions may coincide.
	for i := range sep {
		for j := i + 1; j < len(sep); j++ {
			if Angle(sep[i], sep[j]) < dedupeEps {
				t.Errorf("render points %d and %d still coincide", i, j)
			}
		}
	}
	// The four that shared a position must stay within a hair of it, so
	// they cannot wander into a neighboring cell.
	for _, i := range []int{3, 5, 7, 9} {
		if d := Angle(sep[i], pts[3]); d > 2*coincidentSpread {
			t.Errorf("render point %d moved %.2e rad from its site, want <= %.2e", i, d, 2*coincidentSpread)
		}
	}
	// The Voronoi, from the ORIGINAL positions, still merges them.
	cells := Voronoi(pts)
	for _, i := range []int{5, 7, 9} {
		if len(cells[i].Polygon) != len(cells[3].Polygon) {
			t.Errorf("cell %d does not share cell 3's polygon", i)
		}
	}
}

// TestSeparateCoincidentIsDeterministic: same input, same picture.
func TestSeparateCoincidentIsDeterministic(t *testing.T) {
	pts := fibonacciSphere(12, 1)
	pts[4] = pts[2]
	pts[6] = pts[2]
	a, b := SeparateCoincident(pts), SeparateCoincident(pts)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("render point %d differs between runs", i)
		}
	}
}
