package backdrop

import (
	"math/rand"
	"time"

	"github.com/0magnet/termanim/canvas"
	"github.com/0magnet/termanim/matrix"
)

// Painter draws text over a backdrop that is moving rather than over a still.
//
// Render is the whole of what a help screen needs: it is printed once, so the
// frame behind it can be generated, used and thrown away. A full-screen
// program is the other case — it redraws many times a second and the backdrop
// should be moving between one redraw and the next, which means the simulation
// has to live somewhere across frames. That is what this is.
//
// It suits a bubbletea View in particular, where a frame is a string: compose
// the screen with whatever does the layout, hand the result here, and the
// backdrop fills every cell the text did not use. Pad: -1 is usually what such
// a caller wants, since it has already decided where everything goes.
//
// New gives the code rain. NewFor gives any of the pixel animations in this
// repository instead.
//
// Not safe for concurrent use. A View is called from one place.
type Painter struct {
	o Options

	// Exactly one of these is set: m for the rain, a for a pixel animation.
	// They are the two shapes an animation in this repository comes in, and
	// they reach the compositor through the same Frame.
	m *matrix.Matrix
	a canvas.Animation

	// surf is where a is drawn, at two pixel rows to the cell row. Nil for
	// the rain, which is made of glyphs and has no pixels to draw into.
	surf *canvas.Surface

	// f is refilled each frame rather than reallocated. It is the largest
	// thing in a loop that runs many times a second.
	f *Frame

	// cols and rows are what the backdrop was last sized to. A terminal that
	// has been resized gets a new simulation rather than a stretched one: the
	// rain's columns are per-column state and there is no meaningful way to
	// widen them.
	cols, rows int

	last time.Time
}

// New returns a painter that draws text over the code rain. The options are
// the same ones Render takes, and mean the same things; Width and Pad are the
// two worth setting for a full-screen caller, which usually wants its own
// width and no padding at all.
func New(o Options) *Painter { return &Painter{o: o} }

// NewFor returns a painter that draws text over a pixel animation.
//
//	p := backdrop.NewFor(plasma.New(), backdrop.Options{Pad: -1, GapMin: 4})
//	...
//	fmt.Print(p.Tick(screen))
//
// The animation is driven from here and should not also be handed to
// canvas.Run — there is one surface and one clock, and this owns both.
//
// See RenderAnim on which animations suit a backdrop: the rain is mostly gaps,
// and one that covers every cell puts the text on a solid field. Dim and
// GapMin are the two knobs for that.
func NewFor(a canvas.Animation, o Options) *Painter { return &Painter{o: o, a: a} }

// Frame advances the backdrop by dt seconds and returns text drawn over it.
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

	if p.f == nil || p.cols != s.cols || p.rows != s.rows {
		p.reset(s)
	} else if dt > 0 {
		p.advance(dt)
	}
	p.fill(s)
	return paint(p.f, s, p.o)
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

// reset builds a backdrop for a grid this size and runs it in far enough that
// the screen is full. Starting from nothing would open on an empty screen and
// fill over the first second, which reads as the program still loading.
func (p *Painter) reset(s sheet) {
	p.cols, p.rows = s.cols, s.rows
	p.f = NewFrame(s.cols, s.rows)

	if p.a != nil {
		p.surf = canvas.NewSurface(s.cols, s.rows*2)
		p.a.Resize(s.cols, s.rows*2)
		warmUp(p.a, p.surf, p.o.Warm)
		return
	}

	p.m = matrix.New(s.seed)
	p.m.Resize(s.cols, s.rows)

	steps := p.o.Steps
	if steps == 0 {
		rng := rand.New(rand.NewSource(s.seed)) //nolint:gosec
		steps = 3*s.rows + rng.Intn(4*s.rows+40)
	}
	p.m.Advance(steps)
}

// advance moves the backdrop on by dt seconds.
func (p *Painter) advance(dt float64) {
	if p.a != nil {
		p.a.Frame(p.surf, dt)
		return
	}
	p.m.AdvanceTime(dt)
}

// fill reads the current state of the backdrop into the frame the compositor
// works from. It is the whole of what the two kinds of animation have to
// agree on.
func (p *Painter) fill(s sheet) {
	if p.a != nil {
		p.f.FromSurface(p.surf, s.dim)
		return
	}
	p.f.FromMatrix(p.m, s.dim)
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
// The next Frame rebuilds the backdrop if this changed it.
func (p *Painter) SetWidth(w int) { p.o.Width = w }

// Matrix is the running rain, for a caller that wants to tune it — the
// palette, the density, the speed. It is nil until the first Frame, since
// there is nothing to size it to before then, and nil for the whole life of a
// painter made by NewFor, which has an animation behind the text and no rain.
func (p *Painter) Matrix() *matrix.Matrix { return p.m }

// Animation is the animation a NewFor painter is drawing, and nil for one
// drawing the rain. It is the same value that was passed in, returned for
// symmetry with Matrix.
func (p *Painter) Animation() canvas.Animation { return p.a }

// Surface is where a NewFor painter's animation draws, and nil for one drawing
// the rain or before the first Frame.
//
// It is here for a caller that wants to put something on the backdrop itself
// rather than in the text over it — a highlight under the cursor, a wash
// behind a pane. Draw into it after Frame has returned and it lands on the
// next one.
func (p *Painter) Surface() *canvas.Surface { return p.surf }
