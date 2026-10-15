// Package shape picks a character for a cell by the shape of what is in it,
// rather than by how bright it is.
//
// It implements the method Alex Harri describes in "ASCII characters are not
// pixels: a deep dive into ASCII rendering"
// (https://alexharri.com/blog/ascii-rendering). A cell is sampled in six
// circles, two columns by three rows, giving a six-component shape vector.
// Every glyph has a shape vector of its own, measured once from a real font,
// and the glyph whose vector is nearest wins. Contrast enhancement, global and
// directional, pushes the cell's vector toward the corners of that space so
// edges come out crisp.
//
// The glyph vectors in ASCII are measured from libcaca's built-in
// "Monospace 9" bitmap font, the one img2txt-go already embeds for TGA
// export. They are generated into table.go by `go generate`, so this package
// has no font and no dependencies at run time, and a test in package caca
// re-measures the font to prove the table still matches it.
//
// The article gives the mapping from external to internal circles but not
// their coordinates; the positions below are this package's own.
package shape

//go:generate go run ./internal/gentable

import "math"

// Vector is ink coverage in the six sampling circles, row-major:
//
//	0 1
//	2 3
//	4 5
//
// Each component is 0 for no ink and 1 for full ink.
type Vector [6]float64

// Point is a position in cell coordinates: (0,0) is the cell's top-left
// corner and (1,1) its bottom-right.
type Point struct{ X, Y float64 }

// Entry is one glyph and its normalized shape vector.
type Entry struct {
	R rune
	V Vector
}

// Aspect is the width/height ratio of the cell the circles are laid out in:
// Monospace 9 is 7x15. Circles are round in pixels, so elliptical in cell
// coordinates.
const Aspect = 7.0 / 15.0

const (
	// RadiusX is a sampling circle's radius as a fraction of cell width.
	RadiusX = 0.3
	// RadiusY is the same radius as a fraction of cell height.
	RadiusY = RadiusX * Aspect
	// stagger lowers the left circles and raises the right ones, which covers
	// the cell with fewer gaps (the article's suggestion).
	stagger = 0.03
)

// Internal are the centers of the six sampling circles inside the cell.
var Internal = [6]Point{
	{0.25, 1.0/6 + stagger}, {0.75, 1.0/6 - stagger},
	{0.25, 3.0/6 + stagger}, {0.75, 3.0/6 - stagger},
	{0.25, 5.0/6 + stagger}, {0.75, 5.0/6 - stagger},
}

// External are the centers of the ten circles that reach into the
// neighboring cells: two above, three left, three right and two below.
//
//	   0   1
//	2         3
//	4         5
//	6         7
//	   8   9
var External = [10]Point{
	{0.25, -1.0 / 6}, {0.75, -1.0 / 6},
	{-0.25, 1.0/6 + stagger}, {1.25, 1.0/6 - stagger},
	{-0.25, 3.0/6 + stagger}, {1.25, 3.0/6 - stagger},
	{-0.25, 5.0/6 + stagger}, {1.25, 5.0/6 - stagger},
	{0.25, 7.0 / 6}, {0.75, 7.0 / 6},
}

// affecting lists, for each internal circle, the external circles whose value
// can pull it down. This is the article's AFFECTING_EXTERNAL_INDICES.
var affecting = [6][]int{
	{0, 1, 2, 4},
	{0, 1, 3, 5},
	{2, 4, 6},
	{3, 5, 7},
	{4, 6, 8, 9},
	{5, 7, 8, 9},
}

// Options are the two contrast-enhancement exponents. 1 disables a stage.
type Options struct {
	// Directional pulls each component down relative to the brightest of the
	// neighboring external circles that affect it, which sharpens edges
	// between cells.
	Directional float64
	// Global pulls each component down relative to the cell's own maximum,
	// which sharpens edges inside the cell.
	Global float64
}

// Default are the exponents img2txt's -s option uses.
var Default = Options{Directional: 3, Global: 2}

