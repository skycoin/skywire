package backdrop

import (
	"github.com/0magnet/termanim/canvas"
)

// RenderAnim returns text drawn over one frame of a pixel animation.
//
// It is Render with something other than the rain behind it. Every pixel
// animation in this repository qualifies, and so does anything else
// implementing canvas.Animation:
//
//	fmt.Print(backdrop.RenderAnim(help, plasma.New(), backdrop.Options{}))
//
// The Options mean what they mean everywhere else, with two exceptions worth
// knowing. Steps is the rain's and is ignored; Warm is what takes its place.
// And Dim scales the colors themselves rather than a position along a ramp,
// because a surface is already the picture — see Frame.FromSurface.
//
// A word about which animations suit this. The rain is mostly gaps, which is
// what makes it read as something behind the text; a plasma or a fire covers
// every cell, so the help ends up on a solid field. GapMin and Dim are the two
// knobs for that, and something sparse — a starfield, fireworks — needs
// neither.
func RenderAnim(text string, a canvas.Animation, o Options) string {
	if o.Off {
		return text
	}
	if !o.Force && !colorOK() {
		return text
	}

	l := layout(text, o)
	s := canvas.NewSurface(l.cols, l.rows*2)
	a.Resize(l.cols, l.rows*2)
	warmUp(a, s, o.Warm)

	f := NewFrame(l.cols, l.rows)
	f.FromSurface(s, l.dim)
	return paint(f, l, o)
}

// warmUp runs an animation in far enough that it has something to show.
//
// The time is handed over in sixtieths rather than in one lump because these
// advance by elapsed time and several of them integrate: a single step of one
// second would send every boid a second's flight in a straight line and every
// spark of a firework off the screen.
//
// One frame is always drawn, however little warming was asked for. The rain
// can be left unadvanced and still fill the screen because its buffer is
// seeded before anything moves; a surface has nothing on it at all until the
// animation has drawn once, so "no warm-up" has to mean the opening frame
// rather than a blank screen.
func warmUp(a canvas.Animation, s *canvas.Surface, secs float64) {
	if secs == 0 {
		secs = 1
	}
	const step = 1.0 / 60
	for t := 0.0; ; t += step {
		a.Frame(s, step)
		if t+step >= secs {
			return
		}
	}
}
