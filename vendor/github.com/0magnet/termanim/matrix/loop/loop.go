// Package loop is the code rain made exactly periodic: frame t and frame
// t+Period are the same, cell for cell. It is for a video or a GIF that loops,
// where matrix.Matrix's simulation would jump at the seam because it never
// comes back to where it started.
//
// Every stream moves at a speed that carries it round its track a whole number
// of times in one period, and every glyph change is a hash of the beat it
// falls on within the period. The alphabet, the palette, the one in five hot
// heads, the falloff and the change every third step are the simulation's.
//
// A Rain satisfies backdrop.Rain, so a backdrop.Frame and its mask fill from
// it the way they fill from a matrix.Matrix.
package loop

import (
	"math"
	"math/rand"

	"github.com/0magnet/termanim/canvas"
	"github.com/0magnet/termanim/matrix"
)

// ChangeEvery is the steps a glyph holds before it may change, as in
// matrix.Matrix. Period is a multiple of it.
const ChangeEvery = 3

// changeOneIn is the chance a cell takes a new glyph on a given beat, as a
// reciprocal.
const changeOneIn = 40

// stream runs round a track of track rows (the screen, its own trail to clear
// the bottom, and a gap above it that is the column's idle time) laps times
// in one period.
type stream struct {
	phase  float64 // rows along the track at frame 0
	track  int
	laps   int
	length int // trail length in rows
	hot    bool
	id     uint64
}

// head is where a stream's head is at the frame Seek last set.
type head struct {
	row int
	lap int
	s   *stream
}

// Rain is the periodic rain. It is not safe for concurrent use.
type Rain struct {
	// Glyphs is the alphabet drawn.
	Glyphs []rune
	// Palette runs from the dim tail to the bright head.
	Palette canvas.Palette
	// Head is the intensity of a head that is not hot, 0-255.
	Head int
	// Trail is the dimmest a trail gets, 0-255. Zero fades it to nothing as
	// the simulation does. A small picture wants it higher, or a trail is a
	// column of nothing.
	Trail int

	seed       uint64
	cols, rows int
	period     int
	beats      int
	col        [][]stream
	heads      [][]head
	t          int
}

// New returns a rain of cols by rows that repeats every period steps. The
// period is rounded down to a multiple of ChangeEvery, and is at least that.
// The same seed gives the same rain.
func New(seed int64, cols, rows, period int) *Rain {
	period = max(ChangeEvery, period/ChangeEvery*ChangeEvery)
	cols, rows = max(cols, 0), max(rows, 1)
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec // decoration
	r := &Rain{
		Glyphs:  matrix.Glyphs,
		Palette: canvas.Matrix,
		Head:    200,
		seed:    uint64(seed), //nolint:gosec // a hash input: the wraparound is the mixing, not an overflow
		cols:    cols,
		rows:    rows,
		period:  period,
		beats:   period / ChangeEvery,
		col:     make([][]stream, cols),
		heads:   make([][]head, cols),
	}
	id := uint64(0)
	for x := range cols {
		// Most columns carry one stream at a time, as the simulation's do.
		// Some carry two, so the screen is as full as a terminal's.
		k := 1
		if rng.Intn(3) == 0 {
			k = 2
		}
		for range k {
			length := 6 + rng.Intn(rows)
			track := rows + length + rng.Intn(rows)
			// A speed of the simulation's, 0.25 to 1 rows a step, rounded to
			// one that covers the track a whole number of times in a period.
			want := 0.25 + rng.Float64()*0.75
			laps := max(1, int(math.Round(want*float64(period)/float64(track))))
			id++
			r.col[x] = append(r.col[x], stream{
				phase:  rng.Float64() * float64(track),
				track:  track,
				laps:   laps,
				length: length,
				hot:    rng.Intn(5) == 0,
				id:     id,
			})
		}
	}
	r.Seek(0)
	return r
}

// Period is the steps after which the rain repeats.
func (r *Rain) Period() int { return r.period }

// Size is the grid in cells.
func (r *Rain) Size() (cols, rows int) { return r.cols, r.rows }

// Step is the frame Seek last set, in [0, Period).
func (r *Rain) Step() int { return r.t }

