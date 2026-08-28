// Package canvas is the shared machinery every animation in this repository
// needs: a pixel surface twice the height of the terminal, and a frame loop
// that survives the places these run.
//
// A terminal cell is about twice as tall as it is wide, which makes a naive
// one-pixel-per-cell animation look squat and coarse. Drawing every cell as an
// upper half block instead — foreground colouring the top half, background the
// bottom — gives two independently coloured pixels per cell, roughly square,
// at no cost. Everything here is built on that.
package canvas

import (
	"time"

	"github.com/gdamore/tcell/v3"
)

// The two glyphs the surface is drawn with. tcell passes them through unchanged
// on any UTF-8 terminal, and each carries a separate foreground and background,
// which is what makes one cell into two pixels.
//
// Which one is used depends on where the colour is: a cell with both pixels lit
// is an upper block over a background, and one with a single pixel lit is
// whichever block puts the colour on the lit half and leaves the other to the
// terminal. See flush.
const (
	upperHalf = '▀'
	lowerHalf = '▄'
)

// The same two glyphs as strings, because that is what the screen is given.
//
// SetContent takes a rune and is deprecated for it: internally it does
// string(append([]rune{r}, combining...)), so every cell costs a rune slice and
// a string. Put takes the string directly, and a constant one is free — no
// allocation at all. On a full-screen surface that is twenty thousand
// allocations a frame removed, which measured as three quarters of the cost of
// drawing one.
const (
	upperHalfStr = "▀"
	// The block the other way up, for a cell whose lower pixel is lit and whose
	// upper one is not. See flush.
	lowerHalfStr = "▄"
	blankStr     = " "
)

// Surface is a grid of coloured pixels, w wide and h tall, where h is twice
// the terminal's row count. Animations draw into it and never touch the screen
// directly.
type Surface struct {
	w, h int
	px   []tcell.Color
}

// NewSurface returns a surface of w by h pixels, cleared.
func NewSurface(w, h int) *Surface {
	s := &Surface{w: w, h: h, px: make([]tcell.Color, w*h)}
	s.Clear()
	return s
}

// Size reports the surface in pixels. The terminal has half as many rows.
func (s *Surface) Size() (w, h int) { return s.w, s.h }

// Clear resets every pixel to the terminal's own background, so an animation
// that does not cover the screen sits on whatever colour the user has rather
// than on a black rectangle.
func (s *Surface) Clear() {
	for i := range s.px {
		s.px[i] = tcell.ColorDefault
	}
}

// Set colours one pixel. Coordinates outside the surface are dropped, so
// animations can be written without clamping at every call site.
func (s *Surface) Set(x, y int, c tcell.Color) {
	if x < 0 || y < 0 || x >= s.w || y >= s.h {
		return
	}
	s.px[y*s.w+x] = c
}

// At returns the colour of one pixel, or ColorDefault if out of bounds.
func (s *Surface) At(x, y int) tcell.Color {
	if x < 0 || y < 0 || x >= s.w || y >= s.h {
		return tcell.ColorDefault
	}
	return s.px[y*s.w+x]
}

// Fill colours every pixel.
func (s *Surface) Fill(c tcell.Color) {
	for i := range s.px {
		s.px[i] = c
	}
}

