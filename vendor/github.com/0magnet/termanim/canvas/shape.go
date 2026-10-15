package canvas

import (
	"github.com/gdamore/tcell/v3"

	"github.com/0magnet/img2txt-go/shape"
)

// Shape rendering draws a pixel animation as plain ASCII characters chosen by
// shape, the method in Alex Harri's "ASCII characters are not pixels"
// (https://alexharri.com/blog/ascii-rendering).
//
// The animation is not changed at all. It is handed a surface ShapeCellW by
// ShapeCellH times the size of the terminal instead of 1 by 2, so each cell
// covers a small patch of pixels rather than two of them. Each patch is
// sampled in six circles, and the printable ASCII character whose own six
// samples are nearest is drawn, in the patch's color, on the terminal's
// background. Edges come out as / \ | _ and their relatives rather than as
// stair-steps of blocks.
//
// Like Braille this is a trade, not an upgrade. A cell carries one color and
// no background, so a smooth color field such as plasma loses most of its
// color, while anything drawn as lines and edges on black — a wireframe, a
// maze, a starfield — reads as characters the way hand-made ASCII art does.
// It also costs more: the animation computes ShapeCellW*ShapeCellH/2 times as
// many pixels, and each cell is matched against the glyph table, though the
// matcher remembers its answers.
//
// The matcher and its glyph table come from github.com/0magnet/img2txt-go/shape,
// which measures the glyphs from a real bitmap font and is shared with
// img2txt's -s option rather than copied here.

// ShapeCellW and ShapeCellH are the pixels a terminal cell covers when drawn by
// shape. They keep the 1:2 ratio of a terminal cell so that the animation's
// pixels stay square, as they are on the half-block surface.
const (
	ShapeCellW = 2
	ShapeCellH = 4
)

// asciiStr is every printable ASCII character as a string, for the same
// reason flush uses string constants: Put takes a string, and building one
// per cell per frame is what used to dominate the cost of drawing.
var asciiStr = func() [128]string {
	var t [128]string
	for i := range t {
		t[i] = string(rune(i))
	}
	return t
}()

// shapeRenderer holds what drawing by shape reuses from frame to frame.
type shapeRenderer struct {
	kernel  *shape.Kernel
	matcher *shape.Matcher
	ink     []float64 // lightness of every pixel, 0 to 1
}

func newShapeRenderer() *shapeRenderer {
	return &shapeRenderer{
		kernel:  shape.NewKernel(ShapeCellW, ShapeCellH),
		matcher: shape.NewMatcher(shape.ASCII, shape.Default),
	}
}

// lightness of a pixel, 0 to 1, with the terminal's default counting as dark:
// it is the background the characters are drawn on.
func lightness(c tcell.Color) (l float64, r, g, b int32) {
	if c == tcell.ColorDefault {
		return 0, 0, 0, 0
	}
	r, g, b = c.RGB()
	if r < 0 {
		return 0, 0, 0, 0
	}
	return (0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)) / 255, r, g, b
}

// flush draws s, which is ShapeCellW by ShapeCellH pixels per cell, as ASCII.
func (sr *shapeRenderer) flush(s *Surface, screen tcell.Screen) {
	if len(sr.ink) != len(s.px) {
		sr.ink = make([]float64, len(s.px))
	}
	for i, c := range s.px {
		sr.ink[i], _, _, _ = lightness(c)
	}
	cols, rows := s.w/ShapeCellW, s.h/ShapeCellH
	for cy := 0; cy < rows; cy++ {
		for cx := 0; cx < cols; cx++ {
			ch := sr.matcher.Pick(sr.kernel.Sample(sr.ink, s.w, s.h, cx, cy))
			if ch == ' ' {
				screen.Put(cx, cy, blankStr, tcell.StyleDefault) //nolint:errcheck // no error is possible for one cell
				continue
			}
			screen.Put(cx, cy, asciiStr[ch&0x7f], //nolint:errcheck // as above
				tcell.StyleDefault.Foreground(s.cellColor(cx, cy)))
		}
	}
}

// cellColor is the color a cell's character is drawn in: its pixels averaged
// with each weighted by the square of its lightness, so the color is that of
// the lit part the glyph draws rather than diluted by the dark around it.
func (s *Surface) cellColor(cx, cy int) tcell.Color {
	var sr, sg, sb, sw float64
	for y := cy * ShapeCellH; y < (cy+1)*ShapeCellH; y++ {
		for x := cx * ShapeCellW; x < (cx+1)*ShapeCellW; x++ {
			l, r, g, b := lightness(s.px[y*s.w+x])
			w := l * l
			sr += w * float64(r)
			sg += w * float64(g)
			sb += w * float64(b)
			sw += w
		}
	}
	if sw == 0 {
		return tcell.ColorDefault
	}
	return tcell.NewRGBColor(int32(sr/sw+0.5), int32(sg/sw+0.5), int32(sb/sw+0.5))
}

// shaped is implemented by a screen whose host wants pixel animations drawn
// by shape. It is how a command-line flag reaches every animation's Run
// without each of them growing an option: the host wraps its screen, and Run
// asks it, the same way it asks a watched screen whether it is visible.
type shaped interface {
	// ShapeASCII reports whether pixel animations should be drawn as ASCII.
	ShapeASCII() bool
}

// ShapeScreen wraps a screen so that Run draws pixel animations on it by
// shape. Glyph and Braille animations are not affected.
type ShapeScreen struct{ tcell.Screen }

// ShapeASCII reports true. See shaped.
func (ShapeScreen) ShapeASCII() bool { return true }

// RunShape drives a pixel animation drawn as ASCII by shape, on the same loop,
// keys and resize handling as Run. Like Run it does not call Init or Fini.
func RunShape(screen tcell.Screen, a Animation, opt Options) error {
	return runPixels(screen, a, opt, ShapeCellW, ShapeCellH, newShapeRenderer().flush)
}