// Seek moves the rain to step t. Any t is allowed and t+Period is the same
// frame as t.
func (r *Rain) Seek(t int) {
	t %= r.period
	if t < 0 {
		t += r.period
	}
	r.t = t
	for x := range r.cols {
		r.heads[x] = r.heads[x][:0]
		for i := range r.col[x] {
			s := &r.col[x][i]
			// Exact at t = period: laps*track is a whole number of tracks.
			d := s.phase + float64(s.laps*s.track*t)/float64(r.period)
			lap := int(d / float64(s.track))
			r.heads[x] = append(r.heads[x], head{row: int(math.Mod(d, float64(s.track))), lap: lap % s.laps, s: s})
		}
	}
}

// CellAt reports what is drawn at x, y and whether anything is.
func (r *Rain) CellAt(x, y int) (matrix.Cell, bool) {
	h, d := r.nearest(x, y)
	if h == nil {
		return matrix.Cell{}, false
	}
	hot := d == 0 && h.s.hot
	var i int
	switch {
	case hot:
		i = 255
	case d == 0:
		i = r.Head
	default:
		i = max(r.Trail, r.Head-d*(r.Head-r.Trail)/h.s.length)
	}
	return matrix.Cell{Rune: r.glyph(x, y, tag(h)), Intensity: i, Hot: hot}, true
}

// GlyphAt is the glyph the rain holds at x, y, whether or not it is lit there.
// Zero outside the grid.
func (r *Rain) GlyphAt(x, y int) rune {
	if x < 0 || y < 0 || x >= r.cols || y >= r.rows {
		return 0
	}
	h, _ := r.nearest(x, y)
	return r.glyph(x, y, tag(h))
}

// Cells reports every lit cell, left to right and top to bottom.
func (r *Rain) Cells(fn func(x, y int, c matrix.Cell)) {
	for y := range r.rows {
		for x := range r.cols {
			if c, ok := r.CellAt(x, y); ok {
				fn(x, y, c)
			}
		}
	}
}

// nearest is the stream whose head is nearest above x, y and within its
// trail, and how far behind the head the cell is. A faster stream that has
// caught up with a slower one is in front of it.
func (r *Rain) nearest(x, y int) (*head, int) {
	if x < 0 || y < 0 || x >= r.cols || y >= r.rows {
		return nil, -1
	}
	var at *head
	dist := -1
	for i := range r.heads[x] {
		h := &r.heads[x][i]
		if d := h.row - y; d >= 0 && d < h.s.length && (dist < 0 || d < dist) {
			at, dist = h, d
		}
	}
	return at, dist
}

// tag names the stream lap lighting a cell, so a glyph changes when a new lap
// of a stream reaches it. Zero for a dark cell.
func tag(h *head) uint64 {
	if h == nil {
		return 0
	}
	return h.s.id<<8 | uint64(h.lap) //nolint:gosec // a hash input: the wraparound is the mixing, not an overflow
}

// glyph is the glyph at x, y now, for the stream lap tag. A cell turns over
// on about one beat in changeOneIn. Which beats is a hash of the beat's place
// in the period, so the turning over repeats with it.
func (r *Rain) glyph(x, y int, tag uint64) rune {
	if len(r.Glyphs) == 0 {
		return ' '
	}
	b := r.t / ChangeEvery
	last := -1
	for k := range r.beats {
		bb := (b - k + r.beats) % r.beats
		if r.hash(uint64(x), uint64(y), uint64(bb), 1)%changeOneIn == 0 { //nolint:gosec // a hash input: the wraparound is the mixing, not an overflow
			last = bb
			break
		}
	}
	return r.Glyphs[r.hash(uint64(x), uint64(y), tag, uint64(last+1))%uint64(len(r.Glyphs))] //nolint:gosec // a hash input: the wraparound is the mixing, not an overflow
}

// hash mixes the seed and four values with splitmix64's finalizer. It runs
// for every cell of every frame, so it must not allocate.
func (r *Rain) hash(a, b, c, d uint64) uint64 {
	h := r.seed
	for _, v := range [4]uint64{a, b, c, d} {
		h = mix(h ^ v)
	}
	return h
}

func mix(z uint64) uint64 {
	z += 0x9e3779b97f4a7c15
	z = (z ^ z>>30) * 0xbf58476d1ce4e5b9
	z = (z ^ z>>27) * 0x94d049bb133111eb
	return z ^ z>>31
}
