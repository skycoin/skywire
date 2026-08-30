// Package latency pkg/tpviz/latency/voronoi.go c4-vis-latency
//
// Spherical Voronoi over the embedded points.
//
// For points that all lie on a sphere the Delaunay triangulation IS the
// convex hull, so the cells come from one hull computation: each hull
// triangle has a circumcenter on the sphere, and the cell of a point is
// the polygon through the circumcenters of the triangles that touch it,
// taken in angular order around the point.
//
// This is not the same shape as the group-boundary overlay's circles.
// Cells tessellate: they share edges, cover the whole sphere, and every
// point of the sphere belongs to exactly one of them.
package latency

import (
	"math"
	"sort"
)

// Cell is one visor's territory: a closed spherical polygon whose
// vertices are unit vectors, in order around the site.
type Cell struct {
	Site    int
	Polygon []Vec3
}

// Triangle is one face of the hull, by point index, wound so its normal
// points away from the origin.
type Triangle struct{ A, B, C int }

// Voronoi returns one cell per point.
//
// Coincident sites are merged before the hull is built. Two visors with
// the same latency profile embed to the same place, which happens for
// real (two visors on one machine see the same peers at the same RTT),
// and a duplicated site makes the hull non-manifold: the visibility
// predicate cannot be consistent about a zero-area face, so the face
// list grows without bound. Merged sites share the representative cell,
// which is what sharing a position means.
func Voronoi(pts []Vec3) []Cell {
	uniq, rep := dedupeSites(pts)
	if len(uniq) < 4 {
		return nil
	}
	cells := voronoiUnique(uniq)
	if cells == nil {
		return nil
	}
	out := make([]Cell, len(pts))
	for i := range pts {
		out[i] = Cell{Site: i, Polygon: cells[rep[i]].Polygon}
	}
	return out
}

// dedupeSites merges points closer than dedupeEps radians. Returns the
// unique positions and, for every input index, the unique index it maps
// to.
func dedupeSites(pts []Vec3) (uniq []Vec3, rep []int) {
	rep = make([]int, len(pts))
	for i, p := range pts {
		found := -1
		for j, u := range uniq {
			if Angle(p, u) < dedupeEps {
				found = j
				break
			}
		}
		if found < 0 {
			found = len(uniq)
			uniq = append(uniq, p)
		}
		rep[i] = found
	}
	return uniq, rep
}

// dedupeEps is well below the smallest separation that could render as
// two distinct cells, and well above float64 noise.
const dedupeEps = 1e-7

func voronoiUnique(pts []Vec3) []Cell {
	tris := Delaunay(pts)
	if len(tris) == 0 {
		return nil
	}
	// Circumcenter of each face, on the sphere: the normalized normal of
	// the triangle's plane, which is equidistant from all three vertices.
	cc := make([]Vec3, len(tris))
	for i, t := range tris {
		n, ok := normalize(cross(sub(pts[t.B], pts[t.A]), sub(pts[t.C], pts[t.A])))
		if !ok {
			// Degenerate face; fall back to the centroid direction so the
			// cell stays closed instead of losing a vertex.
			n, _ = normalize(Vec3{
				(pts[t.A].X + pts[t.B].X + pts[t.C].X) / 3,
				(pts[t.A].Y + pts[t.B].Y + pts[t.C].Y) / 3,
				(pts[t.A].Z + pts[t.B].Z + pts[t.C].Z) / 3,
			})
		}
		cc[i] = n
	}
	// Faces incident to each point.
	inc := make([][]int, len(pts))
	for i, t := range tris {
		inc[t.A] = append(inc[t.A], i)
		inc[t.B] = append(inc[t.B], i)
		inc[t.C] = append(inc[t.C], i)
	}
	cells := make([]Cell, len(pts))
	for i := range pts {
		cells[i] = Cell{Site: i}
		if len(inc[i]) < 3 {
			continue
		}
		cells[i].Polygon = orderAround(pts[i], cc, inc[i])
	}
	return cells
}