// flush paints the surface onto the screen, two pixel rows per cell row.
func (s *Surface) flush(screen tcell.Screen) {
	rows := s.h / 2
	for cy := 0; cy < rows; cy++ {
		top := cy * 2 * s.w
		bot := top + s.w
		for x := 0; x < s.w; x++ {
			t, b := s.px[top+x], s.px[bot+x]
			switch {
			case t == tcell.ColorDefault && b == tcell.ColorDefault:
				// Nothing here. A space leaves the terminal's background
				// showing and is cheaper for tcell to diff than a block in
				// default-on-default.
				screen.Put(x, cy, blankStr, tcell.StyleDefault) //nolint:errcheck

			case t == tcell.ColorDefault:
				// Only the lower pixel is lit, so the block is drawn the other
				// way up and the empty half becomes the background.
				//
				// Drawing it as an upper block with a default foreground does
				// not do that: a default foreground is a colour — whatever the
				// terminal writes text in — so the empty half came out solid
				// white. That is the bar that danced along the top of the fire
				// and the fringe on everything else that does not fill the
				// screen, because that edge is exactly where one pixel of a
				// cell is lit and the other is not.
				screen.Put(x, cy, lowerHalfStr, //nolint:errcheck
					tcell.StyleDefault.Foreground(b))

			case b == tcell.ColorDefault:
				// The mirror of the case above: an upper block and no
				// background, rather than a background of the default colour.
				screen.Put(x, cy, upperHalfStr, //nolint:errcheck
					tcell.StyleDefault.Foreground(t))

			default:
				st := tcell.StyleDefault.Foreground(t).Background(b)
				screen.Put(x, cy, upperHalfStr, st) //nolint:errcheck
			}
		}
	}
}

// Animation is one effect. Resize is called once before the first frame and
// again whenever the terminal changes size; Frame draws a single frame.
//
// Splitting the two means an animation allocates its buffers in Resize and
// does no allocation per frame, which matters at thirty frames a second in a
// browser.
type Animation interface {
	Resize(w, h int)
	// Frame draws one frame. dt is the seconds elapsed since the previous
	// frame, and every motion should be scaled by it.
	//
	// Advancing by elapsed time rather than by frame is what lets the target
	// rate change without changing how fast anything appears to move, and
	// what keeps a browser tab that misses ticks from running in slow motion
	// instead of simply dropping frames.
	Frame(s *Surface, dt float64)
}

// Options tunes Run. The zero value is sensible.
type Options struct {
	// FPS is the target frame rate. Zero means 60.
	//
	// It is a target and not a promise. time.Ticker drops ticks rather than
	// queueing them, so asking for more than the machine can deliver never
	// builds a backlog — and because animations advance by elapsed time
	// rather than by frame, missing ticks costs smoothness and not speed.
	//
	// 60 fits. Measured on a 200x50 terminal these effects compute a frame in
	// 0.05 to 4.3 ms, while handing a full screen to tcell costs about 6.8 ms
	// whatever is being drawn — the output path dominates and is the same for
	// all of them, so even the heaviest sits inside a 16.7 ms budget.
	FPS int
	// MinCols and MinRows refuse to run in a window too small to show
	// anything. Zero means 8 by 4.
	MinCols, MinRows int
}

// CellAnimation is an effect that draws glyphs rather than pixels.
//
// Not everything wants half blocks. Falling text is made of characters, and
// squeezing it onto a pixel surface would throw away the very thing it is
// made of. Such an animation gets the screen and the cell dimensions, and both
// shapes share the loop below.
type CellAnimation interface {
	Resize(cols, rows int)
	// Frame draws one frame. dt is the seconds since the previous frame; see
	// Animation.Frame for why it is passed rather than assumed.
	Frame(screen tcell.Screen, cols, rows int, dt float64)
}

// Run drives a pixel animation on the given screen until the user presses q,
// Escape or Ctrl-C, or the screen goes away.
//
// It does not call Init or Fini: the screen belongs to the caller. That is what
// lets the same animation run in a terminal, in a browser pane, and inside a
// host application that owns the screen already.
func Run(screen tcell.Screen, a Animation, opt Options) error {
	var surf *Surface
	return run(screen, opt,
		func(cols, rows int) {
			surf = NewSurface(cols, rows*2)
			a.Resize(cols, rows*2)
		},
		func(cols, rows int, dt float64) {
			a.Frame(surf, dt)
			surf.flush(screen)
		})
}

// RunCells drives a glyph animation. Same loop, same keys, same resize
// handling; the animation paints the screen itself.
func RunCells(screen tcell.Screen, a CellAnimation, opt Options) error {
	return run(screen, opt,
		func(cols, rows int) { a.Resize(cols, rows) },
		func(cols, rows int, dt float64) { a.Frame(screen, cols, rows, dt) })
}

