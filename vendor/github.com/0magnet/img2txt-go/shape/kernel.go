package shape

import "math"

// Kernel is the sampling circles laid over a grid of whole pixels, sx by sy
// to a cell, with the bilinear weights of every sample point collapsed into
// one weight per pixel. Sampling a cell is then a short weighted sum per
// circle instead of hundreds of interpolated lookups, which is what makes
// shape matching affordable for every cell of an animation every frame.
type Kernel struct {
	sx, sy int
	taps   [16][]tap // the six internal circles, then the ten external ones

	// The extent of every tap, so a cell whose taps all land inside the grid
	// can skip clamping each one.
	minX, minY, maxX, maxY int
}

type tap struct {
	dx, dy int // pixel offset from the cell's top-left pixel
	w      float64
}

// NewKernel builds the kernel for cells of sx by sy pixels. Both must be at
// least 1.
func NewKernel(sx, sy int) *Kernel {
	k := &Kernel{sx: sx, sy: sy}
	centers := append(Internal[:], External[:]...)
	for c, p := range centers {
		acc := map[[2]int]float64{}
		for _, o := range offsets {
			// Cell coordinates to pixel coordinates, where pixel i's center
			// is at i+0.5.
			x := (p.X+o.X*RadiusX)*float64(sx) - 0.5
			y := (p.Y+o.Y*RadiusY)*float64(sy) - 0.5
			x0, y0 := math.Floor(x), math.Floor(y)
			fx, fy := x-x0, y-y0
			ix, iy := int(x0), int(y0)
			w := 1 / float64(len(offsets))
			acc[[2]int{ix, iy}] += w * (1 - fx) * (1 - fy)
			acc[[2]int{ix + 1, iy}] += w * fx * (1 - fy)
			acc[[2]int{ix, iy + 1}] += w * (1 - fx) * fy
			acc[[2]int{ix + 1, iy + 1}] += w * fx * fy
		}
		// In a fixed order, so the sum is the same every run.
		for dy := -2 * sy; dy <= 3*sy; dy++ {
			for dx := -2 * sx; dx <= 3*sx; dx++ {
				if w := acc[[2]int{dx, dy}]; w > 1e-9 {
					k.taps[c] = append(k.taps[c], tap{dx, dy, w})
					k.minX, k.maxX = min(k.minX, dx), max(k.maxX, dx)
					k.minY, k.maxY = min(k.minY, dy), max(k.maxY, dy)
				}
			}
		}
	}
	return k
}

// Cell reports the kernel's cell size in pixels.
func (k *Kernel) Cell() (sx, sy int) { return k.sx, k.sy }

// Sample measures cell (cx,cy) of a w by h grid of ink values, row-major,
// each 0 to 1. Pixels beyond the grid's edge repeat the nearest edge pixel.
func (k *Kernel) Sample(ink []float64, w, h, cx, cy int) (in Vector, ext [10]float64) {
	x0, y0 := cx*k.sx, cy*k.sy
	inside := x0+k.minX >= 0 && y0+k.minY >= 0 && x0+k.maxX < w && y0+k.maxY < h
	base := y0*w + x0
	sum := func(taps []tap) float64 {
		var s float64
		if inside {
			for _, t := range taps {
				s += t.w * ink[base+t.dy*w+t.dx]
			}
			return s
		}
		for _, t := range taps {
			x := min(max(x0+t.dx, 0), w-1)
			y := min(max(y0+t.dy, 0), h-1)
			s += t.w * ink[y*w+x]
		}
		return s
	}
	for i := range in {
		in[i] = sum(k.taps[i])
	}
	for i := range ext {
		ext[i] = sum(k.taps[len(in)+i])
	}
	return in, ext
}

// Matcher enhances a sampled cell and finds its glyph, remembering the answer
// for each enhanced vector quantized to five bits a component, as the
// article does. Two vectors that quantize alike get the same glyph, so the
// cache trades a little precision for not searching the table again.
type Matcher struct {
	Table   []Entry
	Options Options
	cache   map[uint32]rune
}

// NewMatcher returns a matcher over table with the given enhancement.
func NewMatcher(table []Entry, o Options) *Matcher {
	return &Matcher{Table: table, Options: o, cache: map[uint32]rune{}}
}

// maxCache bounds the cache. Past it the cache starts over rather than
// growing without limit on a picture that never repeats itself.
const maxCache = 1 << 16

// Pick returns the glyph for a sampled cell.
func (m *Matcher) Pick(in Vector, ext [10]float64) rune {
	v := Enhance(in, ext, m.Options)
	var key uint32
	for i := range v {
		q := uint32(min(31, max(0, v[i]*32)))
		key = key<<5 | q
	}
	if r, ok := m.cache[key]; ok {
		return r
	}
	r := Match(m.Table, v)
	if len(m.cache) >= maxCache {
		clear(m.cache)
	}
	m.cache[key] = r
	return r
}