// offsets are the sample points inside a unit circle: the center, a ring of
// six and a ring of twelve. Fixed, so the output is deterministic.
var offsets = func() []Point {
	p := []Point{{0, 0}}
	for _, ring := range []struct {
		n int
		r float64
	}{{6, 0.5}, {12, 0.9}} {
		for i := 0; i < ring.n; i++ {
			a := 2 * math.Pi * (float64(i) + 0.5) / float64(ring.n)
			p = append(p, Point{ring.r * math.Cos(a), ring.r * math.Sin(a)})
		}
	}
	return p
}()

// circle averages ink over one sampling circle centered at c.
func circle(ink func(x, y float64) float64, c Point) float64 {
	var s float64
	for _, o := range offsets {
		s += ink(c.X+o.X*RadiusX, c.Y+o.Y*RadiusY)
	}
	return s / float64(len(offsets))
}

// Sample measures a cell. ink is called with cell coordinates, which run
// outside [0,1] for the external circles, and returns ink coverage in [0,1].
func Sample(ink func(x, y float64) float64) (in Vector, ext [10]float64) {
	for i, c := range Internal {
		in[i] = circle(ink, c)
	}
	for i, c := range External {
		ext[i] = circle(ink, c)
	}
	return in, ext
}

// enhance applies v = (v/m)^e * m, the article's contrast curve.
func enhance(v, m, e float64) float64 {
	if m <= 0 || e == 1 {
		return v
	}
	x := v / m
	// The default exponents are whole numbers, and multiplying is several
	// times cheaper than math.Pow in a loop that runs twelve times a cell.
	switch e {
	case 2:
		return x * x * m
	case 3:
		return x * x * x * m
	}
	return math.Pow(x, e) * m
}

// Enhance applies directional and then global contrast enhancement.
func Enhance(in Vector, ext [10]float64, o Options) Vector {
	out := in
	for i, v := range in {
		m := v
		for _, j := range affecting[i] {
			if e := ext[j]; e > m {
				m = e
			}
		}
		out[i] = enhance(v, m, o.Directional)
	}
	m := 0.0
	for _, v := range out {
		m = max(m, v)
	}
	for i, v := range out {
		out[i] = enhance(v, m, o.Global)
	}
	return out
}

// Match returns the glyph in table whose vector is nearest to v, by squared
// Euclidean distance. Ties go to the earlier entry.
func Match(table []Entry, v Vector) rune {
	best, bestD := ' ', math.Inf(1)
	for _, e := range table {
		var d float64
		for i := range v {
			t := e.V[i] - v[i]
			d += t * t
		}
		if d < bestD {
			best, bestD = e.R, d
		}
	}
	return best
}

// Pick samples a cell, enhances it and returns the nearest ASCII glyph.
func Pick(ink func(x, y float64) float64, o Options) rune {
	in, ext := Sample(ink)
	return Match(ASCII, Enhance(in, ext, o))
}

// Measure computes a glyph's raw shape vector from its bitmap. alpha returns
// the coverage, 0 to 1, of the pixel at (x,y) in a w-by-h glyph. Each circle
// is supersampled on a fine grid, so the result is close to true area
// coverage rather than depending on where a handful of points happen to land.
func Measure(w, h int, alpha func(x, y int) float64) Vector {
	const n = 64
	var v Vector
	for i, c := range Internal {
		var ink, cnt float64
		for gy := 0; gy < n; gy++ {
			dy := (float64(gy)+0.5)/n*2 - 1
			for gx := 0; gx < n; gx++ {
				dx := (float64(gx)+0.5)/n*2 - 1
				if dx*dx+dy*dy > 1 {
					continue
				}
				cnt++
				x, y := c.X+dx*RadiusX, c.Y+dy*RadiusY
				if x < 0 || y < 0 || x >= 1 || y >= 1 {
					continue
				}
				ink += alpha(int(x*float64(w)), int(y*float64(h)))
			}
		}
		v[i] = ink / cnt
	}
	return v
}

// Normalize divides each component by its maximum across vs, so every
// dimension of the glyph set spans [0,1], as the article does.
func Normalize(vs []Vector) {
	var m Vector
	for _, v := range vs {
		for i, c := range v {
			m[i] = math.Max(m[i], c)
		}
	}
	for k := range vs {
		for i := range vs[k] {
			if m[i] > 0 {
				vs[k][i] /= m[i]
			}
		}
	}
}
