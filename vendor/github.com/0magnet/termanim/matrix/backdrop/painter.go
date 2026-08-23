package backdrop

import (
	"math/rand"
	"time"

	"github.com/0magnet/termanim/matrix"
)

// Painter draws text over rain that is falling rather than over a still.
//
// Render is the whole of what a help screen needs: it is printed once, so the
// frame behind it can be generated, used and thrown away. A full-screen
// program is the other case — it redraws many times a second and the rain
// should be moving between one redraw and the next, which means the simulation
// has to live somewhere across frames. That is what this is.
//
// It suits a bubbletea View in particular, where a frame is a string: compose
// the screen with whatever does the layout, hand the result here, and the rain
// fills every cell the text did not use. Pad: -1 is usually what such a caller
// wants, since it has already decided where everything goes.
//
// Not safe for concurrent use. A View is called from one place.
type Painter struct {
	o Options
	m *matrix.Matrix

	// cols and rows are what m was last sized to. A terminal that has been
	// resized gets a new simulation rather than a stretched one: the columns
	// are per-column state and there is no meaningful way to widen them.
	cols, rows int

	last time.Time
}

// New returns a painter. The options are the same ones Render takes, and mean
// the same things; Width and Pad are the two worth setting for a full-screen
// caller, which usually wants its own width and no padding at all.
func New(o Options) *Painter { return &Painter{o: o} }

// Frame advances the rain by dt seconds and returns text drawn over it.
//
// A dt of zero asks for the frame as it stands, redrawn without moving — which
// is what a resize or a keypress wants, as against a tick.
func (p *Painter) Frame(text string, dt float64) string {
	if p.o.Off {
		return text
	}
	if !p.o.Force && !colorOK() {
		return text
	}

	s := layout(text, p.o)

	if p.m == nil || p.cols != s.cols || p.rows != s.rows {
		p.reset(s)
	} else if dt > 0 {
		p.m.AdvanceTime(dt)
	}
	return paint(p.m, s, p.o)
}

// Tick is Frame with dt taken from the clock, for a caller that is driving
// from a timer and does not want to work out the interval itself. The first
// call starts the clock and does not advance anything.
func (p *Painter) Tick(text string) string {
	now := time.Now()
	dt := 0.0
	if !p.last.IsZero() {
		dt = now.Sub(p.last).Seconds()
	}
	p.last = now
	return p.Frame(text, dt)
}

// reset builds a simulation for a grid this size and runs it in far enough
// that the screen is full. Starting from nothing would open on an empty screen
// and fill from the top over the first second, which reads as the program
// still loading.
func (p *Painter) reset(s sheet) {
	p.m = matrix.New(s.seed)
	p.m.Resize(s.cols, s.rows)
	p.cols, p.rows = s.cols, s.rows

	steps := p.o.Steps
	if steps == 0 {
		rng := rand.New(rand.NewSource(s.seed))
		steps = 3*s.rows + rng.Intn(4*s.rows+40)
	}
	p.m.Advance(steps)
}

// SetWidth tells the painter how wide the screen is.
//
// Worth doing rather than leaving to the default, for anything that is told
// its size rather than having to ask. A TUI framework hands its program the
// width and that is the authority; the terminal's own idea of it is a fallback
// for a caller that has nothing better, and the two part company the moment
// output is not the terminal — at which point every row of a screen composed
// at the real width is wider than the painter thinks the screen is, and is
// passed through untouched as an over-wide line.
//
// The next Frame rebuilds the simulation if this changed it.
func (p *Painter) SetWidth(w int) { p.o.Width = w }

// Matrix is the running simulation, for a caller that wants to tune it — the
// palette, the density, the speed. It is nil until the first Frame, since
// there is nothing to size it to before then.
func (p *Painter) Matrix() *matrix.Matrix { return p.m }