// watched is implemented by a screen that knows whether anyone is looking at
// it. A host that shows several animations at once — windows in a page, panes
// in a multiplexer — can implement it to say which one is in front.
//
// This exists because the cost of an animation is nearly all in drawing it,
// and drawing is worth nothing when the result is not visible. A frame is a
// full-surface computation followed by a SetContent for every cell followed by
// a diff and a flush; skipping the whole thing for a hidden window is the
// difference between several of these coexisting and the host falling over.
// It matters most where every animation shares one thread, which is exactly
// the case in a browser.
//
// A screen that does not implement it is always drawn, so nothing changes for
// a plain terminal.
type watched interface {
	// Active reports whether frames drawn now will be seen.
	Active() bool
}

// maxStep bounds the elapsed time handed to an animation.
//
// A tab that was backgrounded, or a machine that stalled, can leave an
// arbitrarily long gap since the last frame. Passing that through would
// teleport every moving thing across the screen in one step — rain would jump
// the window, a flock would scatter. Clamping trades a brief slow-down for
// continuity, which is much less noticeable than the alternative.
const maxStep = 0.1 // seconds

// run is the loop both shapes share. resize is called before the first frame
// and on every size change; frame is called once per tick, before Show, with
// the seconds elapsed since the previous frame.
func run(screen tcell.Screen, opt Options, resize func(cols, rows int), frame func(cols, rows int, dt float64)) error {
	fps := opt.FPS
	if fps <= 0 {
		fps = 30
	}
	minCols, minRows := opt.MinCols, opt.MinRows
	if minCols <= 0 {
		minCols = 8
	}
	if minRows <= 0 {
		minRows = 4
	}

	cols, rows := screen.Size()
	if cols < minCols || rows < minRows {
		return nil
	}
	resize(cols, rows)

	// An animation has no text entry, so a cursor parked in it is only ever a
	// blinking artefact sitting on top of the picture. It belongs here rather
	// than in each host: tcell leaves the cursor wherever it was, so every
	// caller that forgot would show one, and the terminal running these is
	// often not the caller's own — a pane, a window in a page, a texture in a
	// scene. Hosts that want the cursor back get it from Fini or by asking for
	// it after this returns.
	screen.HideCursor()

	// Events come over a channel so the frame loop never blocks waiting for one.
	// tcell closes it when the screen is finalised, which is how a closed pane
	// ends this goroutine instead of leaving it spinning forever.
	events := screen.EventQ()

	tick := time.NewTicker(time.Second / time.Duration(fps))
	defer tick.Stop()

	// Measured from the wall clock rather than assumed from the tick rate:
	// the whole point is that a tick the loop did not keep up with should
	// advance the animation further, not leave it running slow.
	last := time.Now()

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			switch ev := ev.(type) {
			case *tcell.EventKey:
				// A key arrives twice where the terminal reports releases as
				// well as presses. Quitting on the release would end the
				// animation on the way out of a keystroke meant for something
				// else, and would fire twice for the one that was meant.
				if !ev.Pressed() {
					continue
				}
				switch {
				case ev.Key() == tcell.KeyEscape,
					ev.Key() == tcell.KeyCtrlC,
					ev.Str() == "q", ev.Str() == "Q":
					return nil
				}
			case *tcell.EventResize:
				cols, rows = ev.Size()
				if cols < minCols || rows < minRows {
					return nil
				}
				resize(cols, rows)
				screen.Clear()
			}
		case now := <-tick.C:
			dt := now.Sub(last).Seconds()
			last = now
			// A hidden window costs the same as a visible one unless the frame
			// is skipped outright — the expense is in drawing, and it is
			// incurred before anything reaches the screen. Time is still
			// advanced past the gap, so a window brought back to the front
			// shows where the animation would have got to rather than
			// resuming from where it was hidden.
			if w, ok := screen.(watched); ok && !w.Active() {
				continue
			}
			if dt > maxStep {
				dt = maxStep
			}
			frame(cols, rows, dt)
			screen.Show()
		}
	}
}