// orderAround sorts the circumcenters into a closed ring as seen from
// the site, by angle in the site's tangent plane.
func orderAround(site Vec3, cc []Vec3, faces []int) []Vec3 {
	u, ok := normalize(orthogonal(site))
	if !ok {
		return nil
	}
	v := cross(site, u)
	type av struct {
		ang float64
		p   Vec3
	}
	ring := make([]av, 0, len(faces))
	for _, f := range faces {
		p := cc[f]
		ring = append(ring, av{ang: math.Atan2(dot(p, v), dot(p, u)), p: p})
	}
	sort.Slice(ring, func(a, b int) bool { return ring[a].ang < ring[b].ang })
	out := make([]Vec3, len(ring))
	for i, r := range ring {
		out[i] = r.p
	}
	return out
}

// Delaunay returns the spherical Delaunay triangulation, computed as the
// 3D convex hull of the points. Incremental construction: seed with a
// tetrahedron, then add each point by removing the faces it can see and
// stitching a cone to the horizon.
func Delaunay(pts []Vec3) []Triangle {
	n := len(pts)
	if n < 4 {
		return nil
	}
	b, c, d, ok := seedTetrahedron(pts)
	const a = 0
	if !ok {
		return nil
	}
	faces := []Triangle{}
	addSeed := func(x, y, z int) {
		t := Triangle{x, y, z}
		// Wind so the normal faces away from the interior point d.
		if visible(pts, t, pts[d]) {
			t = Triangle{x, z, y}
		}
		faces = append(faces, t)
	}
	interior := Vec3{
		(pts[a].X + pts[b].X + pts[c].X + pts[d].X) / 4,
		(pts[a].Y + pts[b].Y + pts[c].Y + pts[d].Y) / 4,
		(pts[a].Z + pts[b].Z + pts[c].Z + pts[d].Z) / 4,
	}
	_ = interior
	for _, t := range [][3]int{{a, b, c}, {a, b, d}, {a, c, d}, {b, c, d}} {
		var opp int
		switch {
		case t[0] != a && t[1] != a && t[2] != a:
			opp = a
		case t[0] != b && t[1] != b && t[2] != b:
			opp = b
		case t[0] != c && t[1] != c && t[2] != c:
			opp = c
		default:
			opp = d
		}
		tri := Triangle{t[0], t[1], t[2]}
		if visible(pts, tri, pts[opp]) {
			tri = Triangle{t[0], t[2], t[1]}
		}
		faces = append(faces, tri)
	}
	_ = addSeed

	seeded := map[int]bool{a: true, b: true, c: true, d: true}
	for i := 0; i < n; i++ {
		if seeded[i] {
			continue
		}
		p := pts[i]
		// Faces this point can see are no longer on the hull.
		keep := faces[:0:0]
		var horizon [][2]int
		for _, f := range faces {
			if visible(pts, f, p) {
				horizon = append(horizon, [2]int{f.A, f.B}, [2]int{f.B, f.C}, [2]int{f.C, f.A})
				continue
			}
			keep = append(keep, f)
		}
		if len(horizon) == 0 {
			continue // inside the hull; impossible on a sphere but cheap to guard
		}
		// An edge shared by two removed faces is interior to the removed
		// patch; only unpaired edges form the horizon.
		count := map[[2]int]int{}
		for _, e := range horizon {
			k := e
			if k[0] > k[1] {
				k[0], k[1] = k[1], k[0]
			}
			count[k]++
		}
		for _, e := range horizon {
			k := e
			if k[0] > k[1] {
				k[0], k[1] = k[1], k[0]
			}
			if count[k] != 1 {
				continue
			}
			keep = append(keep, Triangle{e[0], e[1], i})
		}
		faces = keep
	}
	return faces
}

// visible reports whether p lies on the outer side of the face plane.
//
// The tolerance is RELATIVE to the face size. An absolute epsilon makes
// the predicate inconsistent for small faces, and an inconsistent
// predicate builds a non-manifold hull whose face list then grows
// without bound.
func visible(pts []Vec3, t Triangle, p Vec3) bool {
	nrm := cross(sub(pts[t.B], pts[t.A]), sub(pts[t.C], pts[t.A]))
	scale := norm(nrm)
	if scale < 1e-15 {
		return false
	}
	return dot(nrm, sub(p, pts[t.A])) > 1e-9*scale
}

// seedTetrahedron picks four points that are not coplanar.
func seedTetrahedron(pts []Vec3) (b, c, d int, ok bool) {
	n := len(pts)
	const a = 0
	for b = 1; b < n; b++ {
		if norm(sub(pts[b], pts[a])) > 1e-9 {
			break
		}
	}
	if b >= n {
		return
	}
	for c = b + 1; c < n; c++ {
		if norm(cross(sub(pts[b], pts[a]), sub(pts[c], pts[a]))) > 1e-9 {
			break
		}
	}
	if c >= n {
		return
	}
	nrm := cross(sub(pts[b], pts[a]), sub(pts[c], pts[a]))
	for d = c + 1; d < n; d++ {
		if math.Abs(dot(nrm, sub(pts[d], pts[a]))) > 1e-9 {
			return b, c, d, true
		}
	}
	return
}

func sub(a, b Vec3) Vec3    { return Vec3{a.X - b.X, a.Y - b.Y, a.Z - b.Z} }
func dot(a, b Vec3) float64 { return a.X*b.X + a.Y*b.Y + a.Z*b.Z }
func norm(a Vec3) float64   { return math.Sqrt(dot(a, a)) }
func cross(a, b Vec3) Vec3 {
	return Vec3{a.Y*b.Z - a.Z*b.Y, a.Z*b.X - a.X*b.Z, a.X*b.Y - a.Y*b.X}
}

// SeparateCoincident returns render positions in which no two points
// share a location.
//
// The embedding legitimately places two visors at the same point when
// their latency profiles are identical, which happens whenever one
// machine runs several visors: they see the same peers at the same RTT,
// so latency space cannot tell them apart. That is the right answer for
// the Voronoi, where a shared position means a shared territory, but the
// wrong one for drawing: cosmos-go would render four visors as one pixel
// and three of them would have no hover, no click and no way to know
// they exist.
//
// Coincident points are spread around a ring far smaller than any cell,
// deterministically, so the same network draws the same way for every
// viewer. The Voronoi should be computed from the ORIGINAL positions,
// not these.
func SeparateCoincident(pts []Vec3) []Vec3 {
	out := make([]Vec3, len(pts))
	copy(out, pts)
	_, rep := dedupeSites(pts)

	groups := make(map[int][]int)
	for i, r := range rep {
		groups[r] = append(groups[r], i)
	}
	for _, members := range groups {
		if len(members) < 2 {
			continue
		}
		// Members come from a map value built in index order, so the ring
		// is stable without sorting.
		center := out[members[0]]
		u, ok := normalize(orthogonal(center))
		if !ok {
			continue
		}
		v := cross(center, u)
		for k, idx := range members {
			ang := 2 * math.Pi * float64(k) / float64(len(members))
			dir := Vec3{
				u.X*math.Cos(ang) + v.X*math.Sin(ang),
				u.Y*math.Cos(ang) + v.Y*math.Sin(ang),
				u.Z*math.Cos(ang) + v.Z*math.Sin(ang),
			}
			p := Vec3{
				center.X + dir.X*coincidentSpread,
				center.Y + dir.Y*coincidentSpread,
				center.Z + dir.Z*coincidentSpread,
			}
			if np, ok := normalize(p); ok {
				out[idx] = np
			}
		}
	}
	return out
}

// coincidentSpread is the ring radius in radians. Well below the 1.5e-3
// first-percentile nearest-neighbor separation measured on the live
// network, so separated points never cross into a neighbor's cell, and
// well above dedupeEps so they are genuinely distinct.
const coincidentSpread = 1e-4
